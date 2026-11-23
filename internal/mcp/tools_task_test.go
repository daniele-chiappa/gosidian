package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/fleet"
	"github.com/gosidian/gosidian/internal/jobs"
	"github.com/gosidian/gosidian/internal/pipeline"
	"github.com/gosidian/gosidian/internal/projects"
)

const testFleetTeam = "---\ntitle: squadra\ntype: team\ntags: [p, type:team]\n---\n\n# squadra\n\n```team\ncoordinator: capo\nroles:\n" +
	"  capo:\n    run: claude.run\n    delegates_to: [dev]\n  dev:\n    run: claude.run\n    writes: [p/kb]\n```\n"

// seedFleet creates the team p/squadra and a task p/tasks/a.md, through
// the tools so the index has them.
func seedFleet(t *testing.T, s *Server, ctx context.Context, task string) string {
	t.Helper()
	for rel, body := range map[string]string{"p/squadra.md": testFleetTeam, "p/tasks/a.md": task} {
		if res, _ := s.handleCreate(ctx, call(map[string]any{"path": rel, "content": body})); res.IsError {
			t.Fatalf("seed %s: %s", rel, expectError(t, res))
		}
	}
	return "p/tasks/a.md"
}

func fleetTask(status, extra string) string {
	return "---\ntitle: pagina\ntype: task\nstatus: " + status + "\nteam: \"[[p/squadra]]\"\nassignee: dev\n" + extra + "tags: [p, type:task]\n---\n\n# pagina\n\nFai la pagina.\n"
}

