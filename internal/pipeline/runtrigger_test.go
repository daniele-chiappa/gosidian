package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/jobs"
)

func TestParse_RunTrigger(t *testing.T) {
	s, err := Parse("trigger: {on: run}\nnodes:\n  - {id: a, use: starlark, with: {code: \"output = 1\"}}", nil)
	if err != nil || s.Trigger.On != TriggerRun || strings.Join(s.Trigger.Status, ",") != "failed" || s.Trigger.Pipelines != "" {
		t.Fatalf("defaults %+v %v", s, err)
	}
	s, err = Parse("trigger: {on: run, status: [Canceled, ' failed ', done, failed], pipelines: /pipelines/*}\nnodes:\n  - {id: a, use: starlark, with: {code: \"output = 1\"}}", nil)
	if err != nil || strings.Join(s.Trigger.Status, ",") != "canceled,failed,done" || s.Trigger.Pipelines != "pipelines/*" {
		t.Fatalf("%+v %v", s, err)
	}
	for _, bad := range []string{"trigger: {on: run, status: [ended]}", "trigger: {on: run, status: [waiting]}", "trigger: {on: run, pipelines: \"[\"}",
		"trigger: {on: run, pipelines: ../q/*}", "trigger: {on: run, pipelines: \"{{ x }}\"}", "trigger: {on: manual, status: [failed]}",
		"trigger: {on: commit, pipelines: \"*\"}"} {
		if _, err := Parse(bad+"\nnodes:\n  - {id: a, use: starlark, with: {code: \"output = 1\"}}", nil); err == nil {
			t.Errorf("%s: no error", bad)
		}
	}
}

func TestTrigger_TakesRun(t *testing.T) {
	tr := Trigger{On: TriggerRun, Status: []string{"failed"}, Pipelines: "pipelines/*"}
	for _, c := range []struct {
		rel, status string
		want        bool
	}{
		{"pipelines/import.md", "failed", true},
		{"pipelines/import.md", "done", false},
		{"pipelines/sub/import.md", "failed", false},
		{"other/import.md", "failed", false},
	} {
		if got := tr.TakesRun(c.rel, c.status); got != c.want {
			t.Errorf("%s %s: %v", c.rel, c.status, got)
		}
	}
	if !(Trigger{On: TriggerRun, Status: []string{"failed"}, Pipelines: "pipelines/import"}).TakesRun("pipelines/import.md", "failed") {
		t.Error("a glob without .md")
	}
	if !(Trigger{On: TriggerRun, Status: []string{"failed"}}).TakesRun("deep/down/x.md", "failed") {
		t.Error("no glob: all of them")
	}
	if (Trigger{On: TriggerCommit, Status: []string{"failed"}}).TakesRun("x.md", "failed") {
		t.Error("another trigger")
	}
}

func registerFail(e *Engine) {
	e.Types().Register("test.fail", TypeFunc(func(context.Context, *Env, map[string]any) (any, error) {
		return nil, errors.New("down")
	}))
}

func runsOf(t *testing.T, e *Engine, ref string) []jobs.Run {
	t.Helper()
	runs, err := e.Runner().Store().Runs(jobs.RunFilter{Ref: ref})
	if err != nil {
		t.Fatal(err)
	}
	return runs
}

