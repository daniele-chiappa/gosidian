package v1

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/webauth"
)

// memberUser creates a member account and mints a SPA bearer for it.
func (f *notesFixture) memberUser(t *testing.T, name string) (*webauth.User, string) {
	t.Helper()
	u, err := f.webauth.AddUser(name, name+"-pass-1234", webauth.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	// Accounts created from v2.32 on are restricted (grants only); these
	// tests exercise visibility, so lift it.
	if err := f.webauth.SetRestricted(u.ID, false); err != nil {
		t.Fatal(err)
	}
	bearer, _, err := f.spaTokens.Create(u.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	return u, bearer
}

// setVisibility flags a project's visibility straight in the store.
func (f *notesFixture) setVisibility(t *testing.T, project, vis string) {
	t.Helper()
	fl := f.projects.Get(project)
	fl.Visibility = vis
	if err := f.projects.Set(project, fl); err != nil {
		t.Fatal(err)
	}
}

// Visibility says who may read; grants say who may write. The role is the
// ceiling. (ADR-026)
func TestProjectAccess_VisibilityAndGrants(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "Alpha/a.md", "x")
	f.seedNote(t, "Beta/b.md", "x")

	m1, bearer := f.memberUser(t, "m1")
	hdr := map[string]string{"Authorization": "Bearer " + bearer}

	// Private by default: a member without a grant sees nothing, and private
	// notes 404 (no leak).
	if rec := f.request(http.MethodGet, "/api/v1/projects", "", hdr); strings.Contains(rec.Body.String(), "Alpha") || strings.Contains(rec.Body.String(), "Beta") {
		t.Errorf("private: member must see no project: %s", rec.Body.String())
	}
	if rec := f.request(http.MethodGet, "/api/v1/notes/Beta/b.md", "", hdr); rec.Code != http.StatusNotFound {
		t.Errorf("private note read = %d want 404", rec.Code)
	}

	// Internal: every member reads, nobody writes without a grant.
	f.setVisibility(t, "Alpha", projects.VisibilityInternal)
	if rec := f.request(http.MethodGet, "/api/v1/projects", "", hdr); !strings.Contains(rec.Body.String(), `"name":"Alpha"`) || strings.Contains(rec.Body.String(), "Beta") {
		t.Errorf("internal: member must see Alpha only: %s", rec.Body.String())
	}
	if rec := f.request(http.MethodGet, "/api/v1/projects/Alpha", "", hdr); !strings.Contains(rec.Body.String(), `"access":"read"`) || !strings.Contains(rec.Body.String(), `"visibility":"internal"`) {
		t.Errorf("internal: project view must report read access: %s", rec.Body.String())
	}
	if rec := f.request(http.MethodGet, "/api/v1/notes/Alpha/a.md", "", hdr); rec.Code != http.StatusOK {
		t.Errorf("internal note read = %d want 200", rec.Code)
	}
	if rec := f.request(http.MethodPut, "/api/v1/notes/Alpha/a.md", `{"content":"y"}`, hdr); rec.Code != http.StatusForbidden {
		t.Errorf("internal note write without grant = %d want 403 (%s)", rec.Code, rec.Body.String())
	}

	// Write grant on Alpha: writes Alpha, still nothing on Beta.
	if err := f.projects.SetMember("Alpha", m1.ID, projects.LevelWrite); err != nil {
		t.Fatal(err)
	}
	if rec := f.request(http.MethodPut, "/api/v1/notes/Alpha/a.md", `{"content":"y"}`, hdr); rec.Code != http.StatusOK {
		t.Errorf("write-grant Alpha write = %d want 200 (%s)", rec.Code, rec.Body.String())
	}
	if rec := f.request(http.MethodGet, "/api/v1/projects/Alpha", "", hdr); !strings.Contains(rec.Body.String(), `"access":"write"`) {
		t.Errorf("write grant must show as write access: %s", rec.Body.String())
	}
	if rec := f.request(http.MethodPut, "/api/v1/notes/Beta/b.md", `{"content":"z"}`, hdr); rec.Code != http.StatusForbidden {
		t.Errorf("Beta write without grant = %d want 403", rec.Code)
	}

	// Read grant on the private Beta: read yes, write no.
	if err := f.projects.SetMember("Beta", m1.ID, projects.LevelRead); err != nil {
		t.Fatal(err)
	}
	if rec := f.request(http.MethodGet, "/api/v1/notes/Beta/b.md", "", hdr); rec.Code != http.StatusOK {
		t.Errorf("read-grant Beta read = %d want 200", rec.Code)
	}
	if rec := f.request(http.MethodPut, "/api/v1/notes/Beta/b.md", `{"content":"z"}`, hdr); rec.Code != http.StatusForbidden {
		t.Errorf("read-grant Beta write = %d want 403", rec.Code)
	}

	// Project settings need the admin level: a write grant is not enough.
	if rec := f.request(http.MethodPut, "/api/v1/projects/Alpha", `{"hidden_from_mcp":true}`, hdr); rec.Code != http.StatusForbidden {
		t.Errorf("settings with write grant = %d want 403 (%s)", rec.Code, rec.Body.String())
	}
	if err := f.projects.SetMember("Alpha", m1.ID, projects.LevelAdmin); err != nil {
		t.Fatal(err)
	}
	if rec := f.request(http.MethodPut, "/api/v1/projects/Alpha", `{"hidden_from_mcp":true}`, hdr); rec.Code != http.StatusOK {
		t.Errorf("settings with admin grant = %d want 200 (%s)", rec.Code, rec.Body.String())
	}
	// ... but making it public is the owner's alone.
	if rec := f.request(http.MethodPut, "/api/v1/projects/Alpha", `{"visibility":"public"}`, hdr); rec.Code != http.StatusForbidden {
		t.Errorf("public by project admin = %d want 403", rec.Code)
	}
	if rec := f.request(http.MethodPut, "/api/v1/projects/Alpha", `{"visibility":"private"}`, hdr); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"visibility":"private"`) {
		t.Errorf("private by project admin = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := f.doAuthRecorder(http.MethodPut, "/api/v1/projects/Alpha", `{"visibility":"public"}`, nil); rec.code != http.StatusOK || !strings.Contains(rec.body, `"public":true`) {
		t.Errorf("public by owner = %d (%s)", rec.code, rec.body)
	}
	// The legacy alias still works: public:false means internal.
	if rec := f.doAuthRecorder(http.MethodPut, "/api/v1/projects/Alpha", `{"public":false}`, nil); rec.code != http.StatusOK || !strings.Contains(rec.body, `"visibility":"internal"`) {
		t.Errorf("legacy public:false = %d (%s)", rec.code, rec.body)
	}
	if rec := f.doAuthRecorder(http.MethodPut, "/api/v1/projects/Alpha", `{"visibility":"secret"}`, nil); rec.code != http.StatusBadRequest {
		t.Errorf("invalid visibility = %d want 400", rec.code)
	}
}

func TestProjectMembers_CRUD(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "Shared/n.md", "x")
	m1, bearer := f.memberUser(t, "alice")
	hdr := map[string]string{"Authorization": "Bearer " + bearer}

	// Owner adds alice as a write member.
	if rec := f.doAuthRecorder(http.MethodPut, "/api/v1/projects/Shared/members", `{"user_id":"`+m1.ID+`","level":"write"}`, nil); rec.code != http.StatusCreated {
		t.Fatalf("add member = %d %s", rec.code, rec.body)
	}
	if rec := f.doAuthRecorder(http.MethodGet, "/api/v1/projects/Shared/members", "", nil); !strings.Contains(rec.body, "alice") || !strings.Contains(rec.body, "write") {
		t.Errorf("member list missing alice: %s", rec.body)
	}
	if rec := f.doAuthRecorder(http.MethodGet, "/api/v1/projects/Shared", "", nil); !strings.Contains(rec.body, `"members_count":1`) {
		t.Errorf("members_count missing: %s", rec.body)
	}

	// Non-owner cannot manage members (delegation is phase 2).
	if rec := f.request(http.MethodGet, "/api/v1/projects/Shared/members", "", hdr); rec.Code != http.StatusForbidden {
		t.Errorf("member-mgmt as non-owner = %d want 403", rec.Code)
	}

	// admin is a level; unknown levels and users are rejected.
	if rec := f.doAuthRecorder(http.MethodPut, "/api/v1/projects/Shared/members", `{"user_id":"`+m1.ID+`","level":"admin"}`, nil); rec.code != http.StatusCreated {
		t.Errorf("admin level = %d want 201 (%s)", rec.code, rec.body)
	}
	if rec := f.doAuthRecorder(http.MethodPut, "/api/v1/projects/Shared/members", `{"user_id":"`+m1.ID+`","level":"root"}`, nil); rec.code != http.StatusBadRequest {
		t.Errorf("invalid level = %d want 400", rec.code)
	}
	if rec := f.doAuthRecorder(http.MethodPut, "/api/v1/projects/Shared/members", `{"user_id":"ghost","level":"read"}`, nil); rec.code != http.StatusNotFound {
		t.Errorf("unknown user = %d want 404", rec.code)
	}

	// Remove.
	if rec := f.doAuthRecorder(http.MethodDelete, "/api/v1/projects/Shared/members/"+m1.ID, "", nil); rec.code != http.StatusNoContent {
		t.Errorf("remove member = %d want 204 (%s)", rec.code, rec.body)
	}
	if _, ok := f.projects.MemberLevel("Shared", m1.ID); ok {
		t.Error("grant not removed")
	}
}

func TestProjectMembership_DisableCascade(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "P/n.md", "x")
	m1, _ := f.memberUser(t, "bob")
	if err := f.projects.SetMember("P", m1.ID, projects.LevelWrite); err != nil {
		t.Fatal(err)
	}
	if rec := f.doAuthRecorder(http.MethodDelete, "/api/v1/admin/users/"+m1.ID, "", nil); rec.code != http.StatusNoContent {
		t.Fatalf("disable = %d %s", rec.code, rec.body)
	}
	if _, ok := f.projects.MemberLevel("P", m1.ID); ok {
		t.Error("grant not stripped on user disable")
	}
}

// The creator administers what they just made.
func TestProjectMembership_CreatorIsAdmin(t *testing.T) {
	f := newNotesFixture(t)
	_, bearer := f.memberUser(t, "carol")
	hdr := map[string]string{"Authorization": "Bearer " + bearer}

	rec := f.request(http.MethodPost, "/api/v1/projects", `{"name":"Carol"}`, hdr)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create project = %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"access":"admin"`) || !strings.Contains(rec.Body.String(), `"visibility":"private"`) {
		t.Errorf("create response = %s", rec.Body.String())
	}
	if rec := f.request(http.MethodPost, "/api/v1/notes", `{"path":"Carol/x.md","content":"c"}`, hdr); rec.Code != http.StatusCreated {
		t.Errorf("creator write to own project = %d want 201 (%s)", rec.Code, rec.Body.String())
	}
	if rec := f.request(http.MethodPut, "/api/v1/projects/Carol", `{"new_name":"Carola"}`, hdr); rec.Code != http.StatusOK {
		t.Errorf("creator rename = %d want 200 (%s)", rec.Code, rec.Body.String())
	}
	if rec := f.request(http.MethodDelete, "/api/v1/projects/Carola", "", hdr); rec.Code != http.StatusNoContent {
		t.Errorf("creator delete = %d want 204 (%s)", rec.Code, rec.Body.String())
	}
}

