package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/fleet"
	"github.com/gosidian/gosidian/internal/jobs"
	"github.com/gosidian/gosidian/internal/plugins"
	"github.com/gosidian/gosidian/internal/projects"
)

// The office's map reads a team of agents through GET
// /api/v1/fleet/teams/<path> (IMP-233): the team as the server reads it,
// its problems on the note's lines, and the state of each role from its
// tasks.

const officeTeamNote = `---
title: La squadra
type: team
---

# La squadra

` + "```team" + `
coordinator: capo
budget: {day_usd: 10, task_usd: 2}
max_agents: 2
roles:
  capo:
    agent: "[[Alpha/agents/capo]]"
    run: claude.run
    model: sonnet
    delegates_to: [dev, rev]
  dev:
    agent: "[[Alpha/agents/dev]]"
    run: pi.run
    writes: [Alpha/docs/]
  rev:
    run: pi.run
    review: true
  doc:
    run: pi.run
` + "```\n"

func officeTaskNote(title, role, status string) string {
	return "---\ntitle: " + title + "\ntype: task\nstatus: " + status + "\nteam: \"[[Alpha/squadra]]\"\nassignee: " + role + "\n---\n\n# " + title + "\n"
}

func (f *notesFixture) createNote(t *testing.T, path, content string) {
	t.Helper()
	if w := f.doAuthRecorder(http.MethodPost, "/api/v1/notes", mustJSON(t, map[string]string{"path": path, "content": content}), nil); w.code != http.StatusCreated {
		t.Fatalf("create %s: %d %s", path, w.code, w.body)
	}
}

func seedOffice(t *testing.T) *notesFixture {
	t.Helper()
	f := newNotesFixture(t)
	f.createNote(t, "Alpha/squadra.md", officeTeamNote)
	f.createNote(t, "Alpha/agents/capo.md", "---\ntitle: Il capo\n---\n\n# Il capo\n\nYou coordinate.\n")
	f.createNote(t, "Alpha/tasks/a.md", officeTaskNote("La pagina", "dev", fleet.Working))
	f.createNote(t, "Alpha/tasks/b.md", officeTaskNote("La revisione", "rev", fleet.Review))
	f.createNote(t, "Alpha/tasks/c.md", officeTaskNote("L'obiettivo", "capo", fleet.Submitted))
	f.createNote(t, "Alpha/tasks/d.md", officeTaskNote("Fatto", "dev", fleet.Completed))
	f.createNote(t, "Alpha/tasks/e.md", strings.Replace(officeTaskNote("Di un'altra", "dev", fleet.Working), "Alpha/squadra", "Alpha/altra", 1))
	return f
}

func readOffice(t *testing.T, body string) (officeTeam, map[string]json.RawMessage) {
	t.Helper()
	var out officeTeam
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	_ = json.Unmarshal([]byte(body), &raw)
	return out, raw
}

func TestFleetTeam_TheTeamItsRolesAndTheirStates(t *testing.T) {
	f := seedOffice(t)
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/fleet/teams/Alpha/squadra.md", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("%d %s", w.code, w.body)
	}
	out, raw := readOffice(t, w.body)
	if out.Team == nil || len(out.Team.Roles) != 4 || out.Team.Coordinator != "capo" || out.Title != "La squadra" {
		t.Fatalf("team %+v title %q", out.Team, out.Title)
	}
	if out.Line != 8 || len(out.Problems) != 0 {
		t.Errorf("line %d problems %+v: the block's fence is line 8 and it reads", out.Line, out.Problems)
	}
	if out.Pipelines || raw["spent_today"] != nil {
		t.Errorf("pipelines off: no spend (%s)", raw["spent_today"])
	}
	for role, want := range map[string]string{"dev": roleWorking, "rev": roleWaiting, "capo": roleQueued, "doc": roleIdle} {
		if got := out.Roles[role]; got == nil || got.State != want {
			t.Errorf("role %s: %+v, want %s", role, got, want)
		}
	}
	dev := out.Roles["dev"]
	if len(dev.Tasks) != 1 || dev.Tasks[0].Path != "Alpha/tasks/a.md" || dev.Tasks[0].Title != "La pagina" {
		t.Errorf("dev's open tasks: %+v (not the completed one, not another team's)", dev.Tasks)
	}
	if a := out.Roles["capo"].Agent; a == nil || a.Path != "Alpha/agents/capo.md" || a.Title != "Il capo" || a.Missing {
		t.Errorf("capo's agent: %+v", a)
	}
	if a := dev.Agent; a == nil || !a.Missing {
		t.Errorf("dev's agent note is missing: %+v", a)
	}
	if out.Roles["rev"].Agent != nil {
		t.Errorf("rev has no agent note: %+v", out.Roles["rev"].Agent)
	}
	if out.Roles["doc"].Tasks == nil {
		t.Error("an idle role lists no task, as [] not null")
	}
}

