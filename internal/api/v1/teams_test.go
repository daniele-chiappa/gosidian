package v1

import (
	"net/http"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/projects"
)

// Owner-only team administration: create, roster, grants, delete; and a
// team grant opens a project to its users like a direct grant would.
func TestAdminTeams_CRUDAndEffect(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "Alpha/a.md", "x")
	m1, bearer := f.memberUser(t, "erin")
	hdr := map[string]string{"Authorization": "Bearer " + bearer}

	// Members cannot administer teams.
	if rec := f.request(http.MethodGet, "/api/v1/admin/teams", "", hdr); rec.Code != http.StatusForbidden {
		t.Errorf("teams as member = %d want 403", rec.Code)
	}

	rec := f.doAuthRecorder(http.MethodPost, "/api/v1/admin/teams", `{"name":"devs","description":"backend"}`, nil)
	if rec.code != http.StatusCreated || !strings.Contains(rec.body, `"name":"devs"`) {
		t.Fatalf("create team = %d (%s)", rec.code, rec.body)
	}
	id := jsonField(t, rec.body, "id")
	if rec := f.doAuthRecorder(http.MethodPost, "/api/v1/admin/teams", `{"name":"Devs"}`, nil); rec.code != http.StatusConflict {
		t.Errorf("duplicate team = %d want 409", rec.code)
	}
	if rec := f.doAuthRecorder(http.MethodPost, "/api/v1/admin/teams", `{"name":""}`, nil); rec.code != http.StatusBadRequest {
		t.Errorf("empty name = %d want 400", rec.code)
	}

	// Roster: owner refused, unknown 404, member added.
	if rec := f.doAuthRecorder(http.MethodPut, "/api/v1/admin/teams/"+id+"/users", `{"user_id":"`+f.owner.ID+`"}`, nil); rec.code != http.StatusBadRequest {
		t.Errorf("owner in team = %d want 400", rec.code)
	}
	if rec := f.doAuthRecorder(http.MethodPut, "/api/v1/admin/teams/"+id+"/users", `{"user_id":"ghost"}`, nil); rec.code != http.StatusNotFound {
		t.Errorf("unknown user = %d want 404", rec.code)
	}
	if rec := f.doAuthRecorder(http.MethodPut, "/api/v1/admin/teams/"+id+"/users", `{"user_id":"`+m1.ID+`"}`, nil); rec.code != http.StatusCreated || !strings.Contains(rec.body, `"username":"erin"`) {
		t.Fatalf("add user = %d (%s)", rec.code, rec.body)
	}

	// Before any grant the member sees nothing (Alpha is private).
	if rec := f.request(http.MethodGet, "/api/v1/notes/Alpha/a.md", "", hdr); rec.Code != http.StatusNotFound {
		t.Errorf("before grant = %d want 404", rec.Code)
	}
	if rec := f.doAuthRecorder(http.MethodPut, "/api/v1/admin/teams/"+id+"/grants", `{"project":"Nope","level":"read"}`, nil); rec.code != http.StatusNotFound {
		t.Errorf("grant on unknown project = %d want 404", rec.code)
	}
	if rec := f.doAuthRecorder(http.MethodPut, "/api/v1/admin/teams/"+id+"/grants", `{"project":"Alpha","level":"write"}`, nil); rec.code != http.StatusCreated || !strings.Contains(rec.body, `"project":"Alpha"`) {
		t.Fatalf("team grant = %d (%s)", rec.code, rec.body)
	}
	// The team grant makes erin a writer of Alpha, reported as such.
	if rec := f.request(http.MethodPut, "/api/v1/notes/Alpha/a.md", `{"content":"via team"}`, hdr); rec.Code != http.StatusOK {
		t.Errorf("write via team = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := f.request(http.MethodGet, "/api/v1/me/access", "", hdr); !strings.Contains(rec.Body.String(), `"via":["team:devs:write"]`) {
		t.Errorf("me/access must name the team: %s", rec.Body.String())
	}
	if rec := f.doAuthRecorder(http.MethodGet, "/api/v1/projects/Alpha", "", nil); !strings.Contains(rec.body, `"teams_count":1`) {
		t.Errorf("teams_count: %s", rec.body)
	}
	// A direct read grant plus the team write grant: the highest wins.
	if err := f.projects.SetMember("Alpha", m1.ID, projects.LevelRead); err != nil {
		t.Fatal(err)
	}
	if rec := f.request(http.MethodGet, "/api/v1/projects/Alpha", "", hdr); !strings.Contains(rec.Body.String(), `"access":"write"`) {
		t.Errorf("highest source must win: %s", rec.Body.String())
	}

	// Rename the team; the via label follows.
	if rec := f.doAuthRecorder(http.MethodPatch, "/api/v1/admin/teams/"+id, `{"name":"backend"}`, nil); rec.code != http.StatusOK {
		t.Fatalf("rename team = %d (%s)", rec.code, rec.body)
	}
	if rec := f.request(http.MethodGet, "/api/v1/me/access", "", hdr); !strings.Contains(rec.Body.String(), `team:backend:write`) {
		t.Errorf("renamed team in via: %s", rec.Body.String())
	}

	// Remove the grant, then the user, then the team.
	if rec := f.doAuthRecorder(http.MethodDelete, "/api/v1/admin/teams/"+id+"/grants/Alpha", "", nil); rec.code != http.StatusNoContent {
		t.Errorf("remove grant = %d", rec.code)
	}
	if rec := f.request(http.MethodPut, "/api/v1/notes/Alpha/a.md", `{"content":"no more"}`, hdr); rec.Code != http.StatusForbidden {
		t.Errorf("after grant removal write = %d want 403", rec.Code)
	}
	if rec := f.doAuthRecorder(http.MethodDelete, "/api/v1/admin/teams/"+id+"/users/"+m1.ID, "", nil); rec.code != http.StatusNoContent {
		t.Errorf("remove user = %d", rec.code)
	}
	if rec := f.doAuthRecorder(http.MethodDelete, "/api/v1/admin/teams/"+id, "", nil); rec.code != http.StatusNoContent {
		t.Errorf("delete team = %d", rec.code)
	}
	if rec := f.doAuthRecorder(http.MethodGet, "/api/v1/admin/teams/"+id, "", nil); rec.code != http.StatusNotFound {
		t.Errorf("deleted team = %d want 404", rec.code)
	}
}

