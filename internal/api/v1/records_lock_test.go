package v1

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/trash"
)

// gitWrites are the audit entries git sync takes as writes of notes and of
// attachments, as cmd/gosidian's wireGitWriters reads them.
var gitWrites = map[audit.Action]bool{
	audit.ActionCreate: true, audit.ActionUpdate: true, audit.ActionAppend: true,
	audit.ActionDelete: true, audit.ActionRename: true, audit.ActionRestore: true,
	audit.ActionDeleteProject: true, audit.ActionRenameProject: true,
	audit.ActionUploadAttachment: true, audit.ActionDeleteAttachment: true,
}

// recordsUnderLock watches the writes the audit log records from now on:
// seen gives each, and free those recorded while no lock of what they name
// was held, both names of a project renamed included (BUG-147).
func (f *notesFixture) recordsUnderLock() (seen, free func() []string) {
	var mu sync.Mutex
	var all, unlocked []string
	f.auditLog.OnWrite(func(e audit.Entry) {
		if !gitWrites[e.Action] {
			return
		}
		paths := []string{e.Path}
		if e.Action == audit.ActionRename || e.Action == audit.ActionRenameProject {
			paths = append(paths, e.To)
		}
		// A restore from the trash names the entry's id: git sync reads the
		// place restored from it (BUG-148).
		if origin := trash.Origin(e.Path); e.Action == audit.ActionCreate && origin != "" && !strings.Contains(e.Path, "/") {
			paths = []string{origin}
		}
		mu.Lock()
		defer mu.Unlock()
		for _, p := range paths {
			all = append(all, string(e.Action)+" "+p)
			if !f.router.deps.Vault.PathBusy(p) {
				unlocked = append(unlocked, string(e.Action)+" "+p)
			}
		}
	})
	get := func(xs *[]string) func() []string {
		return func() []string {
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(*xs)
		}
	}
	return get(&all), get(&unlocked)
}

// A project renamed or deleted from the web UI is recorded while its names
// are still locked: a commit of git sync in between took its files as
// nobody's, "auto:", and the record, left over, gave its verb to the next
// commit (BUG-147).
func TestProjects_RecordedUnderTheirLock(t *testing.T) {
	f := newNotesFixture(t)
	_ = f.doAuthRecorder(http.MethodPost, "/api/v1/projects", `{"name":"alpha"}`, nil)
	f.seedNote(t, "alpha/n.md", "body")
	seen, free := f.recordsUnderLock()
	if w := f.doAuthRecorder(http.MethodPut, "/api/v1/projects/alpha", `{"new_name":"alpha2"}`, nil); w.code != http.StatusOK {
		t.Fatalf("rename = %d %s", w.code, w.body)
	}
	if w := f.doAuthRecorder(http.MethodDelete, "/api/v1/projects/alpha2", "", nil); w.code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", w.code, w.body)
	}
	want := []string{"rename_project alpha", "rename_project alpha2", "delete_project alpha2"}
	if got := seen(); !slices.Equal(got, want) {
		t.Fatalf("recorded %q, want %q", got, want)
	}
	if got := free(); len(got) > 0 {
		t.Errorf("recorded with no lock held: %q", got)
	}
}

// A folder deleted, and a folder or a project restored from the trash, are
// recorded while the lock of their path is held: git sync leaves their files
// for the commit after the record (BUG-148). They took no lock.
func TestTrash_RecordedUnderTheLock(t *testing.T) {
	f := newNotesFixture(t)
	f.router.deps.Trash = trash.New(f.vaultRoot, -1)
	f.seedFolderVault(t)
	_ = f.doAuthRecorder(http.MethodPost, "/api/v1/projects", `{"name":"beta"}`, nil)
	f.seedNote(t, "beta/n.md", "body")
	owner := map[string]string{"Authorization": "Bearer " + f.bearer}
	seen, free := f.recordsUnderLock()

	if w := f.doAuthRecorder(http.MethodDelete, "/api/v1/folders/Alpha/sub", "", nil); w.code != http.StatusOK {
		t.Fatalf("folder delete = %d %s", w.code, w.body)
	}
	if w := f.doAuthRecorder(http.MethodPost, "/api/v1/trash/"+url.PathEscape(f.trashIDFor(t, "Alpha/sub", owner))+"/restore", "", nil); w.code != http.StatusOK {
		t.Fatalf("folder restore = %d %s", w.code, w.body)
	}
	if w := f.doAuthRecorder(http.MethodDelete, "/api/v1/projects/beta", "", nil); w.code != http.StatusNoContent {
		t.Fatalf("project delete = %d %s", w.code, w.body)
	}
	if w := f.doAuthRecorder(http.MethodPost, "/api/v1/trash/"+url.PathEscape(f.trashIDFor(t, "beta", owner))+"/restore", "", nil); w.code != http.StatusOK {
		t.Fatalf("project restore = %d %s", w.code, w.body)
	}
	want := []string{"delete Alpha/sub", "create Alpha/sub", "delete_project beta", "create beta"}
	if got := seen(); !slices.Equal(got, want) {
		t.Fatalf("recorded %q, want %q", got, want)
	}
	if got := free(); len(got) > 0 {
		t.Errorf("recorded with no lock held: %q", got)
	}
}

