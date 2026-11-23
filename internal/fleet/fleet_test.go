package fleet

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/frontmatter"
	"github.com/gosidian/gosidian/internal/parser"
)

const teamSrc = `coordinator: coordinatore
split: context
budget: {day_usd: 20, task_usd: 3}
max_agents: 3
alert: ["#lab/agenti"]
approvers: [daniele]
roles:
  coordinatore:
    agent: "[[fitness/agents/coordinatore]]"
    run: claude.run
    model: sonnet
    delegates_to: [conoscenza, sviluppo]
  sviluppo:
    agent: "[[fitness/agents/sviluppo]]"
    run: claude.run
    repo: agents-fitness
    access: exec
    paths: [web/, src/]
    forbid: [package.json]
    checks: npm test
  conoscenza:
    run: pi.run
    max_tokens: 5000000
    writes: [fitness/conoscenza]
`

func TestParseTeam(t *testing.T) {
	team, problems := Parse(teamSrc, "fitness/squadra.md")
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	if team.Coordinator != "coordinatore" || team.Budget.DayUSD != 20 || team.Budget.TaskUSD != 3 || team.MaxAgents != 3 || team.MaxRounds != 5 {
		t.Fatalf("team: %+v", team)
	}
	if got := []string{team.Roles[0].Name, team.Roles[1].Name, team.Roles[2].Name}; strings.Join(got, ",") != "coordinatore,sviluppo,conoscenza" {
		t.Fatalf("roles out of order: %v", got)
	}
	dev, _ := team.Role("sviluppo")
	if dev.Approve != ApproveMerge || dev.Agent != "fitness/agents/sviluppo.md" || len(dev.Paths) != 2 || dev.Access != AccessExec {
		t.Fatalf("sviluppo: %+v", dev)
	}
	know, _ := team.Role("conoscenza")
	if know.Approve != ApproveNone || len(know.Writes) != 1 || know.Writes[0] != "fitness/conoscenza/" || know.MaxTokens != 5_000_000 || know.Access != "" {
		t.Fatalf("conoscenza: %+v", know)
	}
	if team.Name() != "fitness/squadra" || team.Project() != "fitness" {
		t.Fatalf("name %q project %q", team.Name(), team.Project())
	}
}

func TestParseTeamProblems(t *testing.T) {
	cases := map[string]string{
		"unknown field":        "roles:\n  a:\n    run: claude.run\n    colour: red\n",
		"unknown top field":    "colour: red\nroles:\n  a: {run: claude.run}\n",
		"bad run":              "roles:\n  a: {run: claude}\n",
		"no roles":             "split: context\n",
		"bad coordinator":      "coordinator: b\nroles:\n  a: {run: claude.run}\n",
		"two levels":           "roles:\n  a: {run: claude.run, delegates_to: [b]}\n  b: {run: claude.run, delegates_to: [c]}\n  c: {run: claude.run}\n",
		"delegates to unknown": "roles:\n  a: {run: claude.run, delegates_to: [z]}\n",
		"writes outside":       "roles:\n  a: {run: claude.run, writes: [other/x]}\n",
		"writes dotdot":        "roles:\n  a: {run: claude.run, writes: [fitness/../x]}\n",
		"path dotdot":          "roles:\n  a: {run: claude.run, paths: [../etc]}\n",
		"repo and host":        "roles:\n  a: {run: claude.run, repo: r, host: h}\n",
		"bad approve":          "roles:\n  a: {run: claude.run, approve: maybe}\n",
		"bad split":            "split: mixed\nroles:\n  a: {run: claude.run}\n",
		"negative budget":      "budget: {day_usd: -1}\nroles:\n  a: {run: claude.run}\n",
		"bad role name":        "roles:\n  A: {run: claude.run}\n",
		"yaml":                 "roles: [\n",
		"rounds out of range":  "max_rounds: 99\nroles:\n  a: {run: claude.run}\n",
		"delegates to itself":  "roles:\n  a: {run: claude.run, delegates_to: [a]}\n",
		"negative max_tokens":  "roles:\n  a: {run: claude.run, max_tokens: -1}\n",
		"bad access":           "roles:\n  a: {run: claude.run, access: root}\n",
		"reader with paths":    "roles:\n  a: {run: claude.run, repo: r, access: read, paths: [web/]}\n",
		"reader with forbid":   "roles:\n  a: {run: claude.run, repo: r, access: read, forbid: [package.json]}\n",
		"reader with checks":   "roles:\n  a: {run: claude.run, repo: r, access: read, checks: npm test}\n",
		"reader merges":        "alert: [\"#a\"]\nroles:\n  a: {run: claude.run, repo: r, access: read, approve: merge}\n",
		"guide dotdot":         "guide: \"[[fitness/../other/rules]]\"\nroles:\n  a: {run: claude.run}\n",
		"role guide dotdot":    "roles:\n  a: {run: claude.run, guide: \"../x\"}\n",
		"skill not a note":     "roles:\n  a: {run: claude.run, skills: [fitness/skills/x.png]}\n",
		"skill twice":          "roles:\n  a: {run: claude.run, skills: [\"[[fitness/skills/x]]\", skills/x]}\n",
		"too many skills":      "roles:\n  a: {run: claude.run, skills: [s1, s2, s3, s4, s5, s6, s7, s8, s9, s10, s11, s12, s13, s14, s15, s16, s17, s18, s19, s20, s21]}\n",
	}
	for name, src := range cases {
		if _, problems := Parse(src, "fitness/squadra.md"); len(problems) == 0 {
			t.Errorf("%s: no problem", name)
		}
	}
}