// Each role lists the guides and skills its agent receives (IMP-233), with
// their titles, and says which is missing or no skill.
func TestFleetTeam_TheGuidesAndSkillsOfARole(t *testing.T) {
	f := newNotesFixture(t)
	team := strings.Replace(officeTeamNote, "max_agents: 2\n", "max_agents: 2\nguide: regole\n", 1)
	team = strings.Replace(team, "    writes: [Alpha/docs/]\n", "    writes: [Alpha/docs/]\n    guide: guide/codice\n    skills: [skills/test, skills/piana, skills/nessuna]\n", 1)
	f.createNote(t, "Alpha/squadra.md", team)
	f.createNote(t, "Alpha/regole.md", "---\ntitle: Le regole\n---\n\nNever invent.\n")
	f.createNote(t, "Alpha/skills/test.md", "---\ntitle: I test\ntype: skill\n---\n\nRun them.\n")
	f.createNote(t, "Alpha/skills/piana.md", "---\ntitle: Piana\ntype: note\n---\n\nNo skill.\n")
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/fleet/teams/Alpha/squadra.md", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("%d %s", w.code, w.body)
	}
	out, _ := readOffice(t, w.body)
	var got []string
	for _, n := range out.Roles["dev"].Notes {
		got = append(got, fmt.Sprintf("%s %s %q missing=%v not_skill=%v", n.Kind, n.Path, n.Title, n.Missing, n.NotSkill))
	}
	want := []string{
		`team_guide Alpha/regole.md "Le regole" missing=false not_skill=false`,
		`guide Alpha/guide/codice.md "codice" missing=true not_skill=false`,
		`skill Alpha/skills/test.md "I test" missing=false not_skill=false`,
		`skill Alpha/skills/piana.md "Piana" missing=false not_skill=true`,
		`skill Alpha/skills/nessuna.md "nessuna" missing=true not_skill=false`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("dev's notes:\n%s", strings.Join(got, "\n"))
	}
	if n := out.Roles["doc"].Notes; len(n) != 1 || n[0].Kind != "team_guide" {
		t.Errorf("doc gets the team's guide: %+v", n)
	}

	// A role with a profile (IMP-233): the profile first, then what it
	// gives, and the role reads with its fields.
	f.createNote(t, "Alpha/profiles/analista.md", "---\ntitle: L'analista\ntype: profile\n---\n\n```profile\nrun: claude.run\nskills: [skills/test]\n```\n")
	team = strings.Replace(team, "  doc:\n    run: pi.run\n", "  doc:\n    profile: analista\n", 1)
	if w := f.doAuthRecorder(http.MethodPut, "/api/v1/notes/Alpha/squadra.md", mustJSON(t, map[string]string{"content": team}), nil); w.code != http.StatusOK {
		t.Fatalf("update: %d %s", w.code, w.body)
	}
	w = f.doAuthRecorder(http.MethodGet, "/api/v1/fleet/teams/Alpha/squadra.md", "", nil)
	out, _ = readOffice(t, w.body)
	got = nil
	for _, n := range out.Roles["doc"].Notes {
		got = append(got, fmt.Sprintf("%s %s %q", n.Kind, n.Path, n.Title))
	}
	if strings.Join(got, ", ") != `profile Alpha/profiles/analista.md "L'analista", team_guide Alpha/regole.md "Le regole", skill Alpha/skills/test.md "I test"` {
		t.Errorf("doc's notes: %s (problems %v)", strings.Join(got, ", "), out.Problems)
	}
	if doc, _ := out.Team.Role("doc"); doc.Run != "claude.run" || doc.Profile != "Alpha/profiles/analista.md" {
		t.Errorf("doc: %+v", doc)
	}
}

