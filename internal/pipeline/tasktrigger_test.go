package pipeline

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/fleet"
	"github.com/gosidian/gosidian/internal/jobs"
	"github.com/gosidian/gosidian/internal/parser"
)

// The fake vault keeps teams and tasks too (Fleet), from its notes.

func (h *fakeHost) Teams(project string) (map[string]fleet.Team, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string]fleet.Team{}
	for p, c := range h.notes {
		if !strings.HasPrefix(p, project+"/") || !strings.Contains(c, "type: team") {
			continue
		}
		for _, b := range fleet.Find([]byte(c)) {
			if t, problems := fleet.Parse(b.Src, p); len(problems) == 0 {
				out[p] = t
			}
		}
	}
	return out, nil
}

func (h *fakeHost) Tasks(project string) ([]fleet.Task, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []fleet.Task
	for p, c := range h.notes {
		if strings.HasPrefix(p, project+"/tasks/") && fleet.IsTask(parser.ExtractFrontmatterRaw([]byte(c))) {
			out = append(out, fleet.ReadTask(p, []byte(c)))
		}
	}
	return out, nil
}

func (h *fakeHost) ReadTask(p string) (fleet.Task, []byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.notes[p]
	if !ok {
		return fleet.Task{}, nil, ErrNoNote
	}
	return fleet.ReadTask(p, []byte(c)), []byte(c), nil
}

func (h *fakeHost) UpdateTask(p string, build func(fleet.Task, []byte) ([]byte, error), _ string) (fleet.Task, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.notes[p]
	if !ok {
		return fleet.Task{}, ErrNoNote
	}
	out, err := build(fleet.ReadTask(p, []byte(c)), []byte(c))
	if err != nil {
		return fleet.Task{}, err
	}
	h.notes[p] = string(out)
	return fleet.ReadTask(p, out), nil
}

func (h *fakeHost) CreateTask(project string, n fleet.NewTask, _ string) (fleet.Task, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	at := time.Now()
	p := fleet.TaskPath(project, n.Title, at)
	c := n.Render(project, at)
	h.notes[p] = string(c)
	return fleet.ReadTask(p, c), nil
}