// A role that reads its repository (a reviewer of an app's files) commits
// nothing: no pull request, so nobody approves it by default.
func TestParseTeamReader(t *testing.T) {
	team, problems := Parse("alert: [\"#lab\"]\nroles:\n  revisore: {run: pi.run, repo: agents-fitness, access: read, review: true}\n  sviluppo: {run: pi.run, repo: agents-fitness}\n", "fitness/squadra.md")
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	rev, _ := team.Role("revisore")
	if rev.Approve != ApproveNone || rev.Commits() || rev.Repo != "agents-fitness" {
		t.Fatalf("revisore: %+v, commits %v", rev, rev.Commits())
	}
	dev, _ := team.Role("sviluppo")
	if dev.Approve != ApproveMerge || !dev.Commits() {
		t.Fatalf("sviluppo: %+v, commits %v", dev, dev.Commits())
	}
	if (Role{Access: AccessRead}).Commits() || (Role{}).Commits() {
		t.Fatal("a role without a repo commits")
	}
}

// The guides and the skills of a role (IMP-233) are notes of the team's
// project: a link without the project is inside it. Its agent receives the
// team's guide, its own, then its skills, each once.
func TestParseTeamGuidesAndSkills(t *testing.T) {
	src := "guide: \"[[fitness/regole]]\"\nroles:\n" +
		"  sviluppo: {run: pi.run, guide: \"[[fitness/guide/codice|il codice]]\", skills: [\"[[fitness/skills/test]]\", skills/build.md, regole]}\n" +
		"  revisore: {run: pi.run, guide: regole}\n"
	team, problems := Parse(src, "fitness/squadra.md")
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	dev, _ := team.Role("sviluppo")
	if team.Guide != "fitness/regole.md" || dev.Guide != "fitness/guide/codice.md" ||
		strings.Join(dev.Skills, " ") != "fitness/skills/test.md fitness/skills/build.md fitness/regole.md" {
		t.Fatalf("team guide %q, sviluppo %+v", team.Guide, dev)
	}
	got := func(r string) string {
		ro, _ := team.Role(r)
		var out []string
		for _, n := range team.PromptNotes(ro) {
			k := "guide"
			if n.Skill {
				k = "skill"
			}
			out = append(out, k+" "+n.Path)
		}
		return strings.Join(out, ", ")
	}
	if g := got("sviluppo"); g != "guide fitness/regole.md, guide fitness/guide/codice.md, skill fitness/skills/test.md, skill fitness/skills/build.md" {
		t.Fatalf("sviluppo's notes: %s", g)
	}
	if g := got("revisore"); g != "guide fitness/regole.md" {
		t.Fatalf("revisore's notes: %s", g)
	}
	if _, problems := Parse("roles:\n  a: {run: claude.run, colour: red}\n", "fitness/squadra.md"); len(problems) != 1 || !strings.Contains(problems[0].Msg, "guide, skills") {
		t.Fatalf("an unknown field: %v", problems)
	}
}

