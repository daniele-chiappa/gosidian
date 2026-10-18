package v1

import (
	"archive/zip"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/projects"
)

// Exports with their attachments (IMP-204): the notes keep their images and
// files, their references made relative, nothing the account cannot read.

// seedAttachmentsVault lays out notes that name attachments in every way:
// of their project, of another project, as an embed, missing, outside
// attachments/, and a secret the references try to reach.
func (f *notesFixture) seedAttachmentsVault(t *testing.T) {
	t.Helper()
	f.writeRaw(t, "Alpha/attachments/p.png", "PNGDATA")
	f.writeRaw(t, "Alpha/attachments/m.pdf", "%PDF-1.7")
	f.writeRaw(t, "Beta/attachments/q.png", "QDATA")
	f.writeRaw(t, ".gosidian/auth.json", `{"secret":1}`)
	f.seedNote(t, "Alpha/docs/a.md", "# a\n\n![pic](/vault-files/Alpha/attachments/p.png)\n\n[manual](/vault-files/Alpha/attachments/m.pdf)\n\n![[p.png]]\n")
	f.seedNote(t, "Alpha/docs/sub/b.md", "# b\n\n![same](/vault-files/Alpha/attachments/p.png)\n")
	f.seedNote(t, "Alpha/docs/c.md", "# c\n\n![other](/vault-files/Beta/attachments/q.png)\n\n![gone](/vault-files/Alpha/attachments/none.png)\n\n"+
		"[note](/vault-files/Alpha/docs/a.md)\n\n![x](/vault-files/Alpha/attachments/../../.gosidian/auth.json)\n")
	f.seedNote(t, "Alpha/docs/plain.md", "# plain\n\nno files")
}

func zipText(t *testing.T, byName map[string]*zip.File, name string) string {
	t.Helper()
	e := byName[name]
	if e == nil {
		t.Fatalf("no %s in the archive", name)
	}
	rc, err := e.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	return string(b)
}

func TestExport_FolderTakesItsAttachments(t *testing.T) {
	f := newNotesFixture(t)
	f.seedAttachmentsVault(t)

	rec := f.doAuthRecorder(http.MethodGet, "/api/v1/folders/Alpha/docs/export.zip", "", nil)
	if rec.code != http.StatusOK {
		t.Fatalf("export = %d %s", rec.code, rec.body)
	}
	names, byName := zipEntries(t, rec.body)
	want := []string{"docs/a.md", "docs/attachments/m.pdf", "docs/attachments/p.png", "docs/attachments/q.png", "docs/c.md", "docs/plain.md", "docs/sub/b.md"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("entries = %v, want %v (each attachment once, the secret never)", names, want)
	}
	a := zipText(t, byName, "docs/a.md")
	for _, s := range []string{"![pic](attachments/p.png)", "[manual](attachments/m.pdf)", "![](attachments/p.png)"} {
		if !strings.Contains(a, s) {
			t.Errorf("a.md lacks %q:\n%s", s, a)
		}
	}
	if b := zipText(t, byName, "docs/sub/b.md"); !strings.Contains(b, "![same](../attachments/p.png)") {
		t.Errorf("b.md:\n%s", b)
	}
	c := zipText(t, byName, "docs/c.md")
	for _, s := range []string{"![other](attachments/q.png)", "![gone](/vault-files/Alpha/attachments/none.png)",
		"[note](/vault-files/Alpha/docs/a.md)", "/vault-files/Alpha/attachments/../../.gosidian/auth.json"} {
		if !strings.Contains(c, s) {
			t.Errorf("c.md lacks %q (a missing file, a note and the secret stay as written):\n%s", s, c)
		}
	}
	if p := zipText(t, byName, "docs/attachments/p.png"); p != "PNGDATA" {
		t.Errorf("p.png = %q", p)
	}
	if plain := zipText(t, byName, "docs/plain.md"); plain != "# plain\n\nno files" {
		t.Errorf("a note without references changed: %q", plain)
	}
	// The stored note does not change.
	if got, _ := os.ReadFile(filepath.Join(f.vaultRoot, "Alpha", "docs", "a.md")); !strings.Contains(string(got), "/vault-files/Alpha/attachments/p.png") {
		t.Errorf("the stored note changed:\n%s", got)
	}
}

// A member who reads Alpha but not Beta gets Alpha's files, and the
// reference to Beta's stays as written, its file out of the archive.
func TestExport_AttachmentsTheAccountCannotRead(t *testing.T) {
	f := newNotesFixture(t)
	f.seedAttachmentsVault(t)
	_, member := f.memberUser(t, "m1")
	f.setVisibility(t, "Alpha", projects.VisibilityInternal)

	rec := f.req(t, http.MethodGet, "/api/v1/folders/Alpha/docs/export.zip", "", member)
	if rec.code != http.StatusOK {
		t.Fatalf("export = %d %s", rec.code, rec.body)
	}
	names, byName := zipEntries(t, rec.body)
	for _, n := range names {
		if strings.Contains(n, "q.png") {
			t.Errorf("an attachment of a project the member cannot read is in the archive: %v", names)
		}
	}
	if c := zipText(t, byName, "docs/c.md"); !strings.Contains(c, "![other](/vault-files/Beta/attachments/q.png)") {
		t.Errorf("c.md:\n%s", c)
	}
}

