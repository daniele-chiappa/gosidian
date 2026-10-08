package v1

import (
	"archive/zip"
	"bytes"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/projects"
)

// writeRaw puts a file in the vault without going through the note layer,
// for the files an export copies although they are not notes.
func (f *notesFixture) writeRaw(t *testing.T, rel, content string) {
	t.Helper()
	full := filepath.Join(f.vaultRoot, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// zipEntries opens a zip body and returns its file names (sorted) and the
// entries by name.
func zipEntries(t *testing.T, body string) ([]string, map[string]*zip.File) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader([]byte(body)), int64(len(body)))
	if err != nil {
		t.Fatalf("not a valid zip: %v", err)
	}
	var names []string
	byName := map[string]*zip.File{}
	for _, f := range zr.File {
		names = append(names, f.Name)
		byName[f.Name] = f
	}
	sort.Strings(names)
	return names, byName
}

// seedExportVault lays out a project with every kind of file an export
// must copy or skip.
func (f *notesFixture) seedExportVault(t *testing.T) {
	t.Helper()
	f.seedNote(t, "Alpha/a.md", "# a\n\nalpha body")
	f.seedNote(t, "Alpha/sub/b.md", "# b")
	f.writeRaw(t, "Alpha/board.canvas", `{"nodes":[],"edges":[]}`)
	f.writeRaw(t, "Alpha/attachments/x.webp", "RIFFxxxxWEBP")
	f.writeRaw(t, "Alpha/docs/manual.pdf", "%PDF-1.7")
	f.writeRaw(t, "Alpha/.obsidian/app.json", "{}")
	f.writeRaw(t, "Alpha/.hidden.md", "hidden")
	f.writeRaw(t, "Alpha/node_modules/dep.js", "x")
	f.seedNote(t, "Beta/secret.md", "# secret")
	if err := os.Symlink(filepath.Join(f.vaultRoot, "Beta", "secret.md"), filepath.Join(f.vaultRoot, "Alpha", "link.md")); err != nil {
		t.Fatal(err)
	}
}

func TestExport_ProjectContent(t *testing.T) {
	f := newNotesFixture(t)
	f.seedExportVault(t)

	rec := f.doAuthRecorder(http.MethodGet, "/api/v1/projects/Alpha/export.zip", "", nil)
	if rec.code != http.StatusOK {
		t.Fatalf("export = %d %s", rec.code, rec.body)
	}
	if ct := rec.headers.Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q", ct)
	}
	if disp, params, err := mime.ParseMediaType(rec.headers.Get("Content-Disposition")); err != nil || disp != "attachment" || !strings.HasPrefix(params["filename"], "Alpha-") {
		t.Errorf("Content-Disposition = %q", rec.headers.Get("Content-Disposition"))
	}
	names, byName := zipEntries(t, rec.body)
	want := []string{"Alpha/a.md", "Alpha/attachments/x.webp", "Alpha/board.canvas", "Alpha/docs/manual.pdf", "Alpha/sub/b.md"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("entries = %v, want %v (hidden, node_modules, symlinks and other projects left out)", names, want)
	}
	if e := byName["Alpha/a.md"]; e != nil {
		rc, _ := e.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		if string(b) != "# a\n\nalpha body" || e.Method != zip.Deflate {
			t.Errorf("a.md = %q (method %d)", b, e.Method)
		}
	}
	if e := byName["Alpha/attachments/x.webp"]; e != nil && e.Method != zip.Store {
		t.Errorf("an already compressed format is deflated again (method %d)", e.Method)
	}

	entries, _ := f.auditLog.Tail(1)
	if len(entries) != 1 || entries[0].Action != audit.ActionExport || entries[0].Path != "Alpha" || entries[0].Size != int64(len(rec.body)) {
		t.Errorf("audit = %+v, want export of Alpha with %d bytes", entries, len(rec.body))
	}
}

// Who may export a project: an account that can read it. The open-mode
// guest may read a public project note by note, not download it in bulk.
func TestExport_ProjectAccess(t *testing.T) {
	f := newNotesFixture(t)
	f.seedExportVault(t)
	_, member := f.memberUser(t, "m1")

	if rec := f.req(t, http.MethodGet, "/api/v1/projects/Alpha/export.zip", "", member); rec.code != http.StatusNotFound {
		t.Errorf("private project, member without grant = %d, want 404", rec.code)
	}
	f.setVisibility(t, "Alpha", projects.VisibilityInternal)
	if rec := f.req(t, http.MethodGet, "/api/v1/projects/Alpha/export.zip", "", member); rec.code != http.StatusOK {
		t.Errorf("internal project, member = %d, want 200", rec.code)
	}
	if rec := f.req(t, http.MethodGet, "/api/v1/projects/Nope/export.zip", "", member); rec.code != http.StatusNotFound {
		t.Errorf("missing project = %d, want 404", rec.code)
	}
	if rec := f.doAuthRecorder(http.MethodPost, "/api/v1/projects/Alpha/export.zip", "", nil); rec.code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d, want 405", rec.code)
	}

	f.setVisibility(t, "Alpha", projects.VisibilityPublic)
	f.router.deps.Auth.OpenMode = true
	if rec := f.request(http.MethodGet, "/api/v1/projects/Alpha/export.zip", "", nil); rec.Code != http.StatusForbidden {
		t.Errorf("open-mode anonymous on a public project = %d, want 403", rec.Code)
	}
}