// A role's tools (IMP-233): absent, every plugin of the project (nil);
// [] none; else the plugins and tools it names.
func TestParseTeamTools(t *testing.T) {
	team, problems := Parse("roles:\n  a: {run: pi.run}\n  b: {run: pi.run, tools: []}\n  c: {run: pi.run, tools: [vec-pg, layout_render]}\n", "fitness/squadra.md")
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	a, _ := team.Role("a")
	b, _ := team.Role("b")
	c, _ := team.Role("c")
	if a.Tools != nil || b.Tools == nil || len(b.Tools) != 0 || strings.Join(c.Tools, " ") != "vec-pg layout_render" {
		t.Fatalf("a %#v, b %#v, c %#v", a.Tools, b.Tools, c.Tools)
	}
	for _, bad := range []string{"tools: [Vec]", "tools: [vec-pg, vec-pg]", "tools: [\"vec pg\"]", "tools: [_x]", "tools: vec-pg"} {
		if _, problems := Parse("roles:\n  a: {run: pi.run, "+bad+"}\n", "fitness/squadra.md"); len(problems) == 0 {
			t.Errorf("%s: no problem", bad)
		}
	}
}

// profileNotes are profile notes of the project fitness, as ProfilesFrom
// reads them from the vault.
var profileNotes = map[string]string{
	"fitness/profiles/analista.md": "---\ntitle: Analista\ntype: profile\n---\n\n# Analista\n\n```profile\nrun: claude.run\nmodel: sonnet\naccess: read\nreview: true\ntools: [vec-pg]\nskills: [skills/sql]\nguide: guide/dati\nagent: \"[[fitness/agents/analista]]\"\n```\n",
	"fitness/profiles/strano.md":   "---\ntype: profile\n---\n\n```profile\nrun: claude.run\nrepo: agents-fitness\n```\n",
	"fitness/profiles/nota.md":     "---\ntype: note\n---\n\n```profile\nrun: claude.run\n```\n",
	"fitness/profiles/due.md":      "---\ntype: profile\n---\n\n```profile\nrun: claude.run\n```\n\n```profile\nrun: pi.run\n```\n",
	"fitness/altro/vuoto.md":       "---\ntype: profile\n---\n\n```profile\n```\n",
}

var testProfiles = ProfilesFrom(func(p string) ([]byte, error) {
	c, ok := profileNotes[p]
	if !ok {
		return nil, errors.New("no such note")
	}
	return []byte(c), nil
})

// A role takes the fields of its profile it does not write (IMP-233), and
// its checks hold for what it ends up with.
func TestParseTeamProfiles(t *testing.T) {
	src := "alert: [\"#a\"]\nroles:\n" +
		"  a: {profile: analista}\n" +
		"  b: {profile: \"[[fitness/profiles/analista]]\", model: opus, tools: [], review: false}\n" +
		"  c: {profile: analista, repo: agents-fitness}\n" +
		"  d: {profile: fitness/altro/vuoto.md, run: pi.run}\n"
	team, problems := ParseWith(src, "fitness/squadra.md", testProfiles)
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	a, _ := team.Role("a")
	if a.Profile != "fitness/profiles/analista.md" || a.Run != "claude.run" || a.Model != "sonnet" || a.Access != AccessRead || !a.Review ||
		strings.Join(a.Tools, " ") != "vec-pg" || strings.Join(a.Skills, " ") != "fitness/skills/sql.md" || a.Guide != "fitness/guide/dati.md" ||
		a.Agent != "fitness/agents/analista.md" {
		t.Fatalf("a: %+v", a)
	}
	b, _ := team.Role("b")
	if b.Model != "opus" || b.Tools == nil || len(b.Tools) != 0 || b.Review || b.Run != "claude.run" {
		t.Fatalf("b, its own fields win: %+v", b)
	}
	// The profile reads, the role has the repository: it reads it, and
	// nobody approves what it does not change.
	c, _ := team.Role("c")
	if c.Commits() || c.Approve != ApproveNone || c.Repo != "agents-fitness" {
		t.Fatalf("c: %+v", c)
	}
	d, _ := team.Role("d")
	if d.Profile != "fitness/altro/vuoto.md" || d.Run != "pi.run" {
		t.Fatalf("d: %+v", d)
	}

	for name, tc := range map[string]struct{ src, want string }{
		"missing":       {"roles:\n  a: {profile: nessuno}\n", "role a: profile fitness/profiles/nessuno.md: no note of the project"},
		"not a profile": {"roles:\n  a: {profile: nota}\n", "role a: profile fitness/profiles/nota.md: no note type: profile"},
		"two blocks":    {"roles:\n  a: {profile: due}\n", "a profile holds one ```profile block, this one 2"},
		"team's field":  {"roles:\n  a: {profile: strano}\n", "repo is no field of a profile"},
		"bad name":      {"roles:\n  a: {profile: Analista, run: pi.run}\n", "profile \"Analista\": a name of profiles/"},
		"outside":       {"roles:\n  a: {profile: \"../x\", run: pi.run}\n", "is no note of the project fitness"},
	} {
		_, problems := ParseWith(tc.src, "fitness/squadra.md", testProfiles)
		if len(problems) == 0 || !strings.Contains(problems[0].Msg, tc.want) {
			t.Errorf("%s: %v, want %q", name, problems, tc.want)
		}
	}
	// Without the profiles, a role that names one says so.
	if _, problems := Parse("roles:\n  a: {profile: analista, run: pi.run}\n", "fitness/squadra.md"); len(problems) != 1 || !strings.Contains(problems[0].Msg, "read by the server") {
		t.Errorf("Parse: %v", problems)
	}
}

