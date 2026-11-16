package pipeline

import (
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/fleet"
)

// A task's budget reaches the agent within what its executor's preset
// lets a step spend; a preset that lets it spend nothing gets no max_cost.
func TestTaskFlow_TheBudgetStaysWithinThePresets(t *testing.T) {
	e, host, c := flowEngine(t)
	most := 2.0
	c.most = &most
	c.answers = []map[string]any{{"text": "Wrote the note.", "wrote": []any{"p/kb/nota.md"}}}
	host.notes["p/tasks/a.md"] = taskNote("nota", "doc", "submitted", "budget_usd: 50\n")
	host.notes["p/tasks/b.md"] = taskNote("altra", "doc", "submitted", "budget_usd: 0.5\ndepends_on:\n  - \"[[p/tasks/a]]\"\n")
	waitTask(t, e, host, "p/tasks/b.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "b completed")
	c.mu.Lock()
	if a, b := c.asked[0]["max_cost"], c.asked[1]["max_cost"]; a != 2.0 || b != 0.5 {
		t.Fatalf("max_cost: %v (a budget of 50, a preset of 2), %v (a budget of 0.5)", a, b)
	}
	most = 0
	c.mu.Unlock()
	host.mu.Lock()
	host.notes["p/tasks/c.md"] = taskNote("terza", "doc", "submitted", "budget_usd: 1\n")
	host.mu.Unlock()
	waitTask(t, e, host, "p/tasks/c.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "c completed")
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.asked[2]["max_cost"]; ok {
		t.Fatalf("a preset that lets a step spend nothing got max_cost %v", v)
	}
}

// A coordinator's agent gives a task no more than the team's task_usd.
func TestTaskFlow_APlansBudgetStaysWithinTheTeams(t *testing.T) {
	e, host, c := flowEngine(t)
	host.notes["p/squadra.md"] = strings.Replace(flowTeam, "coordinator: capo\n", "coordinator: capo\nbudget: {day_usd: 10, task_usd: 2}\n", 1)
	c.answers = []map[string]any{
		{"json": map[string]any{"action": "tasks", "tasks": []any{
			map[string]any{"key": "big", "assignee": "doc", "title": "La nota", "body": "Scrivi.", "budget_usd": 100.0},
			map[string]any{"key": "small", "assignee": "doc", "title": "La seconda", "body": "Scrivi.", "budget_usd": 0.5},
		}}},
		{"text": "Fatto.", "wrote": []any{"p/kb/nota.md"}},
		{"text": "Fatto.", "wrote": []any{"p/kb/nota.md"}},
		{"json": map[string]any{"action": "done", "summary": "Fatto."}},
	}
	host.notes["p/tasks/o.md"] = taskNote("obiettivo", "capo", "submitted", "")
	waitTask(t, e, host, "p/tasks/o.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "o completed")
	tasks, _ := host.Tasks("p")
	got := map[string]float64{}
	for _, x := range tasks {
		got[x.Title] = x.BudgetUSD
	}
	if got["La nota"] != 2 || got["La seconda"] != 0.5 {
		t.Fatalf("budgets %v", got)
	}
}

// The note says what the attempt that closed the task spent, and what
// each one that failed did, apart from the task's total.
func TestTaskFlow_EachAttemptSaysWhatItSpent(t *testing.T) {
	e, host, c := flowEngine(t)
	c.answers = []map[string]any{
		{"fail": "the agent stopped at max_tokens", "spent": map[string]any{"cost": 0.5, "usage": map[string]any{"total_tokens": 3000}}},
		{"text": "Wrote the note.", "cost": 0.1, "usage": map[string]any{"total_tokens": 200}, "wrote": []any{"p/kb/nota.md"}},
	}
	host.notes["p/tasks/a.md"] = taskNote("nota", "doc", "submitted", "")
	a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "a completed")
	note := host.note(a.Path)
	if !strings.Contains(note, "This attempt spent 0.5 USD, 3000 tokens.") ||
		!strings.Contains(note, "attempt 2: this attempt 0.1 USD, 200 tokens; the task in all, with the attempts before, 0.6 USD, 3200 tokens.") {
		t.Fatal(note)
	}
}
