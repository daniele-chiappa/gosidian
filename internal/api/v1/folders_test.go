package v1

import (
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/trash"
)

// seedFolderVault lays out a project with a nested folder to export or
// delete, and a note that must stay where it is.
func (f *notesFixture) seedFolderVault(t *testing.T) {
	t.Helper()
	f.seedNote(t, "Alpha/keep.md", "# keep")
	f.seedNote(t, "Alpha/sub/b.md", "# b")
	f.seedNote(t, "Alpha/sub/deep/c.md", "# c")
	f.writeRaw(t, "Alpha/sub/attachments/x.png", "PNG")
	f.writeRaw(t, "Alpha/sub/.hidden.md", "hidden")
}

func TestFolders_Export(t *testing.T) {
	f := newNotesFixture(t)
	f.seedFolderVault(t)

	rec := f.doAuthRecorder(http.MethodGet, "/api/v1/folders/Alpha/sub/export.zip", "", nil)
	if rec.code != http.StatusOK {
		t.Fatalf("export = %d %s", rec.code, rec.body)
	}
	if _, params, err := mime.ParseMediaType(rec.headers.Get("Content-Disposition")); err != nil || !strings.HasPrefix(params["filename"], "Alpha-sub-") {
		t.Errorf("Content-Disposition = %q", rec.headers.Get("Content-Disposition"))
	}
	names, _ := zipEntries(t, rec.body)
	want := []string{"sub/attachments/x.png", "sub/b.md", "sub/deep/c.md"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("entries = %v, want %v (names from the folder, hidden files left out)", names, want)
	}
	entries, _ := f.auditLog.Tail(1)
	if len(entries) != 1 || entries[0].Action != audit.ActionExport || entries[0].Path != "Alpha/sub" {
		t.Errorf("audit = %+v, want export of Alpha/sub", entries)
	}
}

func TestFolders_ExportRefuses(t *testing.T) {
	f := newNotesFixture(t)
	f.seedFolderVault(t)
	if err := os.Symlink(filepath.Join(f.vaultRoot, "Alpha", "sub"), filepath.Join(f.vaultRoot, "Alpha", "link")); err != nil {
		t.Fatal(err)
	}
	_, member := f.memberUser(t, "m1")

	cases := []struct {
		name, path, bearer string // bearer "" = the owner
		want               int
	}{
		{"member without access", "/api/v1/folders/Alpha/sub/export.zip", member, http.StatusNotFound},
		{"missing folder", "/api/v1/folders/Alpha/nope/export.zip", "", http.StatusNotFound},
		{"a note", "/api/v1/folders/Alpha/keep.md/export.zip", "", http.StatusNotFound},
		{"a symlink", "/api/v1/folders/Alpha/link/export.zip", "", http.StatusNotFound},
		{"a hidden folder", "/api/v1/folders/Alpha/.obsidian/export.zip", "", http.StatusBadRequest},
	}
	for _, c := range cases {
		bearer := c.bearer
		if bearer == "" {
			bearer = f.bearer
		}
		if rec := f.req(t, http.MethodGet, c.path, "", bearer); rec.code != c.want {
			t.Errorf("%s = %d, want %d (%s)", c.name, rec.code, c.want, rec.body)
		}
	}
	if rec := f.doAuthRecorder(http.MethodPost, "/api/v1/folders/Alpha/sub/export.zip", "", nil); rec.code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d, want 405", rec.code)
	}
}