func TestEngine_RunTrigger(t *testing.T) {
	e, host := newEngine(t, Config{})
	registerFail(e)
	host.enabled["q"] = true
	host.notes["p/pipelines/import.md"] = pipelineNote(`
nodes:
  - {id: a, use: test.fail, on_error: continue}
  - {id: b, use: test.fail}
`)
	host.notes["p/pipelines/fine.md"] = pipelineNote("nodes:\n  - {id: a, use: starlark, with: {code: \"output = 1\"}}\n")
	host.notes["p/pipelines/hold.md"] = pipelineNote("nodes:\n  - {id: hold, use: wait.call, with: {timeout: 1h}}\n")
	host.notes["p/notify/failed.md"] = pipelineNote(`
trigger: {on: run, pipelines: "pipelines/*"}
nodes:
  - {id: show, use: starlark, with: {input: "{{ trigger.failed_step }}", code: "output = input"}}
`)
	host.notes["p/notify/ended.md"] = pipelineNote("trigger: {on: run, status: [done, canceled]}\nnodes:\n  - {id: a, use: starlark, with: {code: \"output = 1\"}}\n")
	host.notes["p/notify/elsewhere.md"] = pipelineNote("trigger: {on: run, pipelines: \"elsewhere/*\"}\nnodes:\n  - {id: a, use: starlark, with: {code: \"output = 1\"}}\n")
	host.notes["q/notify/failed.md"] = pipelineNote("trigger: {on: run}\nnodes:\n  - {id: a, use: starlark, with: {code: \"output = 1\"}}\n")

	run, _, err := e.Start(StartRequest{Path: "p/pipelines/import.md", Trigger: TriggerManual, Actor: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	failed := waitStatus(t, e, run.ID, jobs.Failed)
	var started []jobs.Run
	eventually(t, func() bool { started = runsOf(t, e, "p/notify/failed.md"); return len(started) == 1 }, "the run on failed")
	got := started[0]
	if got.Trigger != TriggerRun || got.IdemKey != "run:"+run.ID || got.Actor != Actor("p/pipelines/import.md") {
		t.Fatalf("run %+v", got)
	}
	var in map[string]any
	if err := json.Unmarshal(got.Input, &in); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"run": run.ID, "pipeline": "p/pipelines/import.md", "project": "p", "status": "failed", "error": failed.Error, "failed_step": "b"}
	for k, v := range want {
		if in[k] != v {
			t.Errorf("trigger.%s = %v, want %v", k, in[k], v)
		}
	}
	if len(in) != len(want) || !strings.Contains(failed.Error, "b: down") {
		t.Errorf("trigger %v, the run's error %q", in, failed.Error)
	}
	waitStatus(t, e, got.ID, jobs.Done)
	if out := string(stepOf(t, e, got.ID, "show").Output); out != `"b"` {
		t.Errorf("{{ trigger.failed_step }} %s", out)
	}

	// The same end again (a second Ended) starts nothing more.
	steps, _ := e.Runner().Store().Steps(run.ID)
	e.startOnRun(failed, steps)
	if n := len(runsOf(t, e, "p/notify/failed.md")); n != 1 {
		t.Errorf("%d runs on failed after the same end twice", n)
	}
	for _, ref := range []string{"p/notify/ended.md", "p/notify/elsewhere.md", "q/notify/failed.md"} {
		if runs := runsOf(t, e, ref); len(runs) != 0 {
			t.Errorf("%s started on a failure: %+v", ref, runs)
		}
	}

	// A run that ends well starts the pipeline on done; one canceled
	// (Ended runs within Cancel) the same, with no failed step.
	run2, _, _ := e.Start(StartRequest{Path: "p/pipelines/fine.md", Trigger: TriggerManual})
	waitStatus(t, e, run2.ID, jobs.Done)
	eventually(t, func() bool { return len(runsOf(t, e, "p/notify/ended.md")) == 1 }, "the run on done")
	run3, _, _ := e.Start(StartRequest{Path: "p/pipelines/hold.md", Trigger: TriggerManual})
	waitStatus(t, e, run3.ID, jobs.Waiting)
	if err := e.Runner().Cancel(run3.ID); err != nil {
		t.Fatal(err)
	}
	ended := runsOf(t, e, "p/notify/ended.md")
	var onCancel jobs.Run
	for _, r := range ended {
		if r.IdemKey == "run:"+run3.ID {
			onCancel = r
		}
	}
	if len(ended) != 2 || !strings.Contains(string(onCancel.Input), `"status":"canceled"`) || !strings.Contains(string(onCancel.Input), `"failed_step":""`) {
		t.Fatalf("runs on done and canceled %+v", ended)
	}
	if n := len(runsOf(t, e, "p/notify/failed.md")); n != 1 {
		t.Errorf("%d runs on failed, want the first only", n)
	}

	// A project whose pipelines are off starts none.
	host.mu.Lock()
	host.enabled["p"] = false
	host.mu.Unlock()
	e.startOnRun(jobs.Run{ID: "x", Ref: "p/pipelines/import.md", Project: "p", Trigger: TriggerManual, Status: jobs.Failed}, nil)
	if n := len(runsOf(t, e, "p/notify/failed.md")); n != 1 {
		t.Errorf("pipelines off: %d runs on failed", n)
	}
}

// A run a run trigger started starts none when it ends, nor do the runs it
// starts with pipeline.run; the runs a run started by hand starts do.
func TestEngine_RunTriggerStartsNoLoop(t *testing.T) {
	e, host := newEngine(t, Config{})
	registerFail(e)
	// It takes every end of every pipeline, its own too, and fails.
	host.notes["p/notify.md"] = pipelineNote(`
trigger: {on: run, status: [done, failed, canceled]}
nodes:
  - {id: call, use: pipeline.run, with: {pipeline: helper}}
`)
	host.notes["p/helper.md"] = pipelineNote(`
nodes:
  - {id: deeper, use: pipeline.run, with: {pipeline: broken, wait: false}}
  - {id: a, use: test.fail}
`)
	host.notes["p/broken.md"] = pipelineNote("nodes:\n  - {id: a, use: test.fail}\n")
	run, _, _ := e.Start(StartRequest{Path: "p/broken.md", Trigger: TriggerManual})
	waitStatus(t, e, run.ID, jobs.Failed)
	var notices []jobs.Run
	eventually(t, func() bool { notices = runsOf(t, e, "p/notify.md"); return len(notices) == 1 }, "the notice")
	notice := waitStatus(t, e, notices[0].ID, jobs.Failed)
	helpers := runsOf(t, e, "p/helper.md")
	if len(helpers) != 1 {
		t.Fatalf("helpers %+v", helpers)
	}
	waitStatus(t, e, helpers[0].ID, jobs.Failed)
	var deeper []jobs.Run
	eventually(t, func() bool {
		deeper = childrenOf(t, e, keySpawned+helpers[0].ID+":")
		return len(deeper) == 1 && deeper[0].Status.Terminal()
	}, "the helper's child ended")

	// Their ends start nothing: taken again here, as Ended took them.
	for _, r := range []jobs.Run{notice, helpers[0], deeper[0]} {
		r, _ = e.Runner().Store().Run(r.ID)
		if !e.fromRunTrigger(r) {
			t.Errorf("run %s (%s) is not from the run trigger", r.ID, r.Ref)
		}
		steps, _ := e.Runner().Store().Steps(r.ID)
		e.startOnRun(r, steps)
	}
	time.Sleep(50 * time.Millisecond) // a loop would have started by now
	if n := len(runsOf(t, e, "p/notify.md")); n != 1 {
		t.Fatalf("%d notices, want 1", n)
	}

	// A chain it cannot read to its start counts as from it.
	if !e.fromRunTrigger(jobs.Run{Trigger: TriggerPipeline, IdemKey: keyWaited + "pruned:call"}) {
		t.Error("a child whose parent is gone")
	}

	// A run started by hand, and the one it starts, are not from it.
	if e.fromRunTrigger(jobs.Run{Trigger: TriggerManual}) {
		t.Error("a manual run")
	}
	host.put("p/starter.md", pipelineNote("nodes:\n  - {id: call, use: pipeline.run, with: {pipeline: broken, wait: false}}\n"))
	start, _, _ := e.Start(StartRequest{Path: "p/starter.md", Trigger: TriggerManual})
	waitStatus(t, e, start.ID, jobs.Done)
	kids := childrenOf(t, e, keySpawned+start.ID+":")
	if len(kids) != 1 {
		t.Fatalf("kids %+v", kids)
	}
	waitStatus(t, e, kids[0].ID, jobs.Failed)
	if e.fromRunTrigger(kids[0]) {
		t.Error("the child of a manual run")
	}
	// notify takes both ends: the starter's (done) and its child's (failed).
	eventually(t, func() bool { return len(runsOf(t, e, "p/notify.md")) == 3 }, "the notices of the starter and its child")
}

// What a pipeline on run writes, and the pipelines it starts, starts no
// commit pipeline: a commit run that fails would start it again.
func TestEngine_RunTriggerWritesStartNoCommit(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/pipelines/index.md"] = pipelineNote("trigger: {on: commit}\nnodes:\n  - {id: a, use: starlark, with: {input: \"{{ trigger.files }}\", code: \"output = [f['path'] for f in input]\"}}\n")
	host.notes["p/pipelines/notify.md"] = pipelineNote("trigger: {on: run}\nnodes:\n  - {id: call, use: pipeline.run, with: {pipeline: pipelines/log}}\n")
	host.notes["p/pipelines/log.md"] = pipelineNote("nodes:\n  - {id: a, use: starlark, with: {code: \"output = 1\"}}\n")
	host.writers = map[string]string{
		"p/log/notified.md": Actor("p/pipelines/notify.md"),
		"p/log/logged.md":   Actor("p/pipelines/log.md"),
		"p/docs/by-hand.md": "admin",
	}
	date := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	e.Committed(Commit{SHA: "c1", Date: date, Files: []CommitFile{{Path: "p/log/notified.md", Change: "modified"}, {Path: "p/log/logged.md", Change: "added"}}})
	if runs := runsOf(t, e, "p/pipelines/index.md"); len(runs) != 0 {
		t.Fatalf("the notice's writes started %+v", runs)
	}
	e.Committed(Commit{SHA: "c2", Date: date, Files: []CommitFile{{Path: "p/log/notified.md", Change: "modified"}, {Path: "p/docs/by-hand.md", Change: "modified"}}})
	runs := runsOf(t, e, "p/pipelines/index.md")
	if len(runs) != 1 {
		t.Fatalf("runs %+v", runs)
	}
	waitStatus(t, e, runs[0].ID, jobs.Done)
	if out := string(stepOf(t, e, runs[0].ID, "a").Output); out != `["p/docs/by-hand.md"]` {
		t.Fatalf("files %s", out)
	}
}
