package v1

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/authz"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/vault"
	"github.com/gosidian/gosidian/internal/webauth"
)

const inlineTestPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg=="

func TestInlineImages(t *testing.T) {
	dir := t.TempDir()
	vroot := filepath.Join(dir, "vault")
	if err := os.MkdirAll(filepath.Join(vroot, "attachments"), 0o755); err != nil {
		t.Fatal(err)
	}
	png, _ := base64.StdEncoding.DecodeString(inlineTestPNG)
	if err := os.WriteFile(filepath.Join(vroot, "attachments", "x.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := index.Open(filepath.Join(dir, "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })

	// Construct the Router directly (NewRouter would register routes and panic
	// on the nil Auth dep); inlineImages only needs Vault + Index.
	r := &Router{deps: &Deps{Vault: vault.New(vroot), Index: idx}}
	// Owner principal: passes canSee for every project. Projects store is nil
	// here, so member_scope is legacy (unenforced) and the per-note gate falls
	// back to role — an empty Principal (guest) would be denied.
	p := authz.Principal{Role: webauth.RoleOwner}

	md := "![[x.png]]\n\nlink [[note]] and ![alt](/vault-files/attachments/x.png)\n"
	out := r.inlineImages(md, "markdown", p)
	if n := strings.Count(out, "data:image/png;base64,"); n != 2 {
		t.Errorf("markdown: want 2 inlined images, got %d:\n%s", n, out)
	}
	if strings.Contains(out, "![[x.png]]") || strings.Contains(out, "/vault-files/") {
		t.Errorf("markdown image references not replaced:\n%s", out)
	}
	if !strings.Contains(out, "[[note]]") {
		t.Errorf("non-image wikilink must stay intact:\n%s", out)
	}

	html := `<p><img src="/vault-files/attachments/x.png" alt="a"></p>`
	outH := r.inlineImages(html, "html", p)
	if !strings.Contains(outH, "data:image/png;base64,") || strings.Contains(outH, "/vault-files/") {
		t.Errorf("html image not inlined:\n%s", outH)
	}

	// Unresolvable reference is left untouched (no broken data: URI).
	if got := r.inlineImages("![[nope.png]]", "markdown", p); got != "![[nope.png]]" {
		t.Errorf("unresolvable embed should be untouched, got %q", got)
	}
}

// TestInlineImages_RefusesNonImageAndOutsideAttachments is the unit-level guard
// for GHSA-45w4-74p9-cj5j: the inline helper must never embed anything that is
// not a real image under attachments/, so the credential store and arbitrary
// notes stay unreachable through ?inline — even for an owner.
func TestInlineImages_RefusesNonImageAndOutsideAttachments(t *testing.T) {
	dir := t.TempDir()
	vroot := filepath.Join(dir, "vault")
	// The machine-owned credential store, verbatim shape of the real one.
	if err := os.MkdirAll(filepath.Join(vroot, ".gosidian"), 0o755); err != nil {
		t.Fatal(err)
	}
	secret := `{"users":[{"username":"owner","totp_secret":"TOPSECRET"}]}`
	if err := os.WriteFile(filepath.Join(vroot, ".gosidian", "auth.json"), []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	// A legitimate, allowlisted, but non-image attachment.
	if err := os.MkdirAll(filepath.Join(vroot, "attachments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vroot, "attachments", "data.json"), []byte(`{"k":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	idx, err := index.Open(filepath.Join(dir, "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	r := &Router{deps: &Deps{Vault: vault.New(vroot), Index: idx}}
	p := authz.Principal{Role: webauth.RoleOwner}

	cases := []struct {
		name    string
		format  string
		content string
	}{
		{"md credential store", "markdown", "![](/vault-files/.gosidian/auth.json)"},
		{"md wikilink credential store", "markdown", "![[.gosidian/auth.json]]"},
		{"md json in attachments", "markdown", "![](/vault-files/attachments/data.json)"},
		{"html credential store", "html", `<img src="/vault-files/.gosidian/auth.json">`},
		{"html json in attachments", "html", `<img src="/vault-files/attachments/data.json">`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := r.inlineImages(tc.content, tc.format, p)
			if out != tc.content {
				t.Errorf("reference must be left untouched, got:\n%s", out)
			}
			if strings.Contains(out, "data:") {
				t.Errorf("no data: URI must be produced:\n%s", out)
			}
			if strings.Contains(out, "TOPSECRET") || strings.Contains(out, "totp_secret") {
				t.Fatalf("credential store leaked into inline output:\n%s", out)
			}
		})
	}
}

// TestInlineImages_DeniesUnauthorizedPrincipal proves the per-note authz gate:
// a principal that cannot see the attachment's project gets no data: URI, so an
// embed cannot cross a project boundary.
func TestInlineImages_DeniesUnauthorizedPrincipal(t *testing.T) {
	dir := t.TempDir()
	vroot := filepath.Join(dir, "vault")
	if err := os.MkdirAll(filepath.Join(vroot, "attachments"), 0o755); err != nil {
		t.Fatal(err)
	}
	png, _ := base64.StdEncoding.DecodeString(inlineTestPNG)
	if err := os.WriteFile(filepath.Join(vroot, "attachments", "x.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := index.Open(filepath.Join(dir, "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	r := &Router{deps: &Deps{Vault: vault.New(vroot), Index: idx}}

	// Guest principal (empty role): with Projects nil, member_scope is legacy
	// and canSee falls back to role, denying a non-member/non-owner.
	guest := authz.Principal{Role: webauth.RoleGuest}
	md := "![alt](/vault-files/attachments/x.png)"
	if out := r.inlineImages(md, "markdown", guest); out != md {
		t.Errorf("guest must not inline, got:\n%s", out)
	}
}