// A note or a folder trashed from where the state dir is now set does not
// come back into it (BUG-098).
func TestTrash_RestoreIntoTheStateDir(t *testing.T) {
	f := newNotesFixture(t)
	f.router.deps.Trash = trash.New(f.vaultRoot, -1)
	f.seedNote(t, "Alpha/state/x.md", "# x")
	f.seedNote(t, "Alpha/held/y.md", "# y")
	for _, p := range []string{"/api/v1/notes/Alpha/state/x.md", "/api/v1/folders/Alpha/held"} {
		if rec := f.doAuthRecorder(http.MethodDelete, p, "", nil); rec.code != http.StatusOK && rec.code != http.StatusNoContent {
			t.Fatalf("delete %s = %d %s", p, rec.code, rec.body)
		}
	}
	f.router.deps.Vault.SetStateDir(filepath.Join(f.vaultRoot, "Alpha", "state"))
	owner := map[string]string{"Authorization": "Bearer " + f.bearer}
	id := f.trashIDFor(t, "Alpha/state/x.md", owner)
	if rec := f.doAuthRecorder(http.MethodPost, "/api/v1/trash/"+url.PathEscape(id)+"/restore", "", nil); rec.code != http.StatusBadRequest {
		t.Errorf("restore into the state dir = %d %s, want 400", rec.code, rec.body)
	}
	f.router.deps.Vault.SetStateDir(filepath.Join(f.vaultRoot, "Alpha", "held", "state"))
	id = f.trashIDFor(t, "Alpha/held", owner)
	if rec := f.doAuthRecorder(http.MethodPost, "/api/v1/trash/"+url.PathEscape(id)+"/restore", "", nil); rec.code != http.StatusBadRequest {
		t.Errorf("restore of a folder holding the state dir = %d %s, want 400", rec.code, rec.body)
	}
}

// A state dir set inside the vault under a visible name showed up as a
// project folder, and the zip of that "project" held the credentials
// (BUG-098). The vault hides it now: no project, no folder, no entry.
func TestExport_StateDirInsideTheVault(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "Alpha/a.md", "# a")
	f.writeRaw(t, "state/tokens.json", `{"secret":true}`)
	f.writeRaw(t, "Alpha/inner/auth.json", `{"secret":true}`)
	f.router.deps.Vault.SetStateDir(filepath.Join(f.vaultRoot, "state"))

	for p, want := range map[string]int{
		"/api/v1/projects/state/export.zip": http.StatusNotFound,
		// Refused like a hidden folder: not a vault path.
		"/api/v1/folders/state/export.zip": http.StatusBadRequest,
	} {
		if rec := f.doAuthRecorder(http.MethodGet, p, "", nil); rec.code != want {
			t.Errorf("%s = %d, want %d", p, rec.code, want)
		}
	}

	// Below a folder that holds it, the walk leaves it out.
	f.router.deps.Vault.SetStateDir(filepath.Join(f.vaultRoot, "Alpha", "inner"))
	rec := f.doAuthRecorder(http.MethodGet, "/api/v1/projects/Alpha/export.zip", "", nil)
	if rec.code != http.StatusOK {
		t.Fatalf("project export = %d %s", rec.code, rec.body)
	}
	if names, _ := zipEntries(t, rec.body); strings.Join(names, ",") != "Alpha/a.md" {
		t.Errorf("entries = %v, want only Alpha/a.md", names)
	}
	if rec := f.doAuthRecorder(http.MethodGet, "/api/v1/folders/Alpha/inner/export.zip", "", nil); rec.code != http.StatusBadRequest {
		t.Errorf("zip of the state dir = %d, want 400", rec.code)
	}
}

