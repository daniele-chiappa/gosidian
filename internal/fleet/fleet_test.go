package fleet

import (
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
	if dev.Approve != ApproveMerge || dev.Agent != "fitness/agents/sviluppo.md" || len(dev.Paths) != 2 {
		t.Fatalf("sviluppo: %+v", dev)
	}
	know, _ := team.Role("conoscenza")
	if know.Approve != ApproveNone || len(know.Writes) != 1 || know.Writes[0] != "fitness/conoscenza/" || know.MaxTokens != 5_000_000 {
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
	}
	for name, src := range cases {
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