// The tools a role names whose plugin is not exposed in the project are
// said (IMP-233): its agent could not call them.
func TestFleetTeam_TheToolsOfARoleNotExposed(t *testing.T) {
	f := newNotesFixture(t)
	var calls atomic.Int32
	ts := mapPlugin(t, &calls)
	reg, err := plugins.New([]plugins.Config{{Name: "vec", URL: ts.URL + "/mcp"}}, t.TempDir(), nil, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	reg.Check(ctx)
	st, _ := reg.State("vec")
	if _, err := reg.Accept(ctx, "vec", st.SHA, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Expose("vec", "Alpha", "test"); err != nil {
		t.Fatal(err)
	}
	f.router.deps.Plugins = reg
	team := strings.Replace(officeTeamNote, "    writes: [Alpha/docs/]\n", "    writes: [Alpha/docs/]\n    tools: [vec, vec_search, layout_render, vec-qdrant]\n", 1)
	team = strings.Replace(team, "  doc:\n    run: pi.run\n", "  doc:\n    run: pi.run\n    tools: []\n", 1)
	f.createNote(t, "Alpha/squadra.md", team)
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/fleet/teams/Alpha/squadra.md", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("%d %s", w.code, w.body)
	}
	out, _ := readOffice(t, w.body)
	if got := strings.Join(out.Roles["dev"].Unexposed, " "); got != "layout_render vec-qdrant" {
		t.Errorf("dev's unexposed tools: %q", got)
	}
	if out.Roles["doc"].Unexposed != nil || out.Roles["capo"].Unexposed != nil {
		t.Errorf("doc %v, capo %v", out.Roles["doc"].Unexposed, out.Roles["capo"].Unexposed)
	}
	dev, _ := out.Team.Role("dev")
	doc, _ := out.Team.Role("doc")
	capo, _ := out.Team.Role("capo")
	if len(dev.Tools) != 4 || doc.Tools == nil || len(doc.Tools) != 0 || capo.Tools != nil {
		t.Errorf("tools: dev %#v, doc %#v, capo %#v", dev.Tools, doc.Tools, capo.Tools)
	}
	if !strings.Contains(w.body, `"tools":[]`) || !strings.Contains(w.body, `"tools":null`) {
		t.Errorf("none and all are not told apart: %s", w.body)
	}
}

// A team's rooms come with it, each as the map draws a team; a room whose
// note is missing has no view (IMP-233).
func TestFleetTeam_TheRoomsOfATeam(t *testing.T) {
	f := newNotesFixture(t)
	team := strings.Replace(officeTeamNote, "max_agents: 2\n", "max_agents: 2\nrooms:\n  front: front\n  manca: manca\n", 1)
	team = strings.Replace(team, "delegates_to: [dev, rev]", "delegates_to: [dev, rev, front, manca]", 1)
	f.createNote(t, "Alpha/squadra.md", team)
	f.createNote(t, "Alpha/front.md", "---\ntitle: Il front\ntype: team\n---\n\n```team\ncoordinator: lead\nroles:\n  lead: {run: pi.run, delegates_to: [ui]}\n  ui: {run: pi.run}\n```\n")
	f.createNote(t, "Alpha/tasks/r.md", strings.Replace(officeTaskNote("Il bottone", "ui", fleet.Working), "Alpha/squadra", "Alpha/front", 1))
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/fleet/teams/Alpha/squadra.md", "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("%d %s", w.code, w.body)
	}
	out, _ := readOffice(t, w.body)
	if len(out.Rooms) != 2 || out.Rooms[0].Name != "front" || out.Rooms[0].Path != "Alpha/front.md" || out.Rooms[1].View != nil {
		t.Fatalf("rooms: %+v", out.Rooms)
	}
	v := out.Rooms[0].View
	if v == nil || v.Title != "Il front" || v.Team == nil || v.Team.Coordinator != "lead" || v.Roles["ui"] == nil || v.Roles["ui"].State != roleWorking {
		t.Fatalf("front's view: %+v", v)
	}
	if len(out.Problems) != 0 {
		t.Errorf("problems: %v", out.Problems)
	}
}

func TestFleetTeam_ProblemsOnTheNotesLines(t *testing.T) {
	f := newNotesFixture(t)
	f.createNote(t, "Alpha/rotta.md", strings.Replace(officeTeamNote, "run: claude.run", "run: claude", 1))
	f.createNote(t, "Alpha/vuota.md", "---\ntype: team\n---\n\n# Nessun blocco\n")
	f.createNote(t, "Alpha/nota.md", "# Una nota\n\n```team\nroles:\n  a: {run: pi.run}\n```\n")

	out, _ := readOffice(t, f.doAuthRecorder(http.MethodGet, "/api/v1/fleet/teams/Alpha/rotta.md", "", nil).body)
	if len(out.Problems) == 0 || out.Problems[0].Line <= out.Line || !strings.Contains(out.Problems[0].Msg, "run") {
		t.Errorf("a bad run: problems %+v at the note's lines (block at %d)", out.Problems, out.Line)
	}
	out, raw := readOffice(t, f.doAuthRecorder(http.MethodGet, "/api/v1/fleet/teams/Alpha/vuota.md", "", nil).body)
	if string(raw["team"]) != "null" || out.Line != 0 || len(out.Problems) != 1 || !strings.Contains(out.Problems[0].Msg, "no ```team block") {
		t.Errorf("no block: team %s line %d problems %+v", raw["team"], out.Line, out.Problems)
	}
	out, _ = readOffice(t, f.doAuthRecorder(http.MethodGet, "/api/v1/fleet/teams/Alpha/nota.md", "", nil).body)
	if out.Team == nil || len(out.Problems) != 1 || !strings.Contains(out.Problems[0].Msg, "not type: team") {
		t.Errorf("a block in a note of another type: team %+v problems %+v", out.Team, out.Problems)
	}
}

