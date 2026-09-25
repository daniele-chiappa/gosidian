package v1

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/webauth"
)

// seedTwoProjects seeds one public ("pubproj") and one private ("privproj")
// project, each with a note carrying a shared search keyword, then provisions a
// guest user and returns its bearer token. The guest must see pubproj only.
func (f *notesFixture) seedTwoProjects(t *testing.T) string {
	t.Helper()
	f.seedNote(t, "pubproj/welcome.md", "# Welcome\nunicorn keyword in public")
	f.seedNote(t, "privproj/secret.md", "# Secret\nunicorn keyword in private")
	if err := f.projects.Set("pubproj", projects.Flags{Visibility: projects.VisibilityPublic}); err != nil {
		t.Fatal(err)
	}
	u, err := f.webauth.AddUser("guest1", "guest-pass-123", webauth.RoleGuest)
	if err != nil {
		t.Fatal(err)
	}
	// Accounts created from v2.32 on are restricted (grants only); these
	// tests exercise visibility, so lift it.
	if err := f.webauth.SetRestricted(u.ID, false); err != nil {
		t.Fatal(err)
	}
	bearer, _, err := f.spaTokens.Create(u.ID, "guest-agent")
	if err != nil {
		t.Fatal(err)
	}
	return bearer
}

// req issues an authenticated request with the given bearer and returns the
// recorder. Mirrors doAuthRecorder but lets the caller pick the token (so we
// can drive requests as a guest instead of the fixture owner).
func (f *notesFixture) req(t *testing.T, method, path, body, bearer string) *recorder {
	t.Helper()
	w := f.request(method, path, body, map[string]string{"Authorization": "Bearer " + bearer})
	return &recorder{code: w.Code, body: w.Body.String(), headers: w.Header()}
}

func TestGuestTreeSeesOnlyPublic(t *testing.T) {
	f := newNotesFixture(t)
	guest := f.seedTwoProjects(t)

	rec := f.req(t, http.MethodGet, "/api/v1/tree", "", guest)
	if rec.code != http.StatusOK {
		t.Fatalf("guest tree status=%d body=%s", rec.code, rec.body)
	}
	if !strings.Contains(rec.body, "pubproj") {
		t.Errorf("guest tree missing public project: %s", rec.body)
	}
	if strings.Contains(rec.body, "privproj") {
		t.Errorf("guest tree LEAKED private project: %s", rec.body)
	}

	// Owner regression: both projects visible.
	orec := f.doAuthRecorder(http.MethodGet, "/api/v1/tree", "", nil)
	if !strings.Contains(orec.body, "pubproj") || !strings.Contains(orec.body, "privproj") {
		t.Errorf("owner tree should show both projects: %s", orec.body)
	}
}

func TestGuestNoteAccess(t *testing.T) {
	f := newNotesFixture(t)
	guest := f.seedTwoProjects(t)

	// Public note → 200.
	if rec := f.req(t, http.MethodGet, "/api/v1/notes/pubproj/welcome.md", "", guest); rec.code != http.StatusOK {
		t.Errorf("guest GET public note status=%d want 200 (%s)", rec.code, rec.body)
	}
	// Private note → 404 (hide existence, not 403).
	if rec := f.req(t, http.MethodGet, "/api/v1/notes/privproj/secret.md", "", guest); rec.code != http.StatusNotFound {
		t.Errorf("guest GET private note status=%d want 404 (%s)", rec.code, rec.body)
	}
}

func TestGuestCannotWrite(t *testing.T) {
	f := newNotesFixture(t)
	guest := f.seedTwoProjects(t)

	// Create in a PUBLIC project must still be denied — guests are read-only.
	body := `{"path":"pubproj/new-note.md","content":"x"}`
	if rec := f.req(t, http.MethodPost, "/api/v1/notes", body, guest); rec.code != http.StatusForbidden {
		t.Errorf("guest POST note status=%d want 403 (%s)", rec.code, rec.body)
	}
	// Delete a public note → 403.
	if rec := f.req(t, http.MethodDelete, "/api/v1/notes/pubproj/welcome.md", "", guest); rec.code != http.StatusForbidden {
		t.Errorf("guest DELETE note status=%d want 403 (%s)", rec.code, rec.body)
	}
	// Create a project → 403.
	if rec := f.req(t, http.MethodPost, "/api/v1/projects", `{"name":"x"}`, guest); rec.code != http.StatusForbidden {
		t.Errorf("guest POST project status=%d want 403 (%s)", rec.code, rec.body)
	}
}

func TestGuestSearchFiltered(t *testing.T) {
	f := newNotesFixture(t)
	guest := f.seedTwoProjects(t)

	rec := f.req(t, http.MethodGet, "/api/v1/search?q=unicorn", "", guest)
	if rec.code != http.StatusOK {
		t.Fatalf("guest search status=%d body=%s", rec.code, rec.body)
	}
	if !strings.Contains(rec.body, "pubproj") {
		t.Errorf("guest search missing public hit: %s", rec.body)
	}
	if strings.Contains(rec.body, "privproj") {
		t.Errorf("guest search LEAKED private hit: %s", rec.body)
	}
}