// The lint reads a profile's block alone: its fields, and their values as
// a role's.
func TestCheckProfile(t *testing.T) {
	if p := CheckProfile("run: claude.run\naccess: read\ntools: [vec-pg]\n", "fitness/profiles/a.md"); len(p) != 0 {
		t.Errorf("a good profile: %v", p)
	}
	if p := CheckProfile("", "fitness/profiles/a.md"); len(p) != 0 {
		t.Errorf("an empty profile: %v", p)
	}
	for src, want := range map[string]string{
		"access: root\n":          "access \"root\": read, edit or exec",
		"repo: x\n":               "repo is no field of a profile",
		"run: claude\n":           "run \"claude\"",
		"tools: [Vec]\n":          "tools:",
		"- a\n":                   "a profile is a mapping",
		"skills: [\"../../x\"]\n": "skills:",
	} {
		if p := CheckProfile(src, "fitness/profiles/a.md"); len(p) == 0 || !strings.Contains(p[0].Msg, want) {
			t.Errorf("%q: %v, want %q", src, p, want)
		}
	}
}

// A team's rooms (IMP-233): a name its roles delegate to, and the room's
// note, a note of the project.
func TestParseTeamRooms(t *testing.T) {
	src := "coordinator: capo\nrooms:\n  front: \"[[fitness/front]]\"\n  back: squadre/back.md\nroles:\n  capo: {run: pi.run, delegates_to: [dev, front, back]}\n  dev: {run: pi.run}\n"
	team, problems := Parse(src, "fitness/squadra.md")
	if len(problems) > 0 {
		t.Fatalf("problems: %v", problems)
	}
	if fmt.Sprint(team.Rooms) != "[{front fitness/front.md 3} {back fitness/squadre/back.md 4}]" {
		t.Fatalf("rooms: %v", team.Rooms)
	}
	if r, ok := team.RoomOf("fitness/squadre/back.md"); !ok || r.Name != "back" {
		t.Fatalf("RoomOf: %v %v", r, ok)
	}
	for name, src := range map[string]string{
		"a role's name": "rooms:\n  dev: \"[[fitness/front]]\"\nroles:\n  dev: {run: pi.run}\n",
		"itself":        "rooms:\n  front: \"[[fitness/squadra]]\"\nroles:\n  dev: {run: pi.run}\n",
		"twice":         "rooms:\n  a: front\n  b: \"[[fitness/front]]\"\nroles:\n  dev: {run: pi.run}\n",
		"outside":       "rooms:\n  a: \"../x\"\nroles:\n  dev: {run: pi.run}\n",
		"no link":       "rooms:\n  a: [x]\nroles:\n  dev: {run: pi.run}\n",
		"a list":        "rooms: [front]\nroles:\n  dev: {run: pi.run}\n",
		"bad name":      "rooms:\n  Front: front\nroles:\n  dev: {run: pi.run}\n",
		"to no room":    "roles:\n  capo: {run: pi.run, delegates_to: [front]}\n",
		"a receiver's":  "rooms:\n  front: front\nroles:\n  capo: {run: pi.run, delegates_to: [lead]}\n  lead: {run: pi.run, delegates_to: [front]}\n",
	} {
		if _, problems := Parse(src, "fitness/squadra.md"); len(problems) == 0 {
			t.Errorf("%s: no problem", name)
		}
	}
}

