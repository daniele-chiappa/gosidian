package v1

import (
	"net/http"
	"os"
	"testing"

	"github.com/gosidian/gosidian/internal/projects"
)

// breakProjectsStore makes every future save() of the projects store fail:
// the store file is replaced by a directory, so the atomic rename over it
// cannot succeed.
func breakProjectsStore(t *testing.T, f *notesFixture) {
	t.Helper()
	p := f.projects.Path()
	_ = os.Remove(p)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

// A creator who cannot be granted access to the project they just made must
// hear about it instead of getting 201 and an empty project list (BUG-051).
func TestCreateProject_MembershipFailureSurfaces(t *testing.T) {
	f := newNotesFixture(t)
	if err := f.projects.SetMemberScope(projects.MemberScopeMembers); err != nil {
		t.Fatal(err)
	}
	_, bearer := f.memberUser(t, "creator")
	breakProjectsStore(t, f)
	rec := f.request(http.MethodPost, "/api/v1/projects", `{"name":"fresh"}`, map[string]string{"Authorization": "Bearer " + bearer})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s, want 500", rec.Code, rec.Body.String())
	}
}

// When the vault directory was renamed but the flags/members store could not
// follow, the caller must not get a 200 with an unflagged project (BUG-050).
func TestUpdateProject_RenameStoreFailureSurfaces(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "Old/a.md", "x")
	// Give the project something to carry over, otherwise Rename is a no-op
	// that never touches the store.
	if err := f.projects.SetMember("Old", f.owner.ID, projects.LevelWrite); err != nil {
		t.Fatal(err)
	}
	breakProjectsStore(t, f)
	rec := f.request(http.MethodPut, "/api/v1/projects/Old", `{"new_name":"New"}`, map[string]string{"Authorization": "Bearer " + f.bearer})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s, want 500", rec.Code, rec.Body.String())
	}
}