// An attachment stored by the editor (/attach) or by an agent (/upload) is
// in the audit log, recorded while its lock is held, as the MCP uploads
// are (BUG-148): neither wrote an entry, so git sync took the file as
// nobody's.
func TestAttach_RecordedUnderTheLock(t *testing.T) {
	f := newUploadFixture(t)
	seen, free := f.recordsUnderLock()
	for _, route := range []string{"/api/v1/attach?project=alpha", "/api/v1/upload?project=alpha"} {
		ct, body := uploadFile(t, "shot.png", pngBody())
		if w := f.doMultipart(t, http.MethodPost, route, ct, body); w.code != http.StatusCreated {
			t.Fatalf("%s = %d %s", route, w.code, w.body)
		}
	}
	got := seen()
	if len(got) != 2 || !strings.HasPrefix(got[0], "upload_attachment alpha/attachments/") || got[1] != got[0] {
		t.Fatalf("recorded %q, want the upload of the attachment twice", got)
	}
	if got := free(); len(got) > 0 {
		t.Errorf("recorded with no lock held: %q", got)
	}
}

// The personal project of a disabled account, moved out of the way of a
// new account with its username, is recorded under the lock of both names
// too.
func TestAdminCreateUser_ReuseRecordedUnderTheLock(t *testing.T) {
	f := newNotesFixture(t)
	f.wirePersonalProjects()
	create := func(name string) map[string]any {
		t.Helper()
		rec := f.doAuthRecorder(http.MethodPost, "/api/v1/admin/users", `{"username":"`+name+`","password":"pass-1234-5678"}`, nil)
		if rec.code != http.StatusCreated {
			t.Fatalf("create %s = %d (%s)", name, rec.code, rec.body)
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(rec.body), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	first := create("alma")
	f.seedNote(t, "alma/diary.md", "old notes")
	if rec := f.doAuthRecorder(http.MethodDelete, "/api/v1/admin/users/"+first["id"].(string), "", nil); rec.code != http.StatusNoContent {
		t.Fatalf("disable = %d (%s)", rec.code, rec.body)
	}
	seen, free := f.recordsUnderLock()
	second := create("alma")
	archived, _ := second["archived_personal_project"].(string)
	if archived == "" {
		t.Fatalf("the old project did not move: %v", second)
	}
	if got, want := seen(), []string{"rename_project alma", "rename_project " + archived}; !slices.Equal(got, want) {
		t.Fatalf("recorded %q, want %q", got, want)
	}
	if got := free(); len(got) > 0 {
		t.Errorf("recorded with no lock held: %q", got)
	}
}

// A project renamed from the web UI rewrites what names its files in the
// notes its renamer may write, the project's own included, records an
// update of each note outside the project while every lock is held, and
// answers how many it rewrote and how many keep the old path, without
// naming them (BUG-153). Before, it rewrote none.
func TestProjects_RenameRewritesWhereTheUserWrites(t *testing.T) {
	f := newNotesFixture(t)
	m, bearer := f.memberUser(t, "carol")
	f.seedNote(t, "alpha/x.md", "# X\nSee [[alpha/y]].")
	f.seedNote(t, "alpha/y.md", "# Y")
	f.seedNote(t, "beta/z.md", "See [[alpha/x]].")
	f.seedNote(t, "gamma/w.md", "See [[alpha/x]].")
	for project, lvl := range map[string]string{"alpha": projects.LevelAdmin, "beta": projects.LevelWrite, "gamma": projects.LevelRead} {
		if err := f.projects.SetMember(project, m.ID, lvl); err != nil {
			t.Fatal(err)
		}
	}
	seen, free := f.recordsUnderLock()
	rec := f.request(http.MethodPut, "/api/v1/projects/alpha", `{"new_name":"delta"}`, map[string]string{"Authorization": "Bearer " + bearer})
	if rec.Code != http.StatusOK {
		t.Fatalf("rename = %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Name  string `json:"name"`
		Links struct {
			Rewritten    int `json:"rewritten"`
			NotRewritten int `json:"not_rewritten"`
		} `json:"links"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Name != "delta" || out.Links.Rewritten != 2 || out.Links.NotRewritten != 1 {
		t.Errorf("answer = %+v, want delta, 2 rewritten and 1 not", out)
	}
	if strings.Contains(rec.Body.String(), "gamma") {
		t.Errorf("the answer names a note the user may not write: %s", rec.Body.String())
	}
	for path, want := range map[string]string{"delta/x.md": "# X\nSee [[delta/y]].", "beta/z.md": "See [[delta/x]].", "gamma/w.md": "See [[alpha/x]]."} {
		if raw, err := os.ReadFile(filepath.Join(f.vaultRoot, filepath.FromSlash(path))); err != nil || string(raw) != want {
			t.Errorf("%s = %q (%v), want %q", path, raw, err, want)
		}
	}
	if got, want := seen(), []string{"rename_project alpha", "rename_project delta", "update beta/z.md"}; !slices.Equal(got, want) {
		t.Errorf("recorded %q, want %q", got, want)
	}
	if got := free(); len(got) > 0 {
		t.Errorf("recorded with no lock held: %q", got)
	}
}