func taskOf(t *testing.T, s *Server, rel string) fleet.Task {
	t.Helper()
	task, _, err := s.fleetRead(rel)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestTaskTools_ClaimHeartbeatUpdateComplete(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	rel := seedFleet(t, s, ctx, fleetTask("submitted", ""))
	holder := tokenIdentity(auth.AdminToken())

	resultText(t, mustCall(s.handleTaskClaim(ctx, call(map[string]any{"path": rel}))))
	task := taskOf(t, s, rel)
	if task.Status != fleet.Working || task.ClaimedBy != holder || task.Attempts != 1 || task.LeaseUntil.IsZero() {
		t.Fatalf("claimed: %+v", task)
	}
	if msg := expectError(t, mustCall(s.handleTaskClaim(ctx, call(map[string]any{"path": rel})))); !strings.Contains(msg, "status working") {
		t.Fatalf("second claim: %s", msg)
	}
	before := task.LeaseUntil
	resultText(t, mustCall(s.handleTaskHeartbeat(ctx, call(map[string]any{"path": rel}))))
	if after := taskOf(t, s, rel).LeaseUntil; after.Before(before) {
		t.Fatalf("lease went back: %v < %v", after, before)
	}
	resultText(t, mustCall(s.handleTaskUpdate(ctx, call(map[string]any{"path": rel, "status": "review", "message": "Guarda la PR."}))))
	if task = taskOf(t, s, rel); task.Status != fleet.Review {
		t.Fatalf("updated: %+v", task)
	}
	if msg := expectError(t, mustCall(s.handleTaskUpdate(ctx, call(map[string]any{"path": rel, "status": "completed"})))); !strings.Contains(msg, "closing a task completes it") {
		t.Fatalf("update to completed: %s", msg)
	}
	resultText(t, mustCall(s.handleTaskComplete(ctx, call(map[string]any{"path": rel, "report": "Pagina fatta."}))))
	task = taskOf(t, s, rel)
	note, _ := s.vault.Load(rel)
	if task.Status != fleet.Completed || task.ClaimedBy != "" || !strings.Contains(string(note.Content), "## Report\n\nPagina fatta.") ||
		!strings.Contains(string(note.Content), "Guarda la PR.") {
		t.Fatalf("completed: %+v\n%s", task, note.Content)
	}
}

func TestTaskTools_OnlyTheHolderMovesATask(t *testing.T) {
	s, _, _ := newTestServer(t)
	rel := seedFleet(t, s, context.Background(), fleetTask("working", "claimed_by: someone@else\n"))
	msg := expectError(t, mustCall(s.handleTaskComplete(context.Background(), call(map[string]any{"path": rel, "report": "x"}))))
	if !strings.Contains(msg, "held by someone@else") {
		t.Fatalf("complete by another: %s", msg)
	}
	run := &auth.Token{ID: "r1", Name: "run-x-n", Kind: auth.KindRun, RunID: "x", Projects: []string{"p"}, Scopes: []string{auth.ScopeRead}}
	for name, h := range map[string]toolFn{"update": s.handleTaskUpdate, "complete": s.handleTaskComplete, "claim": s.handleTaskClaim, "heartbeat": s.handleTaskHeartbeat} {
		msg = expectError(t, mustCall(h(ctxWithToken(run), call(map[string]any{"path": rel, "status": "working", "report": "x"}))))
		if !strings.Contains(msg, "its pipeline moves and closes the task") {
			t.Fatalf("%s by a run's agent: %s", name, msg)
		}
	}
}

func TestTaskTools_CreateChecksTheTeam(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	obj := seedFleet(t, s, ctx, "---\ntitle: obiettivo\ntype: task\nstatus: working\nteam: \"[[p/squadra]]\"\nassignee: capo\ntags: [p, type:task]\n---\n\n# obiettivo\n")
	res := mustCall(s.handleTaskCreate(ctx, call(map[string]any{"project": "p", "team": "[[p/squadra]]", "assignee": "dev",
		"title": "La pagina", "body": "Fai la pagina.", "parent": "[[p/tasks/a]]", "paths": []any{"web/"}, "budget_usd": 1.5})))
	var out struct {
		Task fleet.Task `json:"task"`
	}
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	if out.Task.Parent != obj || out.Task.Status != fleet.Submitted || out.Task.BudgetUSD != 1.5 || !strings.HasPrefix(out.Task.Path, "p/tasks/") {
		t.Fatalf("created: %+v", out.Task)
	}
	for name, args := range map[string]map[string]any{
		"no team":       {"project": "p", "team": "altra", "assignee": "dev", "title": "x"},
		"no role":       {"project": "p", "team": "squadra", "assignee": "ops", "title": "x"},
		"two levels":    {"project": "p", "team": "squadra", "assignee": "dev", "title": "x", "parent": out.Task.Path},
		"no dependency": {"project": "p", "team": "squadra", "assignee": "dev", "title": "x", "depends_on": []any{"p/tasks/zz"}},
		"no title":      {"project": "p", "team": "squadra", "assignee": "dev"},
	} {
		if res := mustCall(s.handleTaskCreate(ctx, call(args))); !res.IsError {
			t.Errorf("%s: created", name)
		}
	}
	// A run token without a team's run behind it creates none.
	run := &auth.Token{ID: "r1", Name: "run-x-n", Kind: auth.KindRun, RunID: "x", Projects: []string{"p"}, Scopes: []string{auth.ScopeRead}}
	msg := expectError(t, mustCall(s.handleTaskCreate(ctxWithToken(run), call(map[string]any{"project": "p", "team": "squadra", "assignee": "dev", "title": "x"}))))
	if !strings.Contains(msg, "runs no pipelines") {
		t.Fatalf("run token: %s", msg)
	}
}

func TestTaskTools_ListAndCreateTeam(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	res := mustCall(s.handleCreateTeam(ctx, call(map[string]any{"project": "p", "name": "squadra", "roles": []any{"capo", "dev", "test"}, "coordinator": "capo"})))
	text := resultText(t, res)
	for _, want := range []string{"p/squadra.md", "p/tasks.md", "p/squadra-tasks.md", "p/pipelines/squadra-work.md", "p/pipelines/squadra-coordinate.md",
		"p/agents/capo.md", "p/agents/dev.md", "p/agents/test.md"} {
		if !strings.Contains(text, want) {
			t.Fatalf("written lacks %s: %s", want, text)
		}
	}
	// The starter profiles (IMP-233), with the team's executor; a role that
	// takes one reads with it, and the lint finds nothing in them.
	for _, name := range []string{"developer", "reviewer", "analyst"} {
		p := "p/profiles/" + name + ".md"
		if !strings.Contains(text, p) {
			t.Fatalf("written lacks %s: %s", p, text)
		}
		n, err := s.vault.Load(p)
		if err != nil || !strings.Contains(string(n.Content), "```profile\nrun: claude.run\n") || !strings.Contains(string(n.Content), "type: profile") {
			t.Fatalf("%s: %v\n%s", p, err, n.Content)
		}
		if problems := fleet.CheckProfile(fleet.FindProfiles(n.Content)[0].Src, p); len(problems) > 0 {
			t.Errorf("%s: %v", p, problems)
		}
	}
	if res, _ := s.handleEdit(ctx, call(map[string]any{"path": "p/squadra.md", "old_string": "  test:\n", "new_string": "  test:\n    profile: reviewer\n"})); res.IsError {
		t.Fatal(expectError(t, res))
	}
	teams, err := s.fleetTeams("p")
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := teams["p/squadra.md"].Role("test"); !ok || !r.Review || r.Access != fleet.AccessRead || r.Profile != "p/profiles/reviewer.md" {
		t.Fatalf("test with the reviewer's profile: %+v %v", r, ok)
	}
	if res := mustCall(s.handleCreateTeam(ctx, call(map[string]any{"project": "p", "name": "squadra"}))); !res.IsError {
		t.Fatal("the same team twice")
	}
	if res := mustCall(s.handleCreateTeam(ctx, call(map[string]any{"project": "p", "name": "terza", "roles": []any{"dev"}, "coordinator": "capo"}))); !res.IsError {
		t.Fatal("a coordinator that is no role")
	}
	// A second team keeps the database and the agent notes there are.
	text = resultText(t, mustCall(s.handleCreateTeam(ctx, call(map[string]any{"project": "p", "name": "altra"}))))
	if strings.Contains(text, "p/tasks.md") || strings.Contains(text, "p/agents/dev.md") || strings.Contains(text, "p/profiles/") {
		t.Fatalf("written again: %s", text)
	}
	if res, _ := s.handleCreate(ctx, call(map[string]any{"path": "p/tasks/a.md", "content": fleetTask("submitted", "")})); res.IsError {
		t.Fatal(expectError(t, res))
	}
	var out struct {
		Teams []map[string]any `json:"teams"`
		Tasks []fleet.Task     `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(resultText(t, mustCall(s.handleTasks(ctx, call(map[string]any{"project": "p", "team": "squadra"}))))), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Teams) != 1 || len(out.Tasks) != 1 || out.Tasks[0].Assignee != "dev" {
		t.Fatalf("tasks: %+v", out)
	}
}

// A task for a room is an objective of the room's team, for its
// coordinator, under the objective of the team that delegates; the room's
// roles take sub-tasks of it, the one sub-task that has some (IMP-233).
func TestTaskTools_ARoomsObjective(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	squadra := strings.Replace(strings.Replace(testFleetTeam, "```team\n", "```team\nrooms:\n  front: \"[[p/front]]\"\n", 1), "delegates_to: [dev]", "delegates_to: [dev, front]", 1)
	front := "---\ntitle: front\ntype: team\n---\n\n```team\ncoordinator: lead\nroles:\n  lead:\n    run: claude.run\n    delegates_to: [ui]\n  ui:\n    run: claude.run\n```\n"
	for rel, body := range map[string]string{"p/squadra.md": squadra, "p/front.md": front} {
		if res, _ := s.handleCreate(ctx, call(map[string]any{"path": rel, "content": body})); res.IsError {
			t.Fatalf("seed %s: %s", rel, expectError(t, res))
		}
	}
	create := func(args map[string]any) (fleet.Task, string) {
		t.Helper()
		args["project"] = "p"
		res := mustCall(s.handleTaskCreate(ctx, call(args)))
		if res.IsError {
			return fleet.Task{}, expectError(t, res)
		}
		var out struct {
			Task fleet.Task `json:"task"`
		}
		if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
			t.Fatal(err)
		}
		return out.Task, ""
	}
	obj, msg := create(map[string]any{"team": "squadra", "assignee": "capo", "title": "obiettivo"})
	if msg != "" {
		t.Fatal(msg)
	}
	room, msg := create(map[string]any{"team": "squadra", "assignee": "front", "title": "la pagina", "parent": obj.Path})
	if msg != "" || room.Team != "p/front.md" || room.Assignee != "lead" || room.Parent != obj.Path {
		t.Fatalf("the room's objective: %+v %s", room, msg)
	}
	if ui, msg := create(map[string]any{"team": "front", "assignee": "ui", "title": "il bottone", "parent": room.Path}); msg != "" || ui.Parent != room.Path || ui.Team != "p/front.md" {
		t.Fatalf("the room's task: %+v %s", ui, msg)
	}
	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"team": "squadra", "assignee": "dev", "title": "x", "parent": room.Path}, "is itself a sub-task"},
		{map[string]any{"team": "squadra", "assignee": "nessuno", "title": "x"}, "is no role nor room of p/squadra.md"},
		{map[string]any{"team": "front", "assignee": "ui", "title": "x", "parent": obj.Path}, "is a task of another team"},
	} {
		if _, msg := create(c.args); !strings.Contains(msg, c.want) {
			t.Errorf("%v: %q, want %q", c.args, msg, c.want)
		}
	}
}

// The task trigger through the vault's own host: it takes a ready objective
// for its coordinator's run, whose run token gives tasks only to the roles
// it delegates to, under its own task, and moves none.
func TestTaskTools_ACoordinatorsRunDelegates(t *testing.T) {
	s, _, _ := newTestServer(t)
	pstore, err := projects.Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := pstore.Set("p", projects.Flags{UsePipelines: true}); err != nil {
		t.Fatal(err)
	}
	s.SetProjects(pstore)
	s.SetPipelinesSwitch(true)
	ctx := context.Background()
	obj := seedFleet(t, s, ctx, "---\ntitle: obiettivo\ntype: task\nstatus: submitted\nteam: \"[[p/squadra]]\"\nassignee: capo\ntags: [p, type:task]\n---\n\n# obiettivo\n")
	coordina := "---\ntype: pipeline\n---\n\n```pipeline\ntrigger: {on: task, roles: [capo]}\nnodes:\n  - {id: w, use: wait.call}\n```\n"
	if res, _ := s.handleCreate(ctx, call(map[string]any{"path": "p/pipelines/coordina.md", "content": coordina})); res.IsError {
		t.Fatal(expectError(t, res))
	}
	store, err := jobs.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	runner := jobs.NewRunner(store, jobs.Options{Workers: 1, Poll: 10 * time.Millisecond, Lease: time.Minute})
	e := pipeline.New(s.PipelineHost(), runner, pipeline.Config{Location: time.UTC}, nil, nil)
	s.SetPipelines(e)
	e.TickTasks()
	o := taskOf(t, s, obj)
	runID, ok := strings.CutPrefix(o.ClaimedBy, "run-")
	if o.Status != fleet.Working || !ok || o.Attempts != 1 {
		t.Fatalf("objective: %+v", o)
	}
	run := ctxWithToken(&auth.Token{ID: "r1", Name: "run-" + runID + "-w", Kind: auth.KindRun, RunID: runID, Projects: []string{"p"}, Scopes: []string{auth.ScopeRead}})
	var out struct {
		Task fleet.Task `json:"task"`
	}
	if err := json.Unmarshal([]byte(resultText(t, mustCall(s.handleTaskCreate(run, call(map[string]any{"project": "p", "team": "squadra", "assignee": "dev", "title": "La pagina"}))))), &out); err != nil {
		t.Fatal(err)
	}
	if out.Task.Parent != obj || out.Task.Assignee != "dev" {
		t.Fatalf("sub-task: %+v", out.Task)
	}
	if msg := expectError(t, mustCall(s.handleTaskCreate(run, call(map[string]any{"project": "p", "team": "squadra", "assignee": "capo", "title": "x"})))); !strings.Contains(msg, "does not give tasks to capo") {
		t.Fatalf("to itself: %s", msg)
	}
	if msg := expectError(t, mustCall(s.handleTaskCreate(run, call(map[string]any{"project": "p", "team": "squadra", "assignee": "dev", "title": "x", "parent": out.Task.Path})))); !strings.Contains(msg, "a sub-task of your own task") {
		t.Fatalf("under another task: %s", msg)
	}
	// It moves no task, its own neither: its pipeline does.
	if msg := expectError(t, mustCall(s.handleTaskComplete(run, call(map[string]any{"path": obj, "report": "fatto"})))); !strings.Contains(msg, "its pipeline moves and closes") {
		t.Fatalf("complete its own: %s", msg)
	}
	if o = taskOf(t, s, obj); o.Status != fleet.Working || o.ClaimedBy != "run-"+runID {
		t.Fatalf("objective: %+v", o)
	}
	// Its run's role writes nowhere: a write token is not for it.
	if paths, scoped, err := e.RunWritePaths(runID); err != nil || !scoped || len(paths) != 0 {
		t.Fatalf("write paths %v %v %v", paths, scoped, err)
	}
	// Its role names no tools: its tokens see every plugin of the project
	// (IMP-233).
	if tools, limited, err := e.RunTools(runID); err != nil || limited || tools != nil {
		t.Fatalf("tools %v %v %v", tools, limited, err)
	}
	if tools, limited, err := e.RunTools("none"); err == nil || limited || tools != nil {
		t.Fatalf("a run that is none: %v %v %v", tools, limited, err)
	}
}

// What a run's token writes is kept for the gate of its task, until the
// run ends; another token's writes are not.
func TestTaskTools_ARunsWritesAreKeptForItsGate(t *testing.T) {
	s, _, _ := newTestServer(t)
	store, err := jobs.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	runner := jobs.NewRunner(store, jobs.Options{Workers: 1, Poll: 10 * time.Millisecond, Lease: time.Minute})
	e := pipeline.New(s.PipelineHost(), runner, pipeline.Config{Location: time.UTC}, nil, nil)
	s.SetPipelines(e)
	run := ctxWithToken(&auth.Token{ID: "r1", Name: "run-x-agent", Kind: auth.KindRun, RunID: "x", Projects: []string{"p"},
		Scopes: []string{auth.ScopeRead, auth.ScopeWrite}, WritePaths: []string{"p/kb/"}})
	if res, _ := s.handleCreate(run, call(map[string]any{"path": "p/kb/nota.md", "content": "# nota\n"})); res.IsError {
		t.Fatal(expectError(t, res))
	}
	if res, _ := s.handleAppend(run, call(map[string]any{"path": "p/kb/nota.md", "content": "altro"})); res.IsError {
		t.Fatal(expectError(t, res))
	}
	other := ctxWithToken(&auth.Token{ID: "w1", Name: "w", Projects: []string{"p"}, Scopes: []string{auth.ScopeRead, auth.ScopeWrite}})
	if res, _ := s.handleCreate(other, call(map[string]any{"path": "p/kb/altra.md", "content": "# altra\n"})); res.IsError {
		t.Fatal(expectError(t, res))
	}
	if got := e.RunWrites("x"); len(got) != 1 || got[0] != "p/kb/nota.md" {
		t.Fatalf("run x wrote %v", got)
	}
}