func (h *fakeHost) task(t *testing.T, p string) fleet.Task {
	t.Helper()
	task, _, err := h.ReadTask(p)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

const testTeam = "---\ntitle: squadra\ntype: team\n---\n\n```team\ncoordinator: capo\nbudget: {day_usd: 4, task_usd: 1}\nroles:\n" +
	"  capo:\n    run: claude.run\n    delegates_to: [dev]\n  dev:\n    agent: \"[[p/agents/dev]]\"\n    run: claude.run\n    paths: [web/]\n```\n"

func taskNote(title, assignee, status string, extra string) string {
	return "---\ntitle: " + title + "\ntype: task\nstatus: " + status + "\nteam: \"[[p/squadra]]\"\nassignee: " + assignee + "\n" + extra + "tags: [p, type:task]\n---\n\n# " + title + "\n\nDo " + title + ".\n"
}

// taskPipeline closes the task with the prompt task.context wrote, after
// a step that costs cost.
func taskPipeline(trigger, cost string) string {
	return pipelineNote(trigger + `
nodes:
  - id: ctx
    use: task.context
  - id: work
    use: starlark
    with:
      code: |
        output = {"cost": ` + cost + `, "usage": {"total_tokens": 100}}
  - id: done
    use: task.complete
    with:
      report: "{{ ctx.prompt }}"
`)
}

func taskEngine(t *testing.T) (*Engine, *fakeHost) {
	e, host := newEngine(t, Config{Tasks: TaskConfig{MaxAttempts: 2}})
	host.notes["p/squadra.md"] = testTeam
	host.notes["p/agents/dev.md"] = "---\ntype: agent\n---\n\n# dev\n\nYou write the web pages.\n"
	return e, host
}

func waitTask(t *testing.T, e *Engine, host *fakeHost, p string, cond func(fleet.Task) bool, what string) fleet.Task {
	t.Helper()
	var task fleet.Task
	eventually(t, func() bool {
		e.TickTasks()
		task = host.task(t, p)
		return cond(task)
	}, what)
	return task
}

func TestTaskTrigger_ReadyTasksRunInTheOrderOfTheirDependencies(t *testing.T) {
	e, host := taskEngine(t)
	host.notes["p/pipelines/regista.md"] = taskPipeline("trigger: {on: task, team: squadra, roles: [dev]}", "0.5")
	host.notes["p/tasks/a.md"] = taskNote("pagina", "dev", "submitted", "")
	host.notes["p/tasks/b.md"] = taskNote("test", "dev", "submitted", "depends_on: [\"[[p/tasks/a]]\"]\n")
	e.TickTasks()
	if b := host.task(t, "p/tasks/b.md"); b.Status != fleet.Submitted || b.ClaimedBy != "" {
		t.Fatalf("b started before a: %+v", b)
	}
	if w := e.TaskWaits("p")["p/tasks/b.md"]; !strings.Contains(w, "waits for p/tasks/a.md") {
		t.Fatalf("b waits %q", w)
	}
	a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "a completed")
	if a.ClaimedBy != "" || a.Attempts != 1 || a.CostUSD != 0.5 || a.Tokens != 100 {
		t.Fatalf("a: %+v", a)
	}
	c := host.note("p/tasks/a.md")
	for _, want := range []string{"## Report", "You are the role \"dev\" of the team p/squadra. Your task: pagina", "Do pagina.", "Change only these paths of the repository: web/"} {
		if !strings.Contains(c, want) {
			t.Fatalf("a lacks %q:\n%s", want, c)
		}
	}
	b := waitTask(t, e, host, "p/tasks/b.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "b completed")
	if !strings.Contains(host.note(b.Path), "### pagina (dev, completed)") {
		t.Fatalf("b's prompt lacks a's report:\n%s", host.note(b.Path))
	}
}

func TestTaskTrigger_TwoTicksStartATaskOnce(t *testing.T) {
	e, host := taskEngine(t)
	host.notes["p/pipelines/regista.md"] = pipelineNote("trigger: {on: task}\nnodes:\n  - id: w\n    use: wait.call\n")
	host.notes["p/tasks/a.md"] = taskNote("pagina", "dev", "submitted", "")
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); e.TickTasks() }()
	}
	wg.Wait()
	runs, err := e.Runner().Store().Runs(jobs.RunFilter{Ref: "p/pipelines/regista.md"})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("%d runs", len(runs))
	}
	a := host.task(t, "p/tasks/a.md")
	if a.Status != fleet.Working || a.ClaimedBy != "run-"+runs[0].ID || a.Attempts != 1 {
		t.Fatalf("a: %+v", a)
	}
	if runs[0].Task != "p/tasks/a.md" || runs[0].Team != "p/squadra.md" {
		t.Fatalf("run: task %q team %q", runs[0].Task, runs[0].Team)
	}
	// Canceled by a person: the task waits for one.
	if err := e.Runner().Cancel(runs[0].ID); err != nil {
		t.Fatal(err)
	}
	a = waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.InputRequired }, "a to a person")
	if a.ClaimedBy != "" || !strings.Contains(host.note(a.Path), "was canceled") {
		t.Fatalf("a: %+v\n%s", a, host.note(a.Path))
	}
}

func TestTaskTrigger_FailedRunsTryAgainThenFail(t *testing.T) {
	e, host := taskEngine(t)
	host.notes["p/pipelines/regista.md"] = pipelineNote("trigger: {on: task}\nnodes:\n  - id: w\n    use: starlark\n    with:\n      code: fail('broken')\n")
	host.notes["p/tasks/a.md"] = taskNote("pagina", "dev", "submitted", "")
	a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Failed }, "a failed")
	c := host.note(a.Path)
	if a.Attempts != 2 || !strings.Contains(c, "Attempt 1 of 2") || !strings.Contains(c, "Attempt 2 of 2 failed, the last one") || !strings.Contains(c, "broken") {
		t.Fatalf("a: %+v\n%s", a, c)
	}
}