// Disabling an account strips it from every team.
func TestAdminTeams_DisableCascade(t *testing.T) {
	f := newNotesFixture(t)
	m1, _ := f.memberUser(t, "frank")
	team, err := f.projects.CreateTeam("ops", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.projects.AddTeamUser(team.ID, m1.ID); err != nil {
		t.Fatal(err)
	}
	if rec := f.doAuthRecorder(http.MethodDelete, "/api/v1/admin/users/"+m1.ID, "", nil); rec.code != http.StatusNoContent {
		t.Fatalf("disable = %d %s", rec.code, rec.body)
	}
	if got, _ := f.projects.Team(team.ID); len(got.Users) != 0 {
		t.Errorf("disabled user still in team: %+v", got.Users)
	}
}

// Delegation: a project admin manages the project's users and teams and
// sees the candidates; a writer sees the access view without candidates
// and cannot change it; strangers get 404.
func TestProjectAccess_Delegation(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "Shared/n.md", "x")
	admin, adminBearer := f.memberUser(t, "gina")
	writer, writerBearer := f.memberUser(t, "hal")
	stranger, strangerBearer := f.memberUser(t, "ivy")
	_ = stranger
	adminHdr := map[string]string{"Authorization": "Bearer " + adminBearer}
	writerHdr := map[string]string{"Authorization": "Bearer " + writerBearer}
	strangerHdr := map[string]string{"Authorization": "Bearer " + strangerBearer}
	if err := f.projects.SetMember("Shared", admin.ID, projects.LevelAdmin); err != nil {
		t.Fatal(err)
	}
	if err := f.projects.SetMember("Shared", writer.ID, projects.LevelWrite); err != nil {
		t.Fatal(err)
	}
	team, err := f.projects.CreateTeam("qa", "")
	if err != nil {
		t.Fatal(err)
	}

	// Stranger: the project does not exist for them.
	if rec := f.request(http.MethodGet, "/api/v1/projects/Shared/access", "", strangerHdr); rec.Code != http.StatusNotFound {
		t.Errorf("stranger access view = %d want 404", rec.Code)
	}
	if rec := f.request(http.MethodPut, "/api/v1/projects/Shared/members", `{"user_id":"`+stranger.ID+`","level":"admin"}`, strangerHdr); rec.Code != http.StatusNotFound {
		t.Errorf("stranger self-grant = %d want 404", rec.Code)
	}

	// Writer: sees who has access, no candidates, cannot change anything.
	rec := f.request(http.MethodGet, "/api/v1/projects/Shared/access", "", writerHdr)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"username":"gina"`) || strings.Contains(rec.Body.String(), `"candidates"`) || !strings.Contains(rec.Body.String(), `"can_admin":false`) {
		t.Errorf("writer access view = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := f.request(http.MethodPut, "/api/v1/projects/Shared/members", `{"user_id":"`+stranger.ID+`","level":"read"}`, writerHdr); rec.Code != http.StatusForbidden {
		t.Errorf("writer grants = %d want 403", rec.Code)
	}
	if rec := f.request(http.MethodPut, "/api/v1/projects/Shared/teams", `{"team_id":"`+team.ID+`","level":"read"}`, writerHdr); rec.Code != http.StatusForbidden {
		t.Errorf("writer team grant = %d want 403", rec.Code)
	}

	// Project admin: candidates listed (ivy and the qa team), can grant both.
	rec = f.request(http.MethodGet, "/api/v1/projects/Shared/access", "", adminHdr)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `"can_admin":true`) || !strings.Contains(body, `"username":"ivy"`) || !strings.Contains(body, `"name":"qa"`) {
		t.Fatalf("admin access view = %d (%s)", rec.Code, body)
	}
	if strings.Contains(body, `"username":"gina","role"`) {
		t.Errorf("already granted accounts must not be candidates: %s", body)
	}
	if rec := f.request(http.MethodPut, "/api/v1/projects/Shared/members", `{"user_id":"`+stranger.ID+`","level":"read"}`, adminHdr); rec.Code != http.StatusCreated {
		t.Errorf("admin grants user = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := f.request(http.MethodPut, "/api/v1/projects/Shared/teams", `{"team_id":"`+team.ID+`","level":"write"}`, adminHdr); rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"name":"qa"`) {
		t.Errorf("admin grants team = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := f.request(http.MethodPut, "/api/v1/projects/Shared/teams", `{"team_id":"nope","level":"write"}`, adminHdr); rec.Code != http.StatusNotFound {
		t.Errorf("unknown team = %d want 404", rec.Code)
	}
	rec = f.request(http.MethodGet, "/api/v1/projects/Shared/access", "", adminHdr)
	if !strings.Contains(rec.Body.String(), `"team_id":"`+team.ID+`"`) || !strings.Contains(rec.Body.String(), `"username":"ivy","level":"read"`) {
		t.Errorf("access view after grants: %s", rec.Body.String())
	}
	// ivy now reads Shared (via the direct grant), and the stranger view shows the team.
	if rec := f.request(http.MethodGet, "/api/v1/notes/Shared/n.md", "", strangerHdr); rec.Code != http.StatusOK {
		t.Errorf("ivy read after grant = %d", rec.Code)
	}
	// Still not the owner: public stays out of reach for the project admin.
	if rec := f.request(http.MethodPut, "/api/v1/projects/Shared", `{"visibility":"public"}`, adminHdr); rec.Code != http.StatusForbidden {
		t.Errorf("project admin making public = %d want 403", rec.Code)
	}
	// Remove the team grant and the user grant as project admin.
	if rec := f.request(http.MethodDelete, "/api/v1/projects/Shared/teams/"+team.ID, "", adminHdr); rec.Code != http.StatusNoContent {
		t.Errorf("admin removes team grant = %d", rec.Code)
	}
	if rec := f.request(http.MethodDelete, "/api/v1/projects/Shared/members/"+stranger.ID, "", adminHdr); rec.Code != http.StatusNoContent {
		t.Errorf("admin removes user grant = %d", rec.Code)
	}
	if rec := f.request(http.MethodGet, "/api/v1/notes/Shared/n.md", "", strangerHdr); rec.Code != http.StatusNotFound {
		t.Errorf("ivy after removal = %d want 404", rec.Code)
	}
}

// jsonField pulls a top-level string field out of a JSON body without a
// full decode, for ids minted by the server.
func jsonField(t *testing.T, body, field string) string {
	t.Helper()
	key := `"` + field + `":"`
	i := strings.Index(body, key)
	if i < 0 {
		t.Fatalf("field %q missing in %s", field, body)
	}
	rest := body[i+len(key):]
	return rest[:strings.IndexByte(rest, '"')]
}
