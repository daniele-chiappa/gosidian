package v1

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/webauth"
)

// The vault layer must never address a dot-directory: the credential store
// (.gosidian/) and the git metadata (.git/) live inside the vault root and
// are not notes. These tests pin the structural invariant on the HTTP API
// (IMP-076 / follow-up to GHSA-45w4-74p9-cj5j).

func dotdirWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNotes_DotDirs_NobodyCanReadCredentialStoreDirectly(t *testing.T) {
	f := newNotesFixture(t)
	dotdirWrite(t, filepath.Join(f.vaultRoot, ".gosidian", "auth.json"),
		`{"users":[{"username":"owner","hash":"$2a$10$deadbeef","totp_secret":"MEMBERMUSTNOTSEE"}]}`)
	dotdirWrite(t, filepath.Join(f.vaultRoot, ".git", "config"),
		"[remote \"origin\"]\n\turl = https://x:GITTOKENMUSTNOTSEE@git.example.com/v.git\n")

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
	bearers := map[string]string{"member": bobBearer, "owner": f.bearer}
	for who, bearer := range bearers {
		for _, p := range []string{".gosidian/auth.json", ".git/config"} {
			w := f.request(http.MethodGet, "/api/v1/notes/"+p, "",
				map[string]string{"Authorization": "Bearer " + bearer})
			body := w.Body.String()
			// Rel rejects the path before any lookup, exactly like "..": the
			// API answers 400 for the invalid path — never 200, never a body.
			if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
				t.Errorf("%s GET %s: status=%d (want 400/404)", who, p, w.Code)
			}
			if strings.Contains(body, "MUSTNOTSEE") {
				t.Errorf("%s GET %s: secret leaked in body", who, p)
			}
		}
	}
}

func TestNotes_DotDirs_MemberCannotDeleteCredentialStore(t *testing.T) {
	f := newNotesFixture(t)
	authPath := filepath.Join(f.vaultRoot, ".gosidian", "auth.json")
	dotdirWrite(t, authPath, `{"users":[]}`)

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
	w := f.request(http.MethodDelete, "/api/v1/notes/.gosidian/auth.json", "",
		map[string]string{"Authorization": "Bearer " + bobBearer})
	if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
		t.Errorf("DELETE .gosidian/auth.json: status=%d (want 400/404) body=%s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(authPath); err != nil {
		t.Fatalf("credential store was removed through the notes API: %v", err)
	}
}
