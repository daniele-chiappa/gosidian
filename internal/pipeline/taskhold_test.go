package pipeline

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/fleet"
	"github.com/gosidian/gosidian/internal/jobs"
)

func TestHoldDelay(t *testing.T) {
	for n, want := range map[int]time.Duration{0: time.Minute, 1: 2 * time.Minute, 5: 32 * time.Minute, 6: time.Hour, 40: time.Hour} {
		if got := holdDelay(n, 0); got != want {
			t.Errorf("holdDelay(%d) = %s, want %s", n, got, want)
		}
	}
	if got := holdDelay(9, 10*time.Minute); got != 10*time.Minute {
		t.Errorf("holdDelay(9, 10m) = %s", got)
	}
}

// A provider's limit holds the agent's step: the task keeps its attempt,
// what the agent spent before counts, and the team's alert hears once.
func TestTaskFlow_AHeldAgentKeepsItsAttemptAndTellsOnce(t *testing.T) {
	e, host, c := flowEngine(t)
	host.notes["p/pipelines/squadra-alert.md"] = string(fleet.AlertPipeline("p/squadra.md"))
	c.answers = []map[string]any{
		{"hold": "plugin x, run: the agent failed: 403: key limit exceeded",
			"spent": map[string]any{"cost": 0.2, "usage": map[string]any{"total_tokens": 1000}}},
		{"hold": "plugin x, run: the agent failed: 403: key limit exceeded"},
		{"text": "Wrote the note.", "cost": 0.1, "wrote": []any{"p/kb/nota.md"}},
	}
	host.notes["p/tasks/a.md"] = taskNote("nota", "doc", "submitted", "")
	eventually(t, func() bool {
		e.TickTasks()
		return strings.Contains(e.TaskWaits("p")["p/tasks/a.md"], "its run is held and tries again at")
	}, "the held run shows in the task's waits")
	if a := host.task(t, "p/tasks/a.md"); a.Status != fleet.Working || !strings.Contains(e.TaskWaits("p")[a.Path], "403: key limit exceeded") {
		t.Fatalf("a while held: %+v, waits %q", a, e.TaskWaits("p")[a.Path])
	}
	a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "a completed")
	if a.Attempts != 1 || a.CostUSD != 0.3 || a.Tokens != 1000 {
		t.Fatalf("a %+v\n%s", a, host.note(a.Path))
	}
	if s, err := e.TeamSpend("p", "p/squadra.md"); err != nil || math.Abs(s.CostUSD-0.3) > 1e-9 {
		t.Fatalf("the team's spend %+v %v", s, err)
	}
	work, err := e.Runner().Store().Runs(jobs.RunFilter{Ref: "p/pipelines/squadra-work.md"})
	if err != nil || len(work) != 1 {
		t.Fatalf("the task's runs: %d %v", len(work), err)
	}
	steps, err := e.Runner().Store().Steps(work[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range steps {
		if st.Name == "agent" && (st.Holds != 2 || st.Attempt != 1 || st.Status != jobs.Done) {
			t.Fatalf("the agent's step: %+v", st)
		}
	}
	runs, err := e.Runner().Store().Runs(jobs.RunFilter{Ref: "p/pipelines/squadra-alert.md"})
	if err != nil || len(runs) != 1 {
		t.Fatalf("the alert's runs: %d %v", len(runs), err)
	}
	waitStatus(t, e, runs[0].ID, jobs.Done)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.notify) != 1 || !strings.Contains(render(c.notify[0]["text"]), "tries again by itself") || !strings.Contains(render(c.notify[0]["title"]), "waits") {
		t.Fatalf("the alert heard %+v", c.notify)
	}
}

// Sub-tasks behind one that failed never start: once the coordinator had
// its say, a person decides, and the objective does not stay at work.
func TestTaskTrigger_AnObjectiveWhoseSubTasksCannotStartCallsAPerson(t *testing.T) {
	e, host := taskEngine(t)
	host.notes["p/pipelines/coordina.md"] = pipelineNote(`trigger: {on: task, roles: [capo], events: [children_done]}
nodes:
  - id: apply
    use: task.plan
    with:
      plan: {action: wait}
`)
	kid := func(title, status, extra string) string {
		return taskNote(title, "dev", status, "parent: \"[[p/tasks/o]]\"\n"+extra)
	}
	host.notes["p/tasks/o.md"] = taskNote("obiettivo", "capo", "working", "")
	host.notes["p/tasks/k1.md"] = kid("uno", "failed", "")
	host.notes["p/tasks/k2.md"] = kid("due", "submitted", "depends_on:\n  - \"[[p/tasks/k1]]\"\n")
	host.notes["p/tasks/k3.md"] = kid("tre", "submitted", "depends_on:\n  - \"[[p/tasks/k2]]\"\n")
	o := waitTask(t, e, host, "p/tasks/o.md", func(x fleet.Task) bool { return x.Status == fleet.InputRequired }, "the objective goes to a person")
	if o.Rounds != 1 || !strings.Contains(host.note(o.Path), "None of the 2 open sub-tasks of this objective can start: p/tasks/k2.md waits for p/tasks/k1.md (failed)") {
		t.Fatalf("o %+v\n%s", o, host.note(o.Path))
	}
}

// A sub-task that may still start keeps the objective at work.
func TestStuck(t *testing.T) {
	now := time.Now()
	mk := func(p, status string, deps ...string) fleet.Task {
		return fleet.Task{Path: p, Status: status, DependsOn: deps}
	}
	by := func(ts ...fleet.Task) map[string]fleet.Task {
		m := map[string]fleet.Task{}
		for _, x := range ts {
			m[x.Path] = x
		}
		return m
	}
	a, b, c := mk("a", fleet.Canceled), mk("b", fleet.Submitted, "a"), mk("c", fleet.Submitted)
	if why := stuck([]fleet.Task{a, b}, by(a, b), now); !strings.Contains(why, "b waits for a (canceled)") {
		t.Errorf("behind a canceled task: %q", why)
	}
	if why := stuck([]fleet.Task{a, b, c}, by(a, b, c), now); why != "" {
		t.Errorf("one may start: %q", why)
	}
	w := mk("w", fleet.Working)
	d := mk("d", fleet.Submitted, "w")
	if why := stuck([]fleet.Task{w, d}, by(w, d), now); why != "" {
		t.Errorf("one works: %q", why)
	}
	if why := stuck([]fleet.Task{a}, by(a), now); why != "" {
		t.Errorf("all closed: %q", why)
	}
}