// The zip of a project holds its attachments already: the references point
// at them; one of another project comes in beside them.
func TestExport_ProjectAndVaultLinks(t *testing.T) {
	f := newNotesFixture(t)
	f.seedAttachmentsVault(t)

	rec := f.doAuthRecorder(http.MethodGet, "/api/v1/projects/Alpha/export.zip", "", nil)
	if rec.code != http.StatusOK {
		t.Fatalf("project export = %d %s", rec.code, rec.body)
	}
	names, byName := zipEntries(t, rec.body)
	if !contains(names, "Alpha/attachments/p.png") || !contains(names, "Alpha/attachments/q.png") || contains(names, "docs/attachments/p.png") {
		t.Errorf("entries = %v", names)
	}
	if a := zipText(t, byName, "Alpha/docs/a.md"); !strings.Contains(a, "![pic](../attachments/p.png)") {
		t.Errorf("project a.md:\n%s", a)
	}
	if c := zipText(t, byName, "Alpha/docs/c.md"); !strings.Contains(c, "![other](../attachments/q.png)") {
		t.Errorf("project c.md:\n%s", c)
	}

	rec = f.doAuthRecorder(http.MethodGet, "/api/v1/admin/export.zip", "", nil)
	if rec.code != http.StatusOK {
		t.Fatalf("vault export = %d %s", rec.code, rec.body)
	}
	names, byName = zipEntries(t, rec.body)
	if c := zipText(t, byName, "Alpha/docs/c.md"); !strings.Contains(c, "![other](../../Beta/attachments/q.png)") {
		t.Errorf("vault c.md:\n%s", c)
	}
	if contains(names, "attachments/q.png") {
		t.Errorf("the vault export added a copy of a file it holds: %v", names)
	}
}

// An attachment from outside never takes the place of a file the folder
// holds under the same name.
func TestExport_AddedAttachmentDoesNotReplaceAFile(t *testing.T) {
	f := newNotesFixture(t)
	f.writeRaw(t, "Beta/attachments/q.png", "BETA")
	f.writeRaw(t, "Alpha/docs/attachments/q.png", "OWN")
	f.seedNote(t, "Alpha/docs/a.md", "![b](/vault-files/Beta/attachments/q.png)\n")

	rec := f.doAuthRecorder(http.MethodGet, "/api/v1/folders/Alpha/docs/export.zip", "", nil)
	names, byName := zipEntries(t, rec.body)
	if !contains(names, "docs/attachments/Beta-q.png") || zipText(t, byName, "docs/attachments/q.png") != "OWN" {
		t.Fatalf("entries = %v", names)
	}
	if a := zipText(t, byName, "docs/a.md"); !strings.Contains(a, "![b](attachments/Beta-q.png)") {
		t.Errorf("a.md:\n%s", a)
	}
}

// A note downloads as a zip with its attachments when it names some, as
// the file it is when it names none.
func TestExport_Note(t *testing.T) {
	f := newNotesFixture(t)
	f.router.deps.Vault.SetHTMLNotes(true)
	f.seedAttachmentsVault(t)
	f.writeRaw(t, "Alpha/page.html", `<html><body><img src="/vault-files/Alpha/attachments/p.png"><a href='/vault-files/Alpha/attachments/m.pdf'>m</a></body></html>`)

	rec := f.doAuthRecorder(http.MethodGet, "/api/v1/notes/Alpha/docs/a.md?export=zip", "", nil)
	if rec.code != http.StatusOK || rec.headers.Get("Content-Type") != "application/zip" || !strings.Contains(rec.headers.Get("Content-Disposition"), "a.zip") {
		t.Fatalf("note export = %d %q %q", rec.code, rec.headers.Get("Content-Type"), rec.headers.Get("Content-Disposition"))
	}
	names, byName := zipEntries(t, rec.body)
	if strings.Join(names, ",") != "a.md,attachments/m.pdf,attachments/p.png" {
		t.Errorf("entries = %v", names)
	}
	if a := zipText(t, byName, "a.md"); !strings.Contains(a, "![pic](attachments/p.png)") {
		t.Errorf("a.md:\n%s", a)
	}

	rec = f.doAuthRecorder(http.MethodGet, "/api/v1/notes/Alpha/docs/plain.md?export=zip", "", nil)
	if rec.code != http.StatusOK || !strings.HasPrefix(rec.headers.Get("Content-Type"), "text/markdown") || rec.body != "# plain\n\nno files" ||
		!strings.Contains(rec.headers.Get("Content-Disposition"), "plain.md") {
		t.Errorf("plain note = %d %q %q", rec.code, rec.headers.Get("Content-Type"), rec.body)
	}

	rec = f.doAuthRecorder(http.MethodGet, "/api/v1/notes/Alpha/page.html?export=zip", "", nil)
	if rec.code != http.StatusOK {
		t.Fatalf("html note export = %d %s", rec.code, rec.body)
	}
	names, byName = zipEntries(t, rec.body)
	if h := zipText(t, byName, "page.html"); !strings.Contains(h, `src="attachments/p.png"`) || !strings.Contains(h, `href='attachments/m.pdf'`) {
		t.Errorf("page.html:\n%s (entries %v)", h, names)
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
