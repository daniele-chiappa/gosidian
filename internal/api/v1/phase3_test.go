package v1

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/webauth"
)

// wirePersonalProjects installs the creation hook the way main does, so
// accounts created through the API get their personal project.
func (f *notesFixture) wirePersonalProjects() {
	f.webauth.SetOnUserCreated(func(u webauth.User) {
		_, _ = ProvisionPersonalProject(f.router.deps.Vault, f.projects, f.router.deps.Audit, u, false)
	})
}

// A restricted account ignores visibility: internal and public projects stay
// out of reach until a grant arrives; lifting the flag brings them back.
func TestRestrictedAccount_SeesGrantsOnly(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "Open/a.md", "x")
	f.seedNote(t, "Team/b.md", "x")
	f.seedNote(t, "Mine/c.md", "x")
	f.setVisibility(t, "Open", projects.VisibilityPublic)
	f.setVisibility(t, "Team", projects.VisibilityInternal)

	// Created straight in the store: restricted by default.
	u, err := f.webauth.AddUser("rita", "rita-pass-1234", webauth.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	bearer, _, err := f.spaTokens.Create(u.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	hdr := map[string]string{"Authorization": "Bearer " + bearer}

	rec := f.request(http.MethodGet, "/api/v1/me/access", "", hdr)
	if !strings.Contains(rec.Body.String(), `"restricted":true`) || strings.Contains(rec.Body.String(), `"name":"Open"`) || strings.Contains(rec.Body.String(), `"name":"Team"`) {
		t.Errorf("restricted account must see no project by visibility: %s", rec.Body.String())
	}
	if rec := f.request(http.MethodGet, "/api/v1/notes/Open/a.md", "", hdr); rec.Code != http.StatusNotFound {
		t.Errorf("restricted read of a public note = %d want 404", rec.Code)
	}
	if err := f.projects.SetMember("Mine", u.ID, projects.LevelWrite); err != nil {
		t.Fatal(err)
	}
	if rec := f.request(http.MethodGet, "/api/v1/me/access", "", hdr); !strings.Contains(rec.Body.String(), `"name":"Mine"`) {
		t.Errorf("grant must still work for a restricted account: %s", rec.Body.String())
	}

	// The owner lifts the flag: visibility applies again.
	if rec := f.doAuthRecorder(http.MethodPatch, "/api/v1/admin/users/"+u.ID, `{"restricted":false}`, nil); rec.code != http.StatusOK {
		t.Fatalf("unrestrict = %d (%s)", rec.code, rec.body)
	}
	rec = f.request(http.MethodGet, "/api/v1/me/access", "", hdr)
	if !strings.Contains(rec.Body.String(), `"restricted":false`) || !strings.Contains(rec.Body.String(), `"name":"Open"`) || !strings.Contains(rec.Body.String(), `"name":"Team"`) {
		t.Errorf("unrestricted account must see internal and public: %s", rec.Body.String())
	}
	// The users list reports the flags.
	if rec := f.doAuthRecorder(http.MethodGet, "/api/v1/admin/users", "", nil); !strings.Contains(rec.body, `"restricted":false`) || !strings.Contains(rec.body, `"can_create_projects":true`) {
		t.Errorf("users list flags: %s", rec.body)
	}
	if rec := f.doAuthRecorder(http.MethodPatch, "/api/v1/admin/users/"+f.owner.ID, `{"restricted":true}`, nil); rec.code != http.StatusForbidden {
		t.Errorf("restricting the owner = %d want 403", rec.code)
	}
}

// Creating an account provisions its personal project: private, admin
// grant, reported everywhere; collisions and the setting are respected.
func TestPersonalProject_Provisioning(t *testing.T) {
	f := newAdminFixture(t) // settings PUT needs the config path
	f.wirePersonalProjects()
	f.seedNote(t, "taken/x.md", "x")

	rec := f.doAuthRecorder(http.MethodPost, "/api/v1/admin/users", `{"username":"nora","password":"nora-pass-1234"}`, nil)
	if rec.code != http.StatusCreated {
		t.Fatalf("create user = %d (%s)", rec.code, rec.body)
	}
	nora, _ := f.webauth.UserByID(jsonField(t, rec.body, "id"))
	if !f.router.projectExists("nora") || f.projects.Visibility("nora") != projects.VisibilityPrivate {
		t.Fatalf("personal project missing or not private: exists=%v vis=%s", f.router.projectExists("nora"), f.projects.Visibility("nora"))
	}
	if lvl, _ := f.projects.MemberLevel("nora", nora.ID); lvl != projects.LevelAdmin {
		t.Errorf("personal project grant = %q want admin", lvl)
	}
	if rec := f.doAuthRecorder(http.MethodGet, "/api/v1/admin/users", "", nil); !strings.Contains(rec.body, `"personal_project":"nora"`) {
		t.Errorf("users list must name the personal project: %s", rec.body)
	}
	bearer, _, _ := f.spaTokens.Create(nora.ID, "test")
	hdr := map[string]string{"Authorization": "Bearer " + bearer}
	if rec := f.request(http.MethodGet, "/api/v1/me/access", "", hdr); !strings.Contains(rec.Body.String(), `"personal_project":"nora"`) || !strings.Contains(rec.Body.String(), `"level":"admin"`) {
		t.Errorf("me/access must name the personal project: %s", rec.Body.String())
	}
	if rec := f.request(http.MethodPost, "/api/v1/notes", `{"path":"nora/hot.md","content":"# mine"}`, hdr); rec.Code != http.StatusCreated {
		t.Errorf("write in own personal project = %d (%s)", rec.Code, rec.Body.String())
	}

	// A username that collides with an existing folder: account created, no
	// personal project, and the owner can not create it either.
	rec = f.doAuthRecorder(http.MethodPost, "/api/v1/admin/users", `{"username":"taken","password":"taken-pass-1234"}`, nil)
	if rec.code != http.StatusCreated {
		t.Fatalf("create colliding user = %d (%s)", rec.code, rec.body)
	}
	taken, _ := f.webauth.UserByID(jsonField(t, rec.body, "id"))
	if _, ok := f.projects.MemberLevel("taken", taken.ID); ok {
		t.Error("colliding folder must not become the personal project")
	}
	if rec := f.doAuthRecorder(http.MethodPost, "/api/v1/admin/users/"+taken.ID+"/personal-project", "", nil); rec.code != http.StatusConflict {
		t.Errorf("manual provisioning on a taken name = %d want 409", rec.code)
	}

	// Guests get none; the setting switches it off; manual provisioning
	// works regardless of the setting and refuses a second one.
	if rec := f.doAuthRecorder(http.MethodPost, "/api/v1/admin/users", `{"username":"gwen","password":"gwen-pass-1234","role":"guest"}`, nil); rec.code != http.StatusCreated {
		t.Fatalf("create guest = %d", rec.code)
	}
	if f.router.projectExists("gwen") {
		t.Error("guests must not get a personal project")
	}
	if rec := f.doAuthRecorder(http.MethodPut, "/api/v1/settings", `{"personal_projects":false}`, nil); rec.code != http.StatusOK || !strings.Contains(rec.body, `"personal_projects":false`) {
		t.Fatalf("switch off = %d (%s)", rec.code, rec.body)
	}
	rec = f.doAuthRecorder(http.MethodPost, "/api/v1/admin/users", `{"username":"otto","password":"otto-pass-1234"}`, nil)
	if rec.code != http.StatusCreated {
		t.Fatalf("create otto = %d", rec.code)
	}
	otto, _ := f.webauth.UserByID(jsonField(t, rec.body, "id"))
	if f.router.projectExists("otto") {
		t.Error("setting off must skip the personal project")
	}
	if rec := f.doAuthRecorder(http.MethodPost, "/api/v1/admin/users/"+otto.ID+"/personal-project", "", nil); rec.code != http.StatusCreated || !strings.Contains(rec.body, `"personal_project":"otto"`) {
		t.Errorf("manual provisioning = %d (%s)", rec.code, rec.body)
	}
	if rec := f.doAuthRecorder(http.MethodPost, "/api/v1/admin/users/"+otto.ID+"/personal-project", "", nil); rec.code != http.StatusConflict {
		t.Errorf("second provisioning = %d want 409", rec.code)
	}
}

// The capability of creating projects can be withdrawn per account.
func TestCanCreateProjects_Capability(t *testing.T) {
	f := newNotesFixture(t)
	u, bearer := f.memberUser(t, "pat")
	hdr := map[string]string{"Authorization": "Bearer " + bearer}
	if rec := f.request(http.MethodPost, "/api/v1/projects", `{"name":"pat-one"}`, hdr); rec.Code != http.StatusCreated {
		t.Fatalf("create with capability = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := f.doAuthRecorder(http.MethodPatch, "/api/v1/admin/users/"+u.ID, `{"can_create_projects":false}`, nil); rec.code != http.StatusOK {
		t.Fatalf("withdraw = %d (%s)", rec.code, rec.body)
	}
	if rec := f.request(http.MethodPost, "/api/v1/projects", `{"name":"pat-two"}`, hdr); rec.Code != http.StatusForbidden {
		t.Errorf("create without capability = %d want 403", rec.Code)
	}
	if rec := f.request(http.MethodGet, "/api/v1/me/access", "", hdr); !strings.Contains(rec.Body.String(), `"can_create_projects":false`) {
		t.Errorf("me/access must report the capability: %s", rec.Body.String())
	}
}

// Self-service tokens: inherit records no project list, custom a validated
// subset; guests read only; only the owner's own tokens are visible to them.
func TestMeTokens(t *testing.T) {
	f := newAdminFixture(t) // wires the MCP token store
	f.seedNote(t, "Alpha/a.md", "x")
	f.seedNote(t, "Beta/b.md", "x")
	u, bearer := f.memberUser(t, "quinn")
	hdr := map[string]string{"Authorization": "Bearer " + bearer}
	if err := f.projects.SetMember("Alpha", u.ID, projects.LevelWrite); err != nil {
		t.Fatal(err)
	}

	rec := f.request(http.MethodPost, "/api/v1/me/tokens", `{"name":"laptop","scopes":["read","write"]}`, hdr)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"token":"`) || !strings.Contains(rec.Body.String(), `"owner_user_id":"`+u.ID+`"`) {
		t.Fatalf("inherit token = %d (%s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"projects":[`) {
		t.Errorf("inherit must record no project list: %s", rec.Body.String())
	}
	inheritID := jsonField(t, rec.Body.String()[strings.Index(rec.Body.String(), `"record"`):], "id")

	if rec := f.request(http.MethodPost, "/api/v1/me/tokens", `{"name":"bad","mode":"custom","projects":["Beta"]}`, hdr); rec.Code != http.StatusForbidden {
		t.Errorf("custom with an invisible project = %d want 403", rec.Code)
	}
	if rec := f.request(http.MethodPost, "/api/v1/me/tokens", `{"name":"empty","mode":"custom"}`, hdr); rec.Code != http.StatusBadRequest {
		t.Errorf("custom without projects = %d want 400", rec.Code)
	}
	if rec := f.request(http.MethodPost, "/api/v1/me/tokens", `{"name":"alpha-only","mode":"custom","projects":["Alpha"],"tool_profile":"core"}`, hdr); rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"projects":["Alpha"]`) || !strings.Contains(rec.Body.String(), `"tool_profile":"core"`) {
		t.Errorf("custom token = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := f.request(http.MethodPost, "/api/v1/me/tokens", `{"name":"x","mode":"weird"}`, hdr); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown mode = %d want 400", rec.Code)
	}

	// Listing: quinn sees two, the owner's admin page sees them too, another
	// account sees none of quinn's.
	if rec := f.request(http.MethodGet, "/api/v1/me/tokens", "", hdr); !strings.Contains(rec.Body.String(), `"total":2`) {
		t.Errorf("own list: %s", rec.Body.String())
	}
	if rec := f.doAuthRecorder(http.MethodGet, "/api/v1/admin/tokens", "", nil); !strings.Contains(rec.body, `"name":"laptop"`) {
		t.Errorf("owner must see self-service tokens: %s", rec.body)
	}
	_, otherBearer := f.memberUser(t, "ron")
	otherHdr := map[string]string{"Authorization": "Bearer " + otherBearer}
	if rec := f.request(http.MethodGet, "/api/v1/me/tokens", "", otherHdr); !strings.Contains(rec.Body.String(), `"total":0`) {
		t.Errorf("other account must see no token of quinn: %s", rec.Body.String())
	}
	if rec := f.request(http.MethodDelete, "/api/v1/me/tokens/"+inheritID, "", otherHdr); rec.Code != http.StatusNotFound {
		t.Errorf("revoking someone else's token = %d want 404", rec.Code)
	}
	if rec := f.request(http.MethodDelete, "/api/v1/me/tokens/"+inheritID, "", hdr); rec.Code != http.StatusNoContent {
		t.Errorf("revoking own token = %d want 204", rec.Code)
	}
	if rec := f.request(http.MethodGet, "/api/v1/me/tokens", "", hdr); !strings.Contains(rec.Body.String(), `"total":1`) {
		t.Errorf("after revoke: %s", rec.Body.String())
	}

	// Guests mint read tokens only.
	g, err := f.webauth.AddUser("gale", "gale-pass-1234", webauth.RoleGuest)
	if err != nil {
		t.Fatal(err)
	}
	gb, _, _ := f.spaTokens.Create(g.ID, "test")
	ghdr := map[string]string{"Authorization": "Bearer " + gb}
	if rec := f.request(http.MethodPost, "/api/v1/me/tokens", `{"name":"g","scopes":["write"]}`, ghdr); rec.Code != http.StatusForbidden {
		t.Errorf("guest write token = %d want 403", rec.Code)
	}
	if rec := f.request(http.MethodPost, "/api/v1/me/tokens", `{"name":"g"}`, ghdr); rec.Code != http.StatusCreated {
		t.Errorf("guest read token = %d (%s)", rec.Code, rec.Body.String())
	}
}