// New projects take the store default; the owner changes it from Settings.
func TestSettings_DefaultVisibility(t *testing.T) {
	f := newAdminFixture(t)
	if rec := f.doAuthRecorder(http.MethodGet, "/api/v1/settings", "", nil); !strings.Contains(rec.body, `"default_visibility":"private"`) {
		t.Errorf("settings must expose the default visibility: %s", rec.body)
	}
	if rec := f.doAuthRecorder(http.MethodPut, "/api/v1/settings", `{"default_visibility":"secret"}`, nil); rec.code != http.StatusBadRequest {
		t.Errorf("invalid default = %d want 400", rec.code)
	}
	if rec := f.doAuthRecorder(http.MethodPut, "/api/v1/settings", `{"default_visibility":"internal"}`, nil); rec.code != http.StatusOK || !strings.Contains(rec.body, `"default_visibility":"internal"`) {
		t.Fatalf("set default = %d (%s)", rec.code, rec.body)
	}
	if rec := f.doAuthRecorder(http.MethodPost, "/api/v1/projects", `{"name":"Fresh"}`, nil); rec.code != http.StatusCreated || !strings.Contains(rec.body, `"visibility":"internal"`) {
		t.Errorf("new project must take the default: %d (%s)", rec.code, rec.body)
	}
	if f.projects.Get("Fresh").Visibility != projects.VisibilityInternal {
		t.Error("visibility must be pinned at creation")
	}
}