func TestExport_Vault(t *testing.T) {
	f := newNotesFixture(t)
	f.seedExportVault(t)
	f.writeRaw(t, ".gosidian/trash/1__Alpha%2Fold.md", "trashed")
	f.writeRaw(t, "state/tokens.json", `{"secret":true}`)
	f.writeRaw(t, "attachments/root.png", "PNG")
	f.router.deps.Vault.SetStateDir(filepath.Join(f.vaultRoot, "state"))

	rec := f.doAuthRecorder(http.MethodGet, "/api/v1/admin/export.zip", "", nil)
	if rec.code != http.StatusOK {
		t.Fatalf("vault export = %d %s", rec.code, rec.body)
	}
	names, _ := zipEntries(t, rec.body)
	joined := "," + strings.Join(names, ",") + ","
	for _, want := range []string{"Alpha/a.md", "Beta/secret.md", "attachments/root.png"} {
		if !strings.Contains(joined, ","+want+",") {
			t.Errorf("vault export lacks %s: %v", want, names)
		}
	}
	for _, leak := range []string{".gosidian", "state/", "trash"} {
		if strings.Contains(joined, leak) {
			t.Errorf("vault export contains %s: %v", leak, names)
		}
	}

	_, member := f.memberUser(t, "m1")
	if rec := f.req(t, http.MethodGet, "/api/v1/admin/export.zip", "", member); rec.code != http.StatusForbidden {
		t.Errorf("member vault export = %d, want 403", rec.code)
	}
}

func TestExport_RateLimit(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "Alpha/a.md", "# a")
	for i := 0; i < exportMax; i++ {
		if rec := f.doAuthRecorder(http.MethodGet, "/api/v1/projects/Alpha/export.zip", "", nil); rec.code != http.StatusOK {
			t.Fatalf("export %d = %d", i+1, rec.code)
		}
	}
	rec := f.doAuthRecorder(http.MethodGet, "/api/v1/admin/export.zip", "", nil)
	if rec.code != http.StatusTooManyRequests || rec.headers.Get("Retry-After") == "" {
		t.Errorf("export over the limit = %d (Retry-After %q), want 429 with Retry-After", rec.code, rec.headers.Get("Retry-After"))
	}
}

// A name with a backslash is left out (Windows extractors would read it as
// a path); a non-ASCII project name still yields a usable file name.
func TestExport_NamesAndFilename(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "Città/a.md", "# a")
	f.writeRaw(t, `Città/evil\..\..\x.md`, "x")

	rec := f.doAuthRecorder(http.MethodGet, "/api/v1/projects/Citt%C3%A0/export.zip", "", nil)
	if rec.code != http.StatusOK {
		t.Fatalf("export = %d %s", rec.code, rec.body)
	}
	names, _ := zipEntries(t, rec.body)
	if strings.Join(names, ",") != "Città/a.md" {
		t.Errorf("entries = %v, want only Città/a.md", names)
	}
	_, params, err := mime.ParseMediaType(rec.headers.Get("Content-Disposition"))
	if err != nil || !strings.HasPrefix(params["filename"], "Città-") {
		t.Errorf("Content-Disposition = %q (%v)", rec.headers.Get("Content-Disposition"), err)
	}
}

// Once the 200 is out, a failure must not end the body cleanly: the client
// would save a truncated zip as a complete one.
func TestExport_FailureAbortsTheDownload(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "Alpha/a.md", "# a")
	f.writeRaw(t, "Alpha/b.pdf", "%PDF")
	orig := exportOpen
	exportOpen = func(name string) (*os.File, error) {
		if strings.HasSuffix(name, "b.pdf") {
			return nil, os.ErrPermission
		}
		return orig(name)
	}
	t.Cleanup(func() { exportOpen = orig })

	srv := httptest.NewServer(f.router)
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/projects/Alpha/export.zip", nil)
	req.Header.Set("Authorization", "Bearer "+f.bearer)
	// The connection drops: before the headers when nothing was flushed
	// yet, mid-body otherwise. Either way the client sees an error.
	res, err := srv.Client().Do(req)
	if err == nil {
		defer res.Body.Close()
		_, err = io.ReadAll(res.Body)
	}
	if err == nil {
		t.Error("a failed export reached the client as a complete download")
	}
	if entries, _ := f.auditLog.Tail(1); len(entries) != 1 || entries[0].Action != audit.ActionExport {
		t.Errorf("a failed export is not audited: %+v", entries)
	}
}
