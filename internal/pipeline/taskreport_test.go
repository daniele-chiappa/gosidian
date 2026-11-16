package pipeline

import (
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/fleet"
)

// The task's note keeps the whole report of its agent; a task that builds
// on it reads it up to [agents] max_prompt_report, and is told of the cut.
func TestTaskFlow_TheNoteKeepsTheWholeReport(t *testing.T) {
	e, host, c := flowEngine(t)
	long := strings.Repeat("A finding of the review, with its place and its reason. ", 400) + "THE VERDICT AT THE END."
	c.answers = []map[string]any{{"text": long}, {"text": "Read it."}}
	host.notes["p/tasks/a.md"] = taskNote("revisione", "rev", "submitted", "")
	host.notes["p/tasks/b.md"] = taskNote("seguito", "rev", "submitted", "depends_on:\n  - \"[[p/tasks/a]]\"\n")
	a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "a completed")
	if note := host.note(a.Path); !strings.Contains(note, "THE VERDICT AT THE END.") || strings.Contains(note, "The beginning: the whole report") {
		t.Fatalf("the note lost the end of the report (%d bytes of %d)", len(note), len(long))
	}
	waitTask(t, e, host, "p/tasks/b.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "b completed")
	c.mu.Lock()
	defer c.mu.Unlock()
	prompt := render(c.asked[1]["prompt"])
	if !strings.Contains(prompt, "bytes: the whole report is in the note p/tasks/a.md, section Report.)") || strings.Contains(prompt, "THE VERDICT AT THE END.") || len(prompt) < 16<<10 {
		t.Fatalf("b's prompt, %d bytes:\n%s", len(prompt), prompt[max(0, len(prompt)-600):])
	}
}

// A role whose work is its report does not close its task with none, nor
// with one cut at its model's output limit.
func TestTaskFlow_AnEmptyOrCutReportGoesBack(t *testing.T) {
	e, host, c := flowEngine(t)
	c.answers = []map[string]any{{"text": "  "}, {"text": "The review begins and", "stop": "length"}}
	host.notes["p/tasks/a.md"] = taskNote("revisione", "rev", "submitted", "")
	a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Failed }, "a failed after its two attempts")
	note := host.note(a.Path)
	if !strings.Contains(note, "The agent gave no report: its answer is empty") || !strings.Contains(note, "cut at its model's output limit (stop: length)") {
		t.Fatal(note)
	}
}

// For a role judged on its files a cut answer is a fact of the report.
func TestTaskFlow_ACutAnswerOfARoleThatWritesIsSaid(t *testing.T) {
	e, host, c := flowEngine(t)
	c.answers = []map[string]any{{"text": "Wrote the", "stop": "length", "wrote": []any{"p/kb/nota.md"}}}
	host.notes["p/tasks/a.md"] = taskNote("nota", "doc", "submitted", "")
	a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "a completed")
	if note := host.note(a.Path); !strings.Contains(note, "stop: length") || a.Attempts != 1 {
		t.Fatalf("%+v\n%s", a, note)
	}
}

// An attempt out of time had written its notes: the next one, which finds
// them, did the task (the pilot's second half of the catalogue went back
// for writing none).
func TestTaskFlow_NotesOfAnAttemptBeforeCount(t *testing.T) {
	e, host, c := flowEngine(t)
	c.answers = []map[string]any{
		{"fail": "the agent stopped at max_seconds (1800 s)", "wrote": []any{"p/kb/uno.md", "p/kb/due.md"}},
		{"text": "The notes were there: I checked them."},
	}
	host.notes["p/tasks/a.md"] = taskNote("catalogo", "doc", "submitted", "")
	a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "a completed")
	if note := host.note(a.Path); a.Attempts != 2 || !strings.Contains(note, "Notes written by an attempt before this one (2), none by this one: p/kb/due.md, p/kb/uno.md.") {
		t.Fatalf("%+v\n%s", a, note)
	}
	// The run ends a moment after it closed the task.
	eventually(t, func() bool { return len(e.TaskWrites("p/tasks/a.md")) == 0 }, "the closed task forgets its writes")
}

// The agent of the next attempt reads why the preview's node failed.
func TestTaskFlow_TheErrorOfAFailedNodeReachesTheAgent(t *testing.T) {
	e, host, c := flowEngine(t)
	c.noSite = true
	c.answers = []map[string]any{{"text": "Fatto.", "repo": map[string]any{"files": []any{"web/a.html"}, "branch": "agent/x", "pushed": true}}}
	host.notes["p/tasks/a.md"] = taskNote("sito", "web", "submitted", "")
	a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Failed }, "a failed")
	if note := host.note(a.Path); !strings.Contains(note, "its nodes failed.\nStep publish: ") || !strings.Contains(note, "the preview's server does not answer") {
		t.Fatal(note)
	}
}

// A role that reviews ends its report with a verdict the gate counts; one
// without it goes back, and the coordinator is told which problems
// reopen the work.
func TestTaskFlow_AReviewsVerdictIsCounted(t *testing.T) {
	e, host, c := flowEngine(t)
	host.notes["p/squadra.md"] = strings.Replace(strings.Replace(flowTeam, "  rev:\n    run: x.run\n", "  rev:\n    run: x.run\n    review: true\n", 1), "delegates_to: [dev, doc]", "delegates_to: [dev, doc, rev]", 1)
	c.answers = []map[string]any{
		{"text": "It all reads well."},
		{"text": "Two things.\n\n- [minor] week 5: a comma\n- [minor] the title\n\nVERDICT: passed"},
		{"json": map[string]any{"action": "done", "summary": "Fatto."}},
	}
	host.notes["p/tasks/o.md"] = taskNote("obiettivo", "capo", "working", "")
	host.notes["p/tasks/a.md"] = taskNote("revisione", "rev", "submitted", "parent: \"[[p/tasks/o]]\"\n")
	a := waitTask(t, e, host, "p/tasks/a.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "a completed")
	note := host.note(a.Path)
	if a.Attempts != 2 || !strings.Contains(note, "The review has no verdict line") || !strings.Contains(note, "The review: passed, 0 serious and 2 minor problems left open.") {
		t.Fatalf("%+v\n%s", a, note)
	}
	waitTask(t, e, host, "p/tasks/o.md", func(x fleet.Task) bool { return x.Status == fleet.Completed }, "o completed")
	c.mu.Lock()
	defer c.mu.Unlock()
	if p := render(c.asked[0]["prompt"]); !strings.Contains(p, "VERDICT: passed") || !strings.Contains(p, "`[serious]` or `[minor]`") {
		t.Fatalf("the reviewer's prompt:\n%s", p)
	}
	if p := render(c.asked[2]["prompt"]); !strings.Contains(p, "Only the serious ones reopen the work") || !strings.Contains(p, "0 serious and 2 minor problems left open") {
		t.Fatalf("the coordinator's prompt:\n%s", p)
	}
}