func TestTaskTrigger_ARunThatDoesNotCloseItsTaskCallsAPerson(t *testing.T) {
	e, host := taskEngine(t)
	host.notes["p/pipelines/regista.md"] = pipelineNote("trigger: {on: task}\nnodes:\n  - id: w\n    use: starlark\n    with:\n      code: output = 1\n")
	host.notes["p/tasks/a.md"] = taskNote("pagina", "dev", "submitted", "")
	a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.InputRequired }, "a to a person")
	if !strings.Contains(host.note(a.Path), "ended without closing the task") {
		t.Fatal(host.note(a.Path))
	}
	// A person submits it again: it starts once more.
	host.mu.Lock()
	host.notes[a.Path] = strings.Replace(host.notes[a.Path], "status: input-required", "status: submitted", 1)
	host.mu.Unlock()
	waitTask(t, e, host, a.Path, func(x fleet.Task) bool { return x.Attempts == 2 && x.Status == fleet.InputRequired }, "a second attempt")
}

func TestTaskTrigger_AttemptsSetBackByHandStartANewRun(t *testing.T) {
	e, host := taskEngine(t)
	host.notes["p/pipelines/regista.md"] = pipelineNote("trigger: {on: task}\nnodes:\n  - id: w\n    use: starlark\n    with:\n      code: output = 1\n")
	host.notes["p/tasks/a.md"] = taskNote("pagina", "dev", "submitted", "")
	a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.InputRequired }, "a to a person")
	// A person submits it again with its attempts at zero: the key of its
	// first attempt is taken by a run that ended, which must not hold it.
	host.mu.Lock()
	n := strings.Replace(host.notes[a.Path], "status: input-required", "status: submitted", 1)
	host.notes[a.Path] = strings.Replace(n, "attempts: 1", "attempts: 0", 1)
	host.mu.Unlock()
	a = waitTask(t, e, host, a.Path, func(x fleet.Task) bool {
		return x.Status == fleet.InputRequired && strings.Count(host.note(x.Path), "ended without closing the task") == 2
	}, "a second run")
	runs := regexp.MustCompile(`Run ([0-9a-f]+) ended without closing`).FindAllStringSubmatch(host.note(a.Path), -1)
	if len(runs) != 2 || runs[0][1] == runs[1][1] || a.Attempts != 1 {
		t.Fatalf("runs %v attempts %d\n%s", runs, a.Attempts, host.note(a.Path))
	}
}

func TestTaskTrigger_ASpentBudgetHoldsTheTasksAndTellsOnce(t *testing.T) {
	e, host := taskEngine(t)
	host.notes["p/pipelines/regista.md"] = taskPipeline("trigger: {on: task, roles: [dev]}", "5")
	host.notes["p/pipelines/budget.md"] = pipelineNote("trigger: {on: task, events: [budget]}\nnodes:\n  - id: w\n    use: starlark\n    with:\n      input: \"{{ trigger }}\"\n      code: output = input\n")
	host.notes["p/tasks/a.md"] = taskNote("pagina", "dev", "submitted", "")
	waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "a completed")
	s, err := e.TeamSpend("p", "p/squadra.md")
	if err != nil || s.CostUSD != 5 {
		t.Fatalf("spend %+v %v", s, err)
	}
	host.notes["p/tasks/b.md"] = taskNote("test", "dev", "submitted", "")
	e.TickTasks()
	e.TickTasks()
	if b := host.task(t, "p/tasks/b.md"); b.Status != fleet.Submitted || b.ClaimedBy != "" {
		t.Fatalf("b started over budget: %+v", b)
	}
	if w := e.TaskWaits("p")["p/tasks/b.md"]; !strings.Contains(w, "spent its budget of the day (5.00 of 4.00 USD)") {
		t.Fatalf("b waits %q", w)
	}
	runs, err := e.Runner().Store().Runs(jobs.RunFilter{Ref: "p/pipelines/budget.md"})
	if err != nil || len(runs) != 1 {
		t.Fatalf("budget runs %d %v", len(runs), err)
	}
	run := waitStatus(t, e, runs[0].ID, jobs.Done)
	if !strings.Contains(string(run.Input), `"event":"budget"`) || !strings.Contains(string(run.Input), `"budget_usd":4`) {
		t.Fatalf("input %s", run.Input)
	}
}