func TestLinkPath(t *testing.T) {
	cases := map[string]string{
		"[[fitness/squadra]]":   "fitness/squadra.md",
		"[[squadra|the team]]":  "fitness/squadra.md",
		"fitness/tasks/x.md":    "fitness/tasks/x.md",
		"[[fitness/a#Heading]]": "fitness/a.md",
		"":                      "",
		"  [[ fitness/b ]]  ":   "fitness/b.md",
	}
	for in, want := range cases {
		if got := LinkPath(in, "fitness"); got != want {
			t.Errorf("LinkPath(%q) = %q, want %q", in, got, want)
		}
	}
	if Link("fitness/squadra.md") != "[[fitness/squadra]]" {
		t.Error("Link")
	}
}

func TestTaskRenderAndRead(t *testing.T) {
	at := time.Date(2026, 10, 25, 9, 30, 0, 0, time.UTC)
	n := NewTask{Title: "Scheda: principiante", Body: "Fai la scheda.", Team: "fitness/squadra.md", Assignee: "sviluppo",
		Parent: "fitness/tasks/obiettivo.md", DependsOn: []string{"fitness/tasks/esercizi.md"}, Paths: []string{"web/"}, BudgetUSD: 2.5}
	p := TaskPath("fitness", n.Title, at)
	if p != "fitness/tasks/20261025-093000-scheda-principiante.md" {
		t.Fatalf("path %s", p)
	}
	content := n.Render("fitness", at)
	if !IsTask(parser.FrontmatterRawForPath(p, content)) {
		t.Fatalf("not a task:\n%s", content)
	}
	task := ReadTask(p, content)
	if task.Title != "Scheda: principiante" || task.Status != Submitted || task.Team != "fitness/squadra.md" || task.Assignee != "sviluppo" ||
		task.Parent != "fitness/tasks/obiettivo.md" || len(task.DependsOn) != 1 || task.DependsOn[0] != "fitness/tasks/esercizi.md" ||
		task.BudgetUSD != 2.5 || len(task.Paths) != 1 {
		t.Fatalf("task: %+v\n%s", task, content)
	}
	lease := at.Add(15 * time.Minute)
	content, err := Set(content, map[string]any{KeyStatus: Working, KeyClaimedBy: "run-1", KeyLeaseUntil: lease.Format(time.RFC3339), KeyAttempts: 1, KeyCostUSD: 0.42})
	if err != nil {
		t.Fatal(err)
	}
	task = ReadTask(p, content)
	if task.Status != Working || task.ClaimedBy != "run-1" || !task.LeaseUntil.Equal(lease) || task.Attempts != 1 || task.CostUSD != 0.42 {
		t.Fatalf("after Set: %+v\n%s", task, content)
	}
	content, err = Set(content, map[string]any{KeyStatus: Submitted}, KeyClaimedBy, KeyLeaseUntil)
	if err != nil {
		t.Fatal(err)
	}
	task = ReadTask(p, content)
	if task.ClaimedBy != "" || !task.LeaseUntil.IsZero() {
		t.Fatalf("after unset: %+v", task)
	}
	content = AppendSection(content, "Report", "Fatto.")
	if !strings.HasSuffix(string(content), "## Report\n\nFatto.\n") {
		t.Fatalf("section:\n%s", content)
	}
	// The branch an attempt sent back left, until the task closes.
	content, err = Set(content, map[string]any{KeyStatus: Working, KeyClaimedBy: "run-2", KeyBranch: "agent/r1-s2"})
	if err != nil {
		t.Fatal(err)
	}
	if task = ReadTask(p, content); task.Branch != "agent/r1-s2" {
		t.Fatalf("branch: %+v\n%s", task, content)
	}
	if content, err = Complete(task, content, "run-2", Completed, "Fatto.", at); err != nil {
		t.Fatal(err)
	}
	if task = ReadTask(p, content); task.Branch != "" || task.Status != Completed {
		t.Fatalf("after Complete: %+v\n%s", task, content)
	}
}

func TestWaits(t *testing.T) {
	now := time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)
	task := Task{Path: "p/tasks/b.md", Status: Submitted, DependsOn: []string{"p/tasks/a.md"}}
	deps := map[string]Task{"p/tasks/a.md": {Status: Working}}
	if w := task.Waits(deps, now); !strings.Contains(w, "waits for p/tasks/a.md") {
		t.Fatalf("waits %q", w)
	}
	deps["p/tasks/a.md"] = Task{Status: Completed}
	if w := task.Waits(deps, now); w != "" {
		t.Fatalf("ready task waits %q", w)
	}
	if w := task.Waits(map[string]Task{}, now); !strings.Contains(w, "no task") {
		t.Fatalf("missing dependency: %q", w)
	}
	task.LeaseUntil, task.ClaimedBy = now.Add(time.Minute), "agent@x"
	if w := task.Waits(deps, now); !strings.Contains(w, "held by") {
		t.Fatalf("held: %q", w)
	}
	task.Status = Working
	if w := task.Waits(deps, now); w == "" {
		t.Fatal("a working task is ready")
	}
}

