package v1

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/trash"
)

// trashIDFor returns the id of the trash entry discarded from origin, as the
// account behind hdr lists it ("" when it does not see it).
func (f *notesFixture) trashIDFor(t *testing.T, origin string, hdr map[string]string) string {
	t.Helper()
	rec := f.request(http.MethodGet, "/api/v1/trash", "", hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("trash list = %d (%s)", rec.Code, rec.Body.String())
	}
	var out struct {
		Items []trashView `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, e := range out.Items {
		if e.OriginPath == origin {
			return e.ID
		}
	}
	return ""
}

// A trashed project is judged on the access it had when deleted: with the
// default visibility internal, another member used to see it in the trash,
// restore it (as internal: exposed) or purge it. Now only who could read it
// sees it, only an admin of it restores or purges it, and it comes back
// private with its grants (IMP-124).
func TestTrash_ProjectEntriesFollowSavedAccess(t *testing.T) {
	f := newNotesFixture(t)
	f.router.deps.Trash = trash.New(f.vaultRoot, -1)
	if err := f.projects.SetDefaultVisibility(projects.VisibilityInternal); err != nil {
		t.Fatal(err)
	}
	_, aliceBearer := f.memberUser(t, "alice")
	_, bobBearer := f.memberUser(t, "bob")
	alice := map[string]string{"Authorization": "Bearer " + aliceBearer}
	bob := map[string]string{"Authorization": "Bearer " + bobBearer}
	owner := map[string]string{"Authorization": "Bearer " + f.bearer}

	if rec := f.request(http.MethodPost, "/api/v1/projects", `{"name":"Secret"}`, alice); rec.Code != http.StatusCreated {
		t.Fatalf("create = %d (%s)", rec.Code, rec.Body.String())
	}
	f.setVisibility(t, "Secret", projects.VisibilityPrivate)
	if rec := f.request(http.MethodPost, "/api/v1/notes", `{"path":"Secret/x.md","content":"s"}`, alice); rec.Code != http.StatusCreated {
		t.Fatalf("note = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := f.request(http.MethodDelete, "/api/v1/projects/Secret", "", alice); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d (%s)", rec.Code, rec.Body.String())
	}

	id := f.trashIDFor(t, "Secret", alice)
	if id == "" {
		t.Fatal("the project's admin does not see it in the trash")
	}
	if got := f.trashIDFor(t, "Secret", bob); got != "" {
		t.Error("another member sees the trashed private project")
	}
	if rec := f.request(http.MethodPost, "/api/v1/trash/"+id+"/restore", "", bob); rec.Code != http.StatusNotFound {
		t.Errorf("restore by another member = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
	if rec := f.request(http.MethodDelete, "/api/v1/trash/"+id, "", bob); rec.Code != http.StatusNotFound {
		t.Errorf("purge by another member = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}

	rec := f.request(http.MethodPost, "/api/v1/trash/"+id+"/restore", "", alice)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"access_restored":true`) {
		t.Fatalf("restore by its admin = %d (%s)", rec.Code, rec.Body.String())
	}
	if v := f.projects.Visibility("Secret"); v != projects.VisibilityPrivate {
		t.Errorf("restored visibility = %q, want private", v)
	}
	if rec := f.request(http.MethodGet, "/api/v1/notes/Secret/x.md", "", bob); rec.Code != http.StatusNotFound {
		t.Errorf("another member reads the restored note = %d, want 404", rec.Code)
	}
	if rec := f.request(http.MethodGet, "/api/v1/notes/Secret/x.md", "", alice); rec.Code != http.StatusOK {
		t.Errorf("its admin reads the restored note = %d, want 200", rec.Code)
	}

	// A project trashed without saved access (before IMP-124) is the
	// owner's alone, and comes back private to whoever restores it.
	f.seedNote(t, "Legacy/n.md", "l")
	if _, _, err := f.router.deps.Trash.DiscardProject("Legacy", nil); err != nil {
		t.Fatal(err)
	}
	if got := f.trashIDFor(t, "Legacy", alice); got != "" {
		t.Error("a member sees a project trashed without saved access")
	}
	legacy := f.trashIDFor(t, "Legacy", owner)
	if legacy == "" {
		t.Fatal("the owner does not see the legacy entry")
	}
	rec = f.request(http.MethodPost, "/api/v1/trash/"+legacy+"/restore", "", owner)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"access_restored":false`) {
		t.Fatalf("owner restore = %d (%s)", rec.Code, rec.Body.String())
	}
	if v := f.projects.Visibility("Legacy"); v != projects.VisibilityPrivate {
		t.Errorf("legacy visibility = %q, want private (not the internal default)", v)
	}

	// A note whose project is gone waits for the project.
	f.seedNote(t, "Gone/n.md", "g")
	nid, err := f.router.deps.Trash.DiscardNote("Gone/n.md")
	if err != nil {
		t.Fatal(err)
	}
	if rec := f.request(http.MethodDelete, "/api/v1/projects/Gone", "", owner); rec.Code != http.StatusNoContent {
		t.Fatalf("delete Gone = %d (%s)", rec.Code, rec.Body.String())
	}
	// The id holds a literal %2F; the SPA escapes it the same way.
	if rec := f.request(http.MethodPost, "/api/v1/trash/"+url.PathEscape(nid)+"/restore", "", owner); rec.Code != http.StatusConflict {
		t.Errorf("restore of a note whose project is gone = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
}

// A note trashed from a project belongs to that project: a later project
// that takes the name (another account's, or a personal project on username
// reuse) does not see, restore or read it (IMP-124).
func TestTrash_NoteOfAnEarlierProjectStaysTheOwners(t *testing.T) {
	f := newNotesFixture(t)
	f.router.deps.Trash = trash.New(f.vaultRoot, -1)
	_, aliceBearer := f.memberUser(t, "alice")
	_, bobBearer := f.memberUser(t, "bob")
	alice := map[string]string{"Authorization": "Bearer " + aliceBearer}
	bob := map[string]string{"Authorization": "Bearer " + bobBearer}
	owner := map[string]string{"Authorization": "Bearer " + f.bearer}

	if rec := f.request(http.MethodPost, "/api/v1/projects", `{"name":"Secret"}`, alice); rec.Code != http.StatusCreated {
		t.Fatalf("create = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := f.request(http.MethodPost, "/api/v1/notes", `{"path":"Secret/x.md","content":"alice's"}`, alice); rec.Code != http.StatusCreated {
		t.Fatalf("note = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := f.request(http.MethodDelete, "/api/v1/notes/Secret/x.md", "", alice); rec.Code != http.StatusNoContent {
		t.Fatalf("trash note = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := f.request(http.MethodDelete, "/api/v1/projects/Secret", "", alice); rec.Code != http.StatusNoContent {
		t.Fatalf("delete project = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := f.request(http.MethodPost, "/api/v1/projects", `{"name":"Secret"}`, bob); rec.Code != http.StatusCreated {
		t.Fatalf("bob creates Secret = %d (%s)", rec.Code, rec.Body.String())
	}
	if id := f.trashIDFor(t, "Secret/x.md", bob); id != "" {
		t.Fatal("the new project's creator sees the earlier project's trashed note")
	}
	id := f.trashIDFor(t, "Secret/x.md", owner)
	if id == "" {
		t.Fatal("the owner does not see the note")
	}
	if rec := f.request(http.MethodPost, "/api/v1/trash/"+url.PathEscape(id)+"/restore", "", bob); rec.Code != http.StatusNotFound {
		t.Errorf("bob restores it = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
	if rec := f.request(http.MethodGet, "/api/v1/notes/Secret/x.md", "", bob); rec.Code != http.StatusNotFound {
		t.Errorf("bob reads it = %d, want 404", rec.Code)
	}
}

// Who could read a trashed project sees it, but restoring or purging it
// takes admin on it, as its delete did.
func TestTrash_ReadersSeeButOnlyAdminsRestore(t *testing.T) {
	f := newNotesFixture(t)
	f.router.deps.Trash = trash.New(f.vaultRoot, -1)
	if err := f.projects.SetDefaultVisibility(projects.VisibilityInternal); err != nil {
		t.Fatal(err)
	}
	_, aliceBearer := f.memberUser(t, "alice")
	_, bobBearer := f.memberUser(t, "bob")
	alice := map[string]string{"Authorization": "Bearer " + aliceBearer}
	bob := map[string]string{"Authorization": "Bearer " + bobBearer}
	if rec := f.request(http.MethodPost, "/api/v1/projects", `{"name":"Shared"}`, alice); rec.Code != http.StatusCreated {
		t.Fatalf("create = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := f.request(http.MethodDelete, "/api/v1/projects/Shared", "", alice); rec.Code != http.StatusNoContent {
		t.Fatalf("delete = %d (%s)", rec.Code, rec.Body.String())
	}
	id := f.trashIDFor(t, "Shared", bob)
	if id == "" {
		t.Fatal("a reader of the internal project does not see it in the trash")
	}
	if rec := f.request(http.MethodPost, "/api/v1/trash/"+id+"/restore", "", bob); rec.Code != http.StatusForbidden {
		t.Errorf("restore by a reader = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	if rec := f.request(http.MethodDelete, "/api/v1/trash/"+id, "", bob); rec.Code != http.StatusForbidden {
		t.Errorf("purge by a reader = %d, want 403 (%s)", rec.Code, rec.Body.String())
	}
	if rec := f.request(http.MethodDelete, "/api/v1/trash/"+id, "", alice); rec.Code != http.StatusNoContent {
		t.Errorf("purge by its admin = %d, want 204 (%s)", rec.Code, rec.Body.String())
	}
}
