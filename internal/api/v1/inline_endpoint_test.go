package v1

import (
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/webauth"
)

// TestNotesInline_MemberCannotReadCredentialStore is the end-to-end regression
// for GHSA-45w4-74p9-cj5j. A member creates a note embedding the credential
// store and reads it back with ?inline; the response must not carry the secret.
// Before the fix, ?inline base64-inlined .gosidian/auth.json (owner hash,
// plaintext TOTP secret, invite tokens) for any member.
func TestNotesInline_MemberCannotReadCredentialStore(t *testing.T) {
	f := newNotesFixture(t)

	// The real credential store, at its real location inside the vault root.
	if err := os.MkdirAll(filepath.Join(f.vaultRoot, ".gosidian"), 0o755); err != nil {
		t.Fatal(err)
	}
	secret := `{"users":[{"username":"owner","hash":"$2a$10$deadbeef","totp_secret":"MEMBERMUSTNOTSEE"}],"invites":[{"token":"inv_stolen"}]}`
	if err := os.WriteFile(filepath.Join(f.vaultRoot, ".gosidian", "auth.json"), []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}

	// An ordinary member, and a browser token for them.
	bob, err := f.webauth.AddUser("bob", "bob-Pass123!", webauth.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	// Accounts created from v2.32 on are restricted (grants only); these
	// tests exercise visibility, so lift it.
	if err := f.webauth.SetRestricted(bob.ID, false); err != nil {
		t.Fatal(err)
	}
	bobBearer, _, err := f.spaTokens.Create(bob.ID, "bob-agent")
	if err != nil {
		t.Fatal(err)
	}

	// Bob writes a note in a project he can read, embedding the credential store.
	f.seedNote(t, "scratch/pwn.md",
		"![](/vault-files/.gosidian/auth.json)\n\n![[.gosidian/auth.json]]\n")
	if err := f.projects.Set("scratch", projects.Flags{Visibility: projects.VisibilityInternal}); err != nil {
		t.Fatal(err)
	}

	w := f.request(http.MethodGet, "/api/v1/notes/scratch/pwn.md?inline", "",
		map[string]string{"Authorization": "Bearer " + bobBearer})
	body := w.Body.String()

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, body)
	}
	if strings.Contains(body, "MEMBERMUSTNOTSEE") || strings.Contains(body, "totp_secret") || strings.Contains(body, "inv_stolen") {
		t.Fatalf("credential store leaked through ?inline:\n%s", body)
	}
	if strings.Contains(body, "data:application/json") || strings.Contains(body, "data:application/octet-stream") {
		t.Fatalf("credential store inlined as data: URI:\n%s", body)
	}
}

// TestNotesInline_LegitimateImageStillInlines is the positive control: a real
// image under attachments/ must still be embedded, so the fix did not break the
// self-contained download it exists for.
func TestNotesInline_LegitimateImageStillInlines(t *testing.T) {
	f := newNotesFixture(t)

	if err := os.MkdirAll(filepath.Join(f.vaultRoot, "scratch", "attachments"), 0o755); err != nil {
		t.Fatal(err)
	}
	png, _ := base64.StdEncoding.DecodeString(inlineTestPNG)
	if err := os.WriteFile(filepath.Join(f.vaultRoot, "scratch", "attachments", "x.png"), png, 0o644); err != nil {
		t.Fatal(err)
	}
	f.seedNote(t, "scratch/pic.md", "![alt](/vault-files/scratch/attachments/x.png)\n")

	w := f.doAuthRecorder(http.MethodGet, "/api/v1/notes/scratch/pic.md?inline", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.code, w.body)
	}
	if !strings.Contains(w.body, "data:image/png;base64,") {
		t.Errorf("legitimate image was not inlined:\n%s", w.body)
	}
	if strings.Contains(w.body, "/vault-files/") {
		t.Errorf("image reference not replaced:\n%s", w.body)
	}
}
