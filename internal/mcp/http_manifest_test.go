package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/projects"
)

func manifest(t *testing.T, s *Server, token, project string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/mcp/manifest?project="+project, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.handleHTTPManifest(rec, req)
	return rec
}

// mirrorServer is a server with a projects store where proj allows local
// mirrors, other does not, and hid allows them but is hidden from MCP.
func mirrorServer(t *testing.T, project string, scopes []string) (*Server, string) {
	t.Helper()
	s, token := serverWithToken(t, project, scopes)
	pstore, err := projects.Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	for name, f := range map[string]projects.Flags{
		"proj":  {AllowLocalMirror: true},
		"other": {},
		"hid":   {AllowLocalMirror: true, HiddenFromMCP: true},
	} {
		if err := pstore.Set(name, f); err != nil {
			t.Fatal(err)
		}
	}
	s.SetProjects(pstore)
	for _, p := range []string{"proj/a.md", "proj/plans/b.md", "other/c.md", "hid/d.md"} {
		if err := s.vault.Save(p, []byte("---\ntitle: "+filepath.Base(p)+"\n---\n\nbody of "+p+"\n")); err != nil {
			t.Fatal(err)
		}
	}
	// Hidden files and folders are never notes.
	if err := os.WriteFile(filepath.Join(s.vault.Root, "proj", ".secret.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.vault.Root, "proj", ".cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.vault.Root, "proj", ".cache", "e.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return s, token
}

func TestHTTPManifest_ListsReadableNotesWithETags(t *testing.T) {
	s, token := mirrorServer(t, "", []string{auth.ScopeRead})
	al, err := audit.Open(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	s.SetAuditLog(al)

	rec := manifest(t, s, token, "proj")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var m struct {
		Project string         `json:"project"`
		Notes   []manifestNote `json:"notes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.Project != "proj" || len(m.Notes) != 2 {
		t.Fatalf("manifest = %+v, want the two notes of proj", m)
	}
	for _, n := range m.Notes {
		note, err := s.vault.Load(n.Path)
		if err != nil {
			t.Fatal(err)
		}
		if n.ETag != note.ETag() || n.Size != note.Size || n.MTime == "" {
			t.Errorf("%s: etag %s size %d mtime %q, want %s %d", n.Path, n.ETag, n.Size, n.MTime, note.ETag(), note.Size)
		}
		if strings.Contains(n.Path, ".secret") || strings.Contains(n.Path, ".cache") {
			t.Errorf("hidden file listed: %s", n.Path)
		}
	}

	entries, err := al.Tail(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Action != audit.ActionMirrorSync || entries[0].Path != "proj" || entries[0].Size == 0 {
		t.Errorf("audit = %+v, want one mirror_sync on proj with the listed bytes", entries)
	}
}

func TestHTTPManifest_Refusals(t *testing.T) {
	s, token := mirrorServer(t, "", []string{auth.ScopeRead})
	scoped, scopedToken := mirrorServer(t, "other", []string{auth.ScopeRead})
	_ = scoped
	writeOnly, writeToken := mirrorServer(t, "", []string{auth.ScopeWrite})

	for _, tc := range []struct {
		name    string
		s       *Server
		token   string
		project string
		want    int
	}{
		{"no token", s, "", "proj", http.StatusUnauthorized},
		{"no read scope", writeOnly, writeToken, "proj", http.StatusForbidden},
		{"flag off", s, token, "other", http.StatusForbidden},
		{"hidden from MCP", s, token, "hid", http.StatusNotFound},
		{"outside the token's projects", scoped, scopedToken, "proj", http.StatusNotFound},
		{"missing project", s, token, "nope", http.StatusNotFound},
		{"nested path", s, token, "proj/plans", http.StatusBadRequest},
		{"hidden folder", s, token, ".gosidian", http.StatusBadRequest},
		{"empty", s, token, "", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rec := manifest(t, tc.s, tc.token, tc.project); rec.Code != tc.want {
				t.Errorf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

// /download no longer serves notes of a project hidden from MCP.
func TestHTTPDownload_HiddenProjectIsNotFound(t *testing.T) {
	s, token := mirrorServer(t, "", []string{auth.ScopeRead})
	if rec := download(t, s, token, "hid/d.md"); rec.Code != http.StatusNotFound {
		t.Errorf("hidden project download = %d, want 404", rec.Code)
	}
	if rec := download(t, s, token, "proj/a.md"); rec.Code != http.StatusOK {
		t.Errorf("visible project download = %d, want 200", rec.Code)
	}
}

// Account tokens are narrowed to the account's live access before the
// manifest checks the project: a member reads a private project only with a
// grant, a guest only a public one, and the mirror flag applies to all.
func TestHTTPManifest_FollowsAccountAccess(t *testing.T) {
	f := newAccessFixture(t)
	for _, p := range []string{"priv/a.md", "pub/b.md"} {
		if err := f.s.vault.Save(p, []byte("# "+p)); err != nil {
			t.Fatal(err)
		}
	}
	f.visibility(t, "priv", "private")
	f.visibility(t, "pub", "public")
	for _, p := range []string{"priv", "pub"} {
		fl := f.projects.Get(p)
		fl.AllowLocalMirror = true
		if err := f.projects.Set(p, fl); err != nil {
			t.Fatal(err)
		}
	}
	read := []string{auth.ScopeRead}
	owner, _ := f.token(t, "owner", nil, read)
	member, _ := f.token(t, "m1", nil, read)
	guest, _ := f.token(t, "g1", nil, read)

	for _, tc := range []struct {
		name, token, project string
		want                 int
	}{
		{"owner, private", owner, "priv", http.StatusOK},
		{"member without grant, private", member, "priv", http.StatusNotFound},
		{"guest, public", guest, "pub", http.StatusOK},
		{"guest, private", guest, "priv", http.StatusNotFound},
	} {
		if rec := manifest(t, f.s, tc.token, tc.project); rec.Code != tc.want {
			t.Errorf("%s: status = %d, want %d (%s)", tc.name, rec.Code, tc.want, rec.Body.String())
		}
	}
	f.grant(t, "priv", "m1", "read")
	if rec := manifest(t, f.s, member, "priv"); rec.Code != http.StatusOK {
		t.Errorf("member with a read grant: status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
}