func TestTaskTrigger_TheCoordinatorRunsWhenTheSubTasksClose(t *testing.T) {
	e, host := taskEngine(t)
	host.notes["p/pipelines/coordina.md"] = pipelineNote(`trigger: {on: task, roles: [capo], events: [children_done]}
nodes:
  - id: ctx
    use: task.context
  - id: done
    use: task.complete
    with:
      report: "{{ ctx.prompt }}"
`)
	host.notes["p/tasks/o.md"] = taskNote("obiettivo", "capo", "working", "")
	host.notes["p/tasks/k1.md"] = taskNote("uno", "dev", "completed", "parent: \"[[p/tasks/o]]\"\n") + "\n## Report\n\nUno fatto.\n"
	host.notes["p/tasks/k2.md"] = taskNote("due", "dev", "working", "parent: \"[[p/tasks/o]]\"\nclaimed_by: someone\n")
	e.TickTasks()
	if o := host.task(t, "p/tasks/o.md"); o.ClaimedBy != "" || o.Rounds != 0 {
		t.Fatalf("o woke with a sub-task open: %+v", o)
	}
	// k2 closes with a report that has headings of its own, as a
	// reviewer's: the coordinator reads all of it (the fitness pilot,
	// 2026-10-25, read such reports as empty).
	host.mu.Lock()
	host.notes["p/tasks/k2.md"] = string(fleet.AppendSection([]byte(strings.Replace(host.notes["p/tasks/k2.md"], "status: working", "status: completed", 1)),
		"Report", "# Revisione: problemi aperti\n\n## Errori\n\nDue errori nelle note."))
	host.mu.Unlock()
	o := waitTask(t, e, host, "p/tasks/o.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "o completed")
	c := host.note(o.Path)
	if o.Rounds != 1 || !strings.Contains(c, "### uno (dev, completed)\n\nUno fatto.") || !strings.Contains(c, "Due errori nelle note.") ||
		!strings.Contains(c, "You coordinate. The roles you give tasks to:\n- dev (claude.run, no repository nor host: it sees only the vault and writes nothing)") ||
		!strings.Contains(c, "Give the work on a repository only to a role that has it") {
		t.Fatalf("o: %+v\n%s", o, c)
	}
}

func TestTaskTrigger_ACoordinatorThatDoesNothingCallsAPerson(t *testing.T) {
	e, host := taskEngine(t)
	host.notes["p/pipelines/coordina.md"] = pipelineNote("trigger: {on: task, roles: [capo], events: [children_done]}\nnodes:\n  - id: w\n    use: starlark\n    with:\n      code: output = 1\n")
	host.notes["p/tasks/o.md"] = taskNote("obiettivo", "capo", "working", "")
	host.notes["p/tasks/k1.md"] = taskNote("uno", "dev", "failed", "parent: \"[[p/tasks/o]]\"\n")
	o := waitTask(t, e, host, "p/tasks/o.md", func(x fleet.Task) bool { return x.Status == fleet.InputRequired }, "o to a person")
	if o.Rounds != 1 || !strings.Contains(host.note(o.Path), "neither closed the objective nor gave it new tasks") {
		t.Fatalf("o: %+v\n%s", o, host.note(o.Path))
	}
}

func TestTaskTrigger_MaxRoundsCallsAPerson(t *testing.T) {
	e, host := taskEngine(t)
	host.notes["p/pipelines/coordina.md"] = pipelineNote("trigger: {on: task, roles: [capo], events: [children_done]}\nnodes:\n  - id: w\n    use: wait.call\n")
	host.notes["p/tasks/o.md"] = taskNote("obiettivo", "capo", "working", "rounds: 5\n")
	host.notes["p/tasks/k1.md"] = taskNote("uno", "dev", "completed", "parent: \"[[p/tasks/o]]\"\n")
	e.TickTasks()
	o := host.task(t, "p/tasks/o.md")
	if o.Status != fleet.InputRequired || !strings.Contains(host.note(o.Path), "ran 5 rounds") {
		t.Fatalf("o: %+v\n%s", o, host.note(o.Path))
	}
}

func TestTaskNodes_OnlyTheHolderMovesATask(t *testing.T) {
	e, host := taskEngine(t)
	host.notes["p/tasks/a.md"] = taskNote("pagina", "dev", "working", "claimed_by: run-other\n")
	host.notes["p/pipelines/mano.md"] = pipelineNote("trigger: {on: manual}\nnodes:\n  - id: done\n    use: task.complete\n    with: {task: tasks/a.md}\n")
	run, _, err := e.Start(StartRequest{Path: "p/pipelines/mano.md", Trigger: TriggerManual, Actor: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, jobs.Failed)
	if st := stepOf(t, e, run.ID, "done"); !strings.Contains(st.Error, "held by run-other, not by run-") {
		t.Fatalf("error %q", st.Error)
	}
}

func TestTaskTrigger_Lease(t *testing.T) {
	e, host := taskEngine(t)
	past := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	host.notes["p/pipelines/regista.md"] = pipelineNote("trigger: {on: task}\nnodes:\n  - id: w\n    use: wait.call\n")
	host.notes["p/tasks/a.md"] = taskNote("pagina", "dev", "working", "claimed_by: agent@x\nlease_until: "+past+"\nattempts: 1\n")
	e.TickTasks()
	a := host.task(t, "p/tasks/a.md")
	// Given back, then taken again by the pipeline in the same tick.
	if a.Status != fleet.Working || !strings.HasPrefix(a.ClaimedBy, "run-") || a.Attempts != 2 || !strings.Contains(host.note(a.Path), "without a heartbeat") {
		t.Fatalf("a: %+v\n%s", a, host.note(a.Path))
	}
}

func TestParse_TaskTrigger(t *testing.T) {
	s, err := Parse("trigger: {on: task, team: \"[[p/squadra]]\", roles: [dev, test]}\nnodes: [{id: a, use: note.read}]", testTypes)
	if err != nil {
		t.Fatal(err)
	}
	tr := s.Trigger
	if strings.Join(tr.Events, ",") != "ready" || !tr.TakesTask(EventReady, "p/squadra.md", "dev", "capo") || tr.TakesTask(EventReady, "p/altra.md", "dev", "capo") ||
		tr.TakesTask(EventReady, "p/squadra.md", "capo", "capo") || tr.TakesTask(EventBudget, "p/squadra.md", "", "capo") {
		t.Fatalf("trigger %+v", tr)
	}
	// coordinator: true takes the coordinator's tasks only, false the others'.
	for src, want := range map[string][2]bool{
		"trigger: {on: task, coordinator: true}":  {true, false},
		"trigger: {on: task, coordinator: false}": {false, true},
		"trigger: {on: task}":                     {true, true},
	} {
		s, err := Parse(src+"\nnodes: [{id: a, use: note.read}]", testTypes)
		if err != nil {
			t.Fatal(err)
		}
		if got := [2]bool{s.Trigger.TakesTask(EventReady, "p/squadra.md", "capo", "capo"), s.Trigger.TakesTask(EventReady, "p/squadra.md", "dev", "capo")}; got != want {
			t.Errorf("%s: capo, dev = %v, want %v", src, got, want)
		}
	}
	for name, src := range map[string]string{
		"bad event":             "trigger: {on: task, events: [done]}",
		"bad role":              "trigger: {on: task, roles: [Dev]}",
		"team elsewhere":        "trigger: {on: task, team: ../x}",
		"roles on manual":       "trigger: {on: manual, roles: [dev]}",
		"events on commit":      "trigger: {on: commit, events: [ready]}",
		"coordinator on manual": "trigger: {on: manual, coordinator: true}",
	} {
		if _, err := Parse(src+"\nnodes: [{id: a, use: note.read}]", testTypes); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// fakeAgents is a plugin of agents: x.run keeps the agent-run contract.
type fakeAgents struct{}

func (fakeAgents) Known(use string) bool { return use == "x.run" || use == "x.other" }
func (fakeAgents) Type(use string) (Type, bool) {
	return TypeFunc(func(context.Context, *Env, map[string]any) (any, error) { return nil, errors.New("not here") }), true
}
func (fakeAgents) Contract(use string) string {
	if use == "x.run" {
		return AgentContract
	}
	return ""
}

func TestTaskTrigger_AgentsTakeTheirTeamsPlaces(t *testing.T) {
	e, _ := taskEngine(t)
	e.Types().Extend(fakeAgents{})
	run := jobs.Run{Team: "p/squadra.md"}
	got := e.resources(run, &Node{Use: "x.run"}, scope{})
	if strings.Join(got, ",") != "team:p/squadra,agents" {
		t.Fatalf("resources %v", got)
	}
	if got := e.resources(run, &Node{Use: "x.other"}, scope{}); len(got) != 0 {
		t.Fatalf("a node that runs no agent: %v", got)
	}
	if got := e.resources(jobs.Run{}, &Node{Use: "x.run"}, scope{}); len(got) != 0 {
		t.Fatalf("an agent outside a team: %v", got)
	}
	e.TickTasks()
	if e.TeamLimit("p/squadra") != 0 {
		t.Fatal("max_agents not given is no limit")
	}
}

func TestTaskTrigger_AFailedSubTaskCallsTheCoordinatorOnce(t *testing.T) {
	e, host := taskEngine(t)
	// The coordinator, called, has nothing to give: it waits.
	host.notes["p/pipelines/coordina.md"] = pipelineNote(`trigger: {on: task, roles: [capo], events: [children_done]}
nodes:
  - id: apply
    use: task.plan
    with:
      plan: {action: wait}
`)
	kid := func(title, status string) string {
		holder := ""
		if status == "working" {
			holder = "claimed_by: someone\n"
		}
		return taskNote(title, "dev", status, "parent: \"[[p/tasks/o]]\"\n"+holder)
	}
	host.notes["p/tasks/o.md"] = taskNote("obiettivo", "capo", "working", "")
	host.notes["p/tasks/k1.md"] = kid("uno", "failed")
	host.notes["p/tasks/k2.md"] = kid("due", "working")
	o := waitTask(t, e, host, "p/tasks/o.md", func(x fleet.Task) bool { return x.Rounds == 1 && x.ClaimedBy == "" }, "the coordinator ran once")
	if o.Status != fleet.Working {
		t.Fatalf("o after a wait: %+v\n%s", o, host.note(o.Path))
	}
	runs, err := e.Runner().Store().Runs(jobs.RunFilter{Ref: "p/pipelines/coordina.md"})
	if err != nil || len(runs) != 1 || runs[0].Status != jobs.Done {
		t.Fatalf("the coordinator's runs: %+v %v", runs, err)
	}
	// Another sub-task starts, then one closes: the failed one does not
	// call the coordinator again (the fitness pilot's third run burnt four
	// rounds so).
	host.mu.Lock()
	host.notes["p/tasks/k3.md"] = kid("tre", "working")
	host.mu.Unlock()
	e.TickTasks()
	host.mu.Lock()
	host.notes["p/tasks/k2.md"] = strings.Replace(host.notes["p/tasks/k2.md"], "status: working", "status: completed", 1)
	host.mu.Unlock()
	e.TickTasks()
	e.TickTasks()
	if o := host.task(t, "p/tasks/o.md"); o.Rounds != 1 {
		t.Fatalf("the coordinator ran again for the same failed sub-task: %+v", o)
	}
	// All closed: it runs again.
	host.mu.Lock()
	host.notes["p/tasks/k3.md"] = strings.Replace(host.notes["p/tasks/k3.md"], "status: working", "status: completed", 1)
	host.mu.Unlock()
	waitTask(t, e, host, "p/tasks/o.md", func(x fleet.Task) bool { return x.Rounds == 2 }, "the coordinator ran when all closed")
}