func TestFleetTeam_WhoMayReadIt(t *testing.T) {
	f := seedOffice(t)
	for path, code := range map[string]int{
		"Alpha/squadra":    http.StatusBadRequest,
		"Alpha/nessuna.md": http.StatusNotFound,
	} {
		if w := f.doAuthRecorder(http.MethodGet, "/api/v1/fleet/teams/"+path, "", nil); w.code != code {
			t.Errorf("%s: %d, want %d (%s)", path, w.code, code, w.body)
		}
	}
	// The mux cleans a path that climbs out before the handler sees it.
	if w := f.doAuthRecorder(http.MethodGet, "/api/v1/fleet/teams/../fuori/squadra.md", "", nil); w.code == http.StatusOK {
		t.Errorf("a path out of the vault: %d %s", w.code, w.body)
	}
	if w := f.doAuthRecorder(http.MethodPut, "/api/v1/fleet/teams/Alpha/squadra.md", "{}", nil); w.code != http.StatusMethodNotAllowed {
		t.Errorf("PUT: %d, the map reads and checks a draft (POST)", w.code)
	}
	_, member := f.memberUser(t, "bruno")
	if w := f.req(t, http.MethodGet, "/api/v1/fleet/teams/Alpha/squadra.md", "", member); w.code != http.StatusNotFound {
		t.Errorf("a member without access: %d, want 404", w.code)
	}
	f.setVisibility(t, "Alpha", projects.VisibilityInternal)
	if w := f.req(t, http.MethodGet, "/api/v1/fleet/teams/Alpha/squadra.md", "", member); w.code != http.StatusOK {
		t.Errorf("a member who reads the project: %d %s", w.code, w.body)
	}
	if w := f.request(http.MethodGet, "/api/v1/fleet/teams/Alpha/squadra.md", "", nil); w.Code != http.StatusUnauthorized {
		t.Errorf("no session: %d", w.Code)
	}
}

type fakeOfficeEngine struct {
	waits  map[string]string
	team   jobs.Spend
	byTask map[string]jobs.Spend
}

func (e fakeOfficeEngine) TaskWaits(string) map[string]string { return e.waits }
func (e fakeOfficeEngine) TeamSpend(string, string) (jobs.Spend, error) {
	return e.team, nil
}
func (e fakeOfficeEngine) TeamSpendByTask(string, string) (map[string]jobs.Spend, error) {
	return e.byTask, nil
}
func (e fakeOfficeEngine) Now() time.Time { return time.Date(2026, 11, 18, 16, 0, 0, 0, time.UTC) }

// With the pipelines on, a task that waits holds its role, and each role
// sums today's spend of its tasks, closed ones too.
func TestFleetTeam_WaitsAndSpendFromTheEngine(t *testing.T) {
	f := seedOffice(t)
	team, problems := fleet.Parse(fleet.Find([]byte(officeTeamNote))[0].Src, "Alpha/squadra.md")
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	out := officeTeam{Pipelines: true, Roles: map[string]*officeRole{}}
	eng := fakeOfficeEngine{
		waits: map[string]string{"Alpha/tasks/c.md": "the team's budget of the day is spent"},
		team:  jobs.Spend{CostUSD: 1.25, Tokens: 900, Steps: 4},
		byTask: map[string]jobs.Spend{
			"Alpha/tasks/a.md": {CostUSD: 0.5, Tokens: 300, Steps: 1},
			"Alpha/tasks/d.md": {CostUSD: 0.25, Tokens: 200, Steps: 1},
			"Alpha/tasks/b.md": {CostUSD: 0.5, Tokens: 400, Steps: 2},
			"":                 {CostUSD: 9, Tokens: 9, Steps: 9},
		},
	}
	f.router.officeState(&out, team, eng, f.router.deps.Vault.Load)
	if out.SpentToday == nil || *out.SpentToday != eng.team {
		t.Errorf("the team's spend: %+v", out.SpentToday)
	}
	capo := out.Roles["capo"]
	if capo.State != roleHeld || len(capo.Tasks) != 1 || capo.Tasks[0].Waits != "the team's budget of the day is spent" {
		t.Errorf("capo, its objective waiting for the budget: %+v", capo)
	}
	if sp := out.Roles["dev"].SpentToday; sp == nil || *sp != (jobs.Spend{CostUSD: 0.75, Tokens: 500, Steps: 2}) {
		t.Errorf("dev's spend, its open and its closed task: %+v", sp)
	}
	if sp := out.Roles["doc"].SpentToday; sp == nil || *sp != (jobs.Spend{}) {
		t.Errorf("an idle role spent nothing, and says so: %+v", sp)
	}
}
