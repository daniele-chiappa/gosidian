package pipeline

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/jobs"
)

func TestEngine_IterationTrigger(t *testing.T) {
	e, host := newEngine(t, Config{MaxInputBytes: 4096})
	show := "nodes:\n  - id: show\n    use: starlark\n    with: {code: \"output = ctx\"}\n"
	host.notes["p/pipelines/retro.md"] = pipelineNote("trigger: {on: iteration}\n" + show)
	host.notes["p/pipelines/other.md"] = pipelineNote("trigger: {on: manual}\n" + show)
	host.notes["q/pipelines/retro.md"] = pipelineNote("trigger: {on: iteration}\n" + show) // q runs no pipelines
	c := IterationClose{Project: "p", Iteration: "p/iterations/1.md", Board: "p/board.md", Goal: "Il primo mese",
		Start: "2026-11-09", End: "2026-11-22", Review: "## Revisione\n", Deferred: []string{"p/tasks/b.md"}}
	e.IterationClosed(c)
	e.IterationClosed(IterationClose{Project: "q", Iteration: "q/iterations/1.md", End: "2026-11-22"})
	runs, err := e.Runner().Store().Runs(jobs.RunFilter{})
	if err != nil || len(runs) != 1 || runs[0].Ref != "p/pipelines/retro.md" || runs[0].Trigger != TriggerIteration ||
		runs[0].Actor != "iteration" || runs[0].IdemKey != "iteration:p/iterations/1.md:2026-11-22" {
		t.Fatalf("runs %+v %v", runs, err)
	}
	var in map[string]any
	if err := json.Unmarshal(runs[0].Input, &in); err != nil {
		t.Fatal(err)
	}
	if in["event"] != EventClosed || in["iteration"] != c.Iteration || in["board"] != c.Board || in["review"] != c.Review ||
		in["end"] != c.End || len(in["deferred"].([]any)) != 1 {
		t.Errorf("input %v", in)
	}
	waitStatus(t, e, runs[0].ID, jobs.Done)
	// The same iteration again starts nothing; a review too long for the
	// input is left out.
	e.IterationClosed(c)
	c.Iteration, c.Review = "p/iterations/2.md", strings.Repeat("x", 5000)
	e.IterationClosed(c)
	runs, _ = e.Runner().Store().Runs(jobs.RunFilter{})
	if len(runs) != 2 {
		t.Fatalf("%d runs", len(runs))
	}
	for _, r := range runs {
		if r.IdemKey == "iteration:p/iterations/2.md:2026-11-22" && strings.Contains(string(r.Input), `"review"`) {
			t.Errorf("a long review in the input: %d bytes", len(r.Input))
		}
	}
}

func TestParse_IterationTrigger(t *testing.T) {
	s, err := Parse("trigger: {on: iteration}\nnodes:\n  - {id: a, use: starlark, with: {code: \"output = 1\"}}\n", nil)
	if err != nil || s.Trigger.On != TriggerIteration {
		t.Fatalf("%+v %v", s, err)
	}
	if _, err := Parse("trigger: {on: iteration, team: squadra}\nnodes:\n  - {id: a, use: starlark, with: {code: \"output = 1\"}}\n", nil); err == nil {
		t.Fatal("team on an iteration trigger")
	}
}