func TestGuestProjectsListAndGetFiltered(t *testing.T) {
	f := newNotesFixture(t)
	guest := f.seedTwoProjects(t)

	rec := f.req(t, http.MethodGet, "/api/v1/projects", "", guest)
	if rec.code != http.StatusOK {
		t.Fatalf("guest projects status=%d body=%s", rec.code, rec.body)
	}
	if !strings.Contains(rec.body, "pubproj") {
		t.Errorf("guest projects list missing public: %s", rec.body)
	}
	if strings.Contains(rec.body, "privproj") {
		t.Errorf("guest projects list LEAKED private: %s", rec.body)
	}
	// GET a private project directly → 404.
	if rec := f.req(t, http.MethodGet, "/api/v1/projects/privproj", "", guest); rec.code != http.StatusNotFound {
		t.Errorf("guest GET private project status=%d want 404 (%s)", rec.code, rec.body)
	}
	// GET a public project → 200.
	if rec := f.req(t, http.MethodGet, "/api/v1/projects/pubproj", "", guest); rec.code != http.StatusOK {
		t.Errorf("guest GET public project status=%d want 200 (%s)", rec.code, rec.body)
	}
}

func TestGuestSettingsDenied(t *testing.T) {
	f := newNotesFixture(t)
	guest := f.seedTwoProjects(t)
	if rec := f.req(t, http.MethodGet, "/api/v1/settings", "", guest); rec.code != http.StatusForbidden {
		t.Errorf("guest GET settings status=%d want 403 (%s)", rec.code, rec.body)
	}
}

func TestGuestDeniedAdmin(t *testing.T) {
	f := newNotesFixture(t)
	guest := f.seedTwoProjects(t)
	// Owner-only admin surface must reject a guest (403, via requireOwner).
	if rec := f.req(t, http.MethodGet, "/api/v1/admin/users", "", guest); rec.code != http.StatusForbidden {
		t.Errorf("guest GET admin/users status=%d want 403 (%s)", rec.code, rec.body)
	}
}

func TestAdminRoleEdit(t *testing.T) {
	f := newNotesFixture(t)
	u, err := f.webauth.AddUser("toedit", "edit-pass-123", webauth.RoleGuest)
	if err != nil {
		t.Fatal(err)
	}
	// Accounts created from v2.32 on are restricted (grants only); these
	// tests exercise visibility, so lift it.
	if err := f.webauth.SetRestricted(u.ID, false); err != nil {
		t.Fatal(err)
	}

	// Promote guest → member (owner action via the fixture owner bearer).
	rec := f.doAuthRecorder(http.MethodPatch, "/api/v1/admin/users/"+u.ID, `{"role":"member"}`, nil)
	if rec.code != http.StatusOK {
		t.Fatalf("promote status=%d body=%s", rec.code, rec.body)
	}
	if !strings.Contains(rec.body, `"role":"member"`) {
		t.Errorf("response should show member role: %s", rec.body)
	}
	if uu, ok := f.webauth.UserByID(u.ID); !ok || uu.Role != webauth.RoleMember {
		t.Errorf("role not persisted to member")
	}

	// Demote member → guest.
	rec = f.doAuthRecorder(http.MethodPatch, "/api/v1/admin/users/"+u.ID, `{"role":"guest"}`, nil)
	if rec.code != http.StatusOK {
		t.Fatalf("demote status=%d body=%s", rec.code, rec.body)
	}
	if uu, ok := f.webauth.UserByID(u.ID); !ok || uu.Role != webauth.RoleGuest {
		t.Errorf("role not persisted to guest")
	}

	// Assigning 'owner' via this endpoint is rejected (400).
	if rec := f.doAuthRecorder(http.MethodPatch, "/api/v1/admin/users/"+u.ID, `{"role":"owner"}`, nil); rec.code != http.StatusBadRequest {
		t.Errorf("assign owner status=%d want 400 (%s)", rec.code, rec.body)
	}

	// The owner's own role is immutable (403).
	if rec := f.doAuthRecorder(http.MethodPatch, "/api/v1/admin/users/"+f.owner.ID, `{"role":"member"}`, nil); rec.code != http.StatusForbidden {
		t.Errorf("change owner role status=%d want 403 (%s)", rec.code, rec.body)
	}
}

// BUG-058: a non-owner's search is scoped inside the index query, so a big
// unreadable project outranking everything cannot empty the result.
func TestSearchScopedBeforeLimit(t *testing.T) {
	f := newNotesFixture(t)
	guest := f.seedTwoProjects(t)
	for k := 0; k < 40; k++ {
		f.seedNote(t, fmt.Sprintf("privproj/z%02d.md", k), fmt.Sprintf("# Zephyr %d\nzephyr zephyr", k))
	}
	for k := 0; k < 3; k++ {
		f.seedNote(t, fmt.Sprintf("pubproj/z%d.md", k), fmt.Sprintf("# Public %d\nlong prose that mentions zephyr once", k))
	}
	rec := f.req(t, http.MethodGet, "/api/v1/search?q=zephyr&limit=2", "", guest)
	if rec.code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.code, rec.body)
	}
	if strings.Count(rec.body, `"pubproj/z`) != 2 || strings.Contains(rec.body, "privproj") {
		t.Errorf("guest search = %s, want 2 public hits and no private", rec.body)
	}
	rec = f.req(t, http.MethodGet, "/api/v1/note-titles?q=zephyr&limit=5", "", guest)
	if rec.code != http.StatusOK || strings.Count(rec.body, `"pubproj/z`) != 3 || strings.Contains(rec.body, "privproj") {
		t.Errorf("guest note-titles = %d %s, want the 3 public notes", rec.code, rec.body)
	}
}