// /me/access lists what the caller may read and why; the owner's per-user
// preview returns the same shape for any account.
func TestAccessViews(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "Alpha/a.md", "x")
	f.seedNote(t, "Beta/b.md", "x")
	f.seedNote(t, "Gamma/c.md", "x")
	m1, bearer := f.memberUser(t, "dave")
	hdr := map[string]string{"Authorization": "Bearer " + bearer}
	f.setVisibility(t, "Alpha", projects.VisibilityPublic)
	if err := f.projects.SetMember("Alpha", m1.ID, projects.LevelWrite); err != nil {
		t.Fatal(err)
	}
	if err := f.projects.SetMember("Beta", m1.ID, projects.LevelRead); err != nil {
		t.Fatal(err)
	}

	rec := f.request(http.MethodGet, "/api/v1/me/access", "", hdr)
	if rec.Code != http.StatusOK {
		t.Fatalf("me/access = %d (%s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"role":"member"`, `"name":"Alpha"`, `"level":"write"`, `"via":["public","grant:write"]`, `"name":"Beta"`, `"level":"read"`, `"via":["grant:read"]`} {
		if !strings.Contains(body, want) {
			t.Errorf("me/access missing %s: %s", want, body)
		}
	}
	if strings.Contains(body, "Gamma") {
		t.Errorf("me/access must not list an unreadable project: %s", body)
	}

	// Owner: everything, via owner.
	orec := f.doAuthRecorder(http.MethodGet, "/api/v1/me/access", "", nil)
	if !strings.Contains(orec.body, `"name":"Gamma"`) || !strings.Contains(orec.body, `"via":["owner"]`) || strings.Contains(orec.body, `"level":"read"`) {
		t.Errorf("owner me/access = %s", orec.body)
	}

	// Owner preview of dave; members cannot use it.
	prec := f.doAuthRecorder(http.MethodGet, "/api/v1/admin/users/"+m1.ID+"/access", "", nil)
	if prec.code != http.StatusOK || !strings.Contains(prec.body, `"user_id":"`+m1.ID+`"`) || !strings.Contains(prec.body, `"name":"Beta"`) || strings.Contains(prec.body, "Gamma") {
		t.Errorf("admin access preview = %d (%s)", prec.code, prec.body)
	}
	if rec := f.request(http.MethodGet, "/api/v1/admin/users/"+m1.ID+"/access", "", hdr); rec.Code != http.StatusForbidden {
		t.Errorf("preview as member = %d want 403", rec.Code)
	}
	if rec := f.doAuthRecorder(http.MethodGet, "/api/v1/admin/users/ghost/access", "", nil); rec.code != http.StatusNotFound {
		t.Errorf("preview of unknown user = %d want 404", rec.code)
	}

	// The users list carries the summary counts (never for the owner).
	urec := f.doAuthRecorder(http.MethodGet, "/api/v1/admin/users", "", nil)
	if !strings.Contains(urec.body, `"projects_readable":2`) || !strings.Contains(urec.body, `"projects_writable":1`) {
		t.Errorf("users list counts: %s", urec.body)
	}
}