func TestFolders_DeleteAndRestore(t *testing.T) {
	f := newNotesFixture(t)
	f.router.deps.Trash = trash.New(f.vaultRoot, -1)
	f.seedFolderVault(t)

	rec := f.doAuthRecorder(http.MethodDelete, "/api/v1/folders/Alpha/sub", "", nil)
	if rec.code != http.StatusOK {
		t.Fatalf("delete = %d %s", rec.code, rec.body)
	}
	var out struct {
		TrashID string   `json:"trash_id"`
		Removed []string `json:"removed"`
	}
	if err := json.Unmarshal([]byte(rec.body), &out); err != nil {
		t.Fatal(err)
	}
	if out.TrashID == "" || len(out.Removed) != 3 {
		t.Errorf("response = %+v, want a trash id and the 3 notes", out)
	}
	if _, err := os.Stat(filepath.Join(f.vaultRoot, "Alpha", "sub")); err == nil {
		t.Error("the folder is still in the vault")
	}
	if rec := f.doAuthRecorder(http.MethodGet, "/api/v1/notes/Alpha/sub/b.md", "", nil); rec.code != http.StatusNotFound {
		t.Errorf("deleted note = %d, want 404", rec.code)
	}
	if rec := f.doAuthRecorder(http.MethodGet, "/api/v1/tree", "", nil); strings.Contains(rec.body, "Alpha/sub") || !strings.Contains(rec.body, "Alpha/keep.md") {
		t.Errorf("tree after the delete: %s", rec.body)
	}
	entries, _ := f.auditLog.Tail(1)
	if len(entries) != 1 || entries[0].Action != audit.ActionDelete || entries[0].Path != "Alpha/sub" {
		t.Errorf("audit = %+v, want delete of Alpha/sub", entries)
	}

	owner := map[string]string{"Authorization": "Bearer " + f.bearer}
	id := f.trashIDFor(t, "Alpha/sub", owner)
	if id != out.TrashID {
		t.Fatalf("trash lists %q, want %q", id, out.TrashID)
	}
	rec = f.doAuthRecorder(http.MethodPost, "/api/v1/trash/"+url.PathEscape(id)+"/restore", "", nil)
	if rec.code != http.StatusOK || strings.Contains(rec.body, "access_restored") {
		t.Fatalf("restore = %d %s, want a folder restore, not a project one", rec.code, rec.body)
	}
	if rec := f.doAuthRecorder(http.MethodGet, "/api/v1/tree", "", nil); !strings.Contains(rec.body, "Alpha/sub/deep/c.md") {
		t.Errorf("restored notes are not indexed: %s", rec.body)
	}
	if _, err := os.Stat(filepath.Join(f.vaultRoot, "Alpha/sub/attachments/x.png")); err != nil {
		t.Errorf("the attachment did not come back: %v", err)
	}

	// Recreated in the meantime: the restore refuses, nothing is merged.
	if rec := f.doAuthRecorder(http.MethodDelete, "/api/v1/folders/Alpha/sub", "", nil); rec.code != http.StatusOK {
		t.Fatalf("second delete = %d %s", rec.code, rec.body)
	}
	id = f.trashIDFor(t, "Alpha/sub", owner)
	f.seedNote(t, "Alpha/sub/new.md", "# new")
	if rec := f.doAuthRecorder(http.MethodPost, "/api/v1/trash/"+url.PathEscape(id)+"/restore", "", nil); rec.code == http.StatusOK || !strings.Contains(rec.body, "already exists") {
		t.Errorf("restore onto a recreated folder = %d %s, want a refusal", rec.code, rec.body)
	}
	if _, err := os.Stat(filepath.Join(f.vaultRoot, "Alpha/sub/b.md")); err == nil {
		t.Error("the restore merged into the recreated folder")
	}
}