func TestCanMove(t *testing.T) {
	if !CanMove(Submitted, Working) || !CanMove(Working, Review) || !CanMove(Failed, Submitted) {
		t.Fatal("allowed move refused")
	}
	if CanMove(Completed, Submitted) || CanMove(Submitted, Completed) || CanMove("bogus", Working) {
		t.Fatal("refused move allowed")
	}
}

func TestNotes(t *testing.T) {
	db := DatabaseNote("fitness")
	if !strings.Contains(string(db), "source: fitness/tasks") || !strings.Contains(string(db), "input-required") {
		t.Fatalf("database:\n%s", db)
	}
	team := TeamNote("fitness", "squadra", "claude.run", []string{"capo", "sviluppo", "revisore"}, "capo")
	blocks := Find(team)
	if len(blocks) != 1 {
		t.Fatalf("blocks %d:\n%s", len(blocks), team)
	}
	parsed, problems := Parse(blocks[0].Src, "fitness/squadra.md")
	if len(problems) > 0 {
		t.Fatalf("the new team's block: %v", problems)
	}
	if capo, _ := parsed.Role("capo"); parsed.Coordinator != "capo" || strings.Join(capo.DelegatesTo, ",") != "sviluppo,revisore" || capo.Agent != "fitness/agents/capo.md" {
		t.Fatalf("the new team: %+v", parsed)
	}
	if v := ViewNote("fitness", "fitness/squadra.md"); !strings.Contains(string(v), "team = [[fitness/squadra]]") {
		t.Fatalf("view:\n%s", v)
	}
	// Their frontmatter reads as YAML, as every reader of the vault needs.
	for name, c := range map[string][]byte{"database": db, "team": team, "view": ViewNote("fitness", "fitness/squadra.md"),
		"agent": AgentNote("fitness", "capo", true), "work": WorkPipeline("fitness/squadra.md"), "coordinate": CoordinatePipeline("fitness/squadra.md")} {
		if err := frontmatter.YAMLError(parser.ExtractFrontmatterRaw(c)); err != nil {
			t.Errorf("%s: %v\n%s", name, err, c)
		}
	}
}

func TestAppendSectionDemotesTheTextsHeadings(t *testing.T) {
	report := "# Revisione: problemi aperti\n\n## Errori\n\n- uno\n\n```markdown\n# Scheda\n## Settimana 1\n```\n\n###### Nota\n#hashtag e #### fine"
	got := string(AppendSection([]byte("---\ntitle: t\n---\n\n# t\n"), "Report", report))
	want := "## Report\n\n### Revisione: problemi aperti\n\n#### Errori\n\n- uno\n\n```markdown\n# Scheda\n## Settimana 1\n```\n\n###### Nota\n#hashtag e #### fine\n"
	if !strings.HasSuffix(got, want) {
		t.Fatalf("got:\n%s", got)
	}
	if s := LastSection([]byte(got), "Report"); !strings.HasPrefix(s, "### Revisione") || !strings.HasSuffix(s, "#### fine") {
		t.Fatalf("the report read back: %q", s)
	}
}

func TestLastSection(t *testing.T) {
	note := "---\ntitle: t\n---\n\n# t\n\nIl compito.\n\n## Report\n\nIl primo.\n\n## 2026-10-25 17:00 — submitted\n\nDi nuovo.\n\n## Report\n\nIl secondo.\n\n```\n## Report\n```\n"
	if got := LastSection([]byte(note), "Report"); got != "Il secondo.\n\n```\n## Report\n```" {
		t.Fatalf("last: %q", got)
	}
	if got := LastSection([]byte("---\ntitle: t\n---\n\n# t\n\n## Report\n\nUno.\n\n## Dopo\n\nAltro.\n"), "Report"); got != "Uno." {
		t.Fatalf("up to the next ##: %q", got)
	}
	if got := LastSection([]byte("# t\n\nNiente.\n"), "Report"); got != "" {
		t.Fatalf("none: %q", got)
	}
}