func TestFolders_DeleteRefuses(t *testing.T) {
	f := newNotesFixture(t)
	f.seedFolderVault(t)
	if rec := f.doAuthRecorder(http.MethodDelete, "/api/v1/folders/Alpha/sub", "", nil); rec.code != http.StatusServiceUnavailable {
		t.Errorf("without a trash = %d, want 503", rec.code)
	}
	if _, err := os.Stat(filepath.Join(f.vaultRoot, "Alpha", "sub", "b.md")); err != nil {
		t.Fatalf("the folder went without a trash: %v", err)
	}

	f.router.deps.Trash = trash.New(f.vaultRoot, -1)
	f.setVisibility(t, "Alpha", projects.VisibilityInternal)
	_, reader := f.memberUser(t, "reader")
	f.writeRaw(t, "Alpha/held/state/tokens.json", `{"secret":true}`)
	f.router.deps.Vault.SetStateDir(filepath.Join(f.vaultRoot, "Alpha", "held", "state"))

	cases := []struct {
		name, path, bearer string // bearer "" = the owner
		want               int
	}{
		{"a project", "/api/v1/folders/Alpha", "", http.StatusBadRequest},
		{"read-only member", "/api/v1/folders/Alpha/sub", reader, http.StatusForbidden},
		{"missing folder", "/api/v1/folders/Alpha/nope", "", http.StatusNotFound},
		{"a note", "/api/v1/folders/Alpha/keep.md", "", http.StatusNotFound},
		{"holds the state dir", "/api/v1/folders/Alpha/held", "", http.StatusBadRequest},
		{"is the state dir", "/api/v1/folders/Alpha/held/state", "", http.StatusBadRequest},
	}
	for _, c := range cases {
		bearer := c.bearer
		if bearer == "" {
			bearer = f.bearer
		}
		if rec := f.req(t, http.MethodDelete, c.path, "", bearer); rec.code != c.want {
			t.Errorf("%s = %d, want %d (%s)", c.name, rec.code, c.want, rec.body)
		}
	}
	f.setVisibility(t, "Alpha", projects.VisibilityPrivate)
	if rec := f.req(t, http.MethodDelete, "/api/v1/folders/Alpha/sub", "", reader); rec.code != http.StatusNotFound {
		t.Errorf("member without access = %d, want 404", rec.code)
	}
	if _, err := os.Stat(filepath.Join(f.vaultRoot, "Alpha", "sub", "b.md")); err != nil {
		t.Errorf("a refused delete touched the folder: %v", err)
	}
}

// A trashed folder is judged like a note of its project: who can read the
// project sees it, who can write there restores or purges it. A trashed
// project still needs admin.
func TestTrash_FolderEntriesFollowTheProject(t *testing.T) {
	f := newNotesFixture(t)
	f.router.deps.Trash = trash.New(f.vaultRoot, -1)
	f.seedFolderVault(t)
	f.setVisibility(t, "Alpha", projects.VisibilityInternal)
	writerUser, writer := f.memberUser(t, "writer")
	_, reader := f.memberUser(t, "reader")
	if err := f.projects.SetMember("Alpha", writerUser.ID, projects.LevelWrite); err != nil {
		t.Fatal(err)
	}

	if rec := f.req(t, http.MethodDelete, "/api/v1/folders/Alpha/sub", "", writer); rec.code != http.StatusOK {
		t.Fatalf("delete by a writer = %d %s", rec.code, rec.body)
	}
	readerHdr := map[string]string{"Authorization": "Bearer " + reader}
	id := f.trashIDFor(t, "Alpha/sub", readerHdr)
	if id == "" {
		t.Fatal("a reader of the project does not see the trashed folder")
	}
	if rec := f.req(t, http.MethodPost, "/api/v1/trash/"+url.PathEscape(id)+"/restore", "", reader); rec.code != http.StatusForbidden {
		t.Errorf("restore by a reader = %d, want 403", rec.code)
	}
	if rec := f.req(t, http.MethodPost, "/api/v1/trash/"+url.PathEscape(id)+"/restore", "", writer); rec.code != http.StatusOK {
		t.Fatalf("restore by a writer = %d %s", rec.code, rec.body)
	}
	if rec := f.req(t, http.MethodDelete, "/api/v1/folders/Alpha/sub", "", writer); rec.code != http.StatusOK {
		t.Fatalf("second delete = %d", rec.code)
	}
	id = f.trashIDFor(t, "Alpha/sub", readerHdr)
	if rec := f.req(t, http.MethodDelete, "/api/v1/trash/"+url.PathEscape(id), "", writer); rec.code != http.StatusNoContent {
		t.Errorf("purge by a writer = %d, want 204", rec.code)
	}
}
