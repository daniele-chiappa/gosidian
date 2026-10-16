package pipeline

import (
	"context"
	"encoding/json"
	"path"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/jobs"
)

// pipeline.run (IMP-186): a parent waits for its child or does not, the
// child's failure is the step's, a chain of runs stops, and a parent that
// ends without its child stops it.

const childEcho = `
nodes:
  - {id: say, use: starlark, with: {input: "{{ trigger.word }}", code: "output = input + '!'"}}
`

func childrenOf(t *testing.T, e *Engine, prefix string) []jobs.Run {
	t.Helper()
	runs, err := e.Runner().Store().Runs(jobs.RunFilter{IdemPrefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	return runs
}

func outputMap(t *testing.T, st jobs.Step) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(st.Output, &m); err != nil {
		t.Fatalf("output of %s: %s: %v", st.Name, st.Output, err)
	}
	return m
}

func TestPipelineRunNode_WaitsAndReadsTheChild(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/pipelines/echo.md"] = pipelineNote(childEcho)
	host.notes["p/parent.md"] = pipelineNote(`
nodes:
  - {id: call, use: pipeline.run, with: {pipeline: pipelines/echo, input: {word: hi}}}
  - {id: after, use: starlark, with: {input: "{{ call.outputs.say }}", code: "output = input"}}
`)
	run, _, err := e.Start(StartRequest{Path: "p/parent.md", Trigger: TriggerManual})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, jobs.Done)
	if out := string(stepOf(t, e, run.ID, "after").Output); out != `"hi!"` {
		t.Fatalf("after %s", out)
	}
	call := outputMap(t, stepOf(t, e, run.ID, "call"))
	child, err := e.Runner().Store().Run(call["run"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if child.Trigger != TriggerPipeline || child.Actor != "pipeline:p/parent.md" || child.IdemKey != "parent:"+run.ID+":call" || child.Ref != "p/pipelines/echo.md" {
		t.Fatalf("child %+v", child)
	}
	if call["status"] != "done" || call["steps"].(map[string]any)["say"] != "done" {
		t.Fatalf("call %v", call)
	}
}

// A child that ends after its parent waits resumes it.
func TestPipelineRunNode_ChildEndingLaterResumesTheParent(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/hold.md"] = pipelineNote(`
nodes:
  - {id: hold, use: wait.call, with: {timeout: 1h}}
  - {id: say, use: starlark, with: {input: "{{ hold.word }}", code: "output = input"}}
`)
	host.notes["p/parent.md"] = pipelineNote(`
nodes:
  - {id: call, use: pipeline.run, with: {pipeline: hold}}
`)
	run, _, _ := e.Start(StartRequest{Path: "p/parent.md", Trigger: TriggerManual})
	waitStatus(t, e, run.ID, jobs.Waiting)
	kids := childrenOf(t, e, "parent:"+run.ID+":")
	if len(kids) != 1 {
		t.Fatalf("children %+v", kids)
	}
	waitStatus(t, e, kids[0].ID, jobs.Waiting)
	if err := e.Runner().Resume(kids[0].ID, "", json.RawMessage(`{"word":"later"}`)); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, jobs.Done)
	call := outputMap(t, stepOf(t, e, run.ID, "call"))
	if say := call["outputs"].(map[string]any)["say"]; say != "later" {
		t.Fatalf("call %v", call)
	}
}

// A start that waits goes on while its run waits only for the run it
// started, and gets the answer once both ended (a bot whose pipeline
// starts another, IMP-191).
func TestPipelineRunNode_AStartWaitsThroughTheChild(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/hold.md"] = pipelineNote(`
nodes:
  - {id: hold, use: wait.call, with: {timeout: 1h}}
  - {id: say, use: starlark, with: {input: "{{ hold.word }}", code: "output = input"}}
`)
	host.notes["p/parent.md"] = pipelineNote(`
answer: reply
nodes:
  - {id: call, use: pipeline.run, with: {pipeline: hold}}
  - {id: reply, use: starlark, with: {input: "{{ call.outputs.say }}", code: "output = input"}}
`)
	run, _, err := e.Start(StartRequest{Path: "p/parent.md", Trigger: TriggerManual})
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.Wait(context.Background(), run.ID, 1500*time.Millisecond)
	if err != nil || got.Status != jobs.Waiting || e.Settled(got) {
		t.Fatalf("a run that waits for its child, its time passed: %s settled %v %v", got.Status, e.Settled(got), err)
	}
	kids := childrenOf(t, e, "parent:"+run.ID+":")
	if len(kids) != 1 {
		t.Fatalf("children %+v", kids)
	}
	waitStatus(t, e, kids[0].ID, jobs.Waiting)
	if !e.Settled(waitStatus(t, e, kids[0].ID, jobs.Waiting)) {
		t.Error("the child waits for a call: settled")
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = e.Runner().Resume(kids[0].ID, "", json.RawMessage(`{"word":"ecco"}`))
	}()
	got, err = e.Wait(context.Background(), run.ID, 10*time.Second)
	if err != nil || got.Status != jobs.Done {
		t.Fatalf("after the child: %s %v", got.Status, err)
	}
	steps, _ := e.Runner().Store().Steps(run.ID)
	if a, ok := AnswerOf(got, steps); !ok || a != "ecco" {
		t.Errorf("answer %v %v", a, ok)
	}
}

// The child's failure fails the step, with the child's outcome for the
// nodes that continue on error; ending soon or later alike.
func TestPipelineRunNode_ChildFailureFailsTheStep(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/broken.md"] = pipelineNote(`
nodes:
  - {id: read, use: note.read, with: {path: missing.md}}
`)
	host.notes["p/hold-broken.md"] = pipelineNote(`
nodes:
  - {id: hold, use: wait.call, with: {timeout: 1h}}
  - {id: read, use: note.read, with: {path: missing.md}}
`)
	host.notes["p/strict.md"] = pipelineNote(`
nodes:
  - {id: call, use: pipeline.run, with: {pipeline: broken}}
`)
	host.notes["p/lenient.md"] = pipelineNote(`
nodes:
  - {id: call, use: pipeline.run, with: {pipeline: hold-broken}, on_error: continue}
  - {id: report, use: starlark, with: {input: "{{ call.status }}", code: "output = input"}}
`)
	strict, _, _ := e.Start(StartRequest{Path: "p/strict.md", Trigger: TriggerManual})
	got := waitStatus(t, e, strict.ID, jobs.Failed)
	if !strings.Contains(got.Error, "of p/broken.md failed") {
		t.Errorf("strict error %q", got.Error)
	}

	lenient, _, _ := e.Start(StartRequest{Path: "p/lenient.md", Trigger: TriggerManual})
	waitStatus(t, e, lenient.ID, jobs.Waiting)
	kids := childrenOf(t, e, "parent:"+lenient.ID+":")
	if len(kids) != 1 {
		t.Fatalf("children %+v", kids)
	}
	waitStatus(t, e, kids[0].ID, jobs.Waiting)
	if err := e.Runner().Resume(kids[0].ID, "", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, lenient.ID, jobs.Done)
	if st := stepOf(t, e, lenient.ID, "call"); st.Status != jobs.Failed {
		t.Errorf("call %s", st.Status)
	}
	if out := string(stepOf(t, e, lenient.ID, "report").Output); out != `"failed"` {
		t.Errorf("report %s", out)
	}
}

// Without wait, one run per element of an each; with wait, an each is
// refused before any run starts.
func TestPipelineRunNode_EachStartsRunsWithoutWaiting(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/echo.md"] = pipelineNote(childEcho)
	host.notes["p/fan.md"] = pipelineNote(`
nodes:
  - {id: list, use: starlark, with: {code: "output = ['a', 'b', 'c']"}}
  - {id: fan, use: pipeline.run, each: "{{ list }}", with: {pipeline: echo, input: {word: "{{ item }}"}, wait: false}}
`)
	host.notes["p/fan-wait.md"] = pipelineNote(`
nodes:
  - {id: list, use: starlark, with: {code: "output = ['a']"}}
  - {id: fan, use: pipeline.run, each: "{{ list }}", with: {pipeline: echo, input: {word: "{{ item }}"}}}
`)
	run, _, _ := e.Start(StartRequest{Path: "p/fan.md", Trigger: TriggerManual})
	waitStatus(t, e, run.ID, jobs.Done)
	eventually(t, func() bool {
		kids := childrenOf(t, e, "spawned:"+run.ID+":fan:")
		done := 0
		for _, k := range kids {
			if k.Status == jobs.Done {
				done++
			}
		}
		return done == 3
	}, "three children done")
	var words, keys []string
	for _, k := range childrenOf(t, e, "spawned:"+run.ID+":fan:") {
		keys = append(keys, k.IdemKey)
		var out string
		_ = json.Unmarshal(stepOf(t, e, k.ID, "say").Output, &out)
		words = append(words, out)
	}
	sort.Strings(words)
	sort.Strings(keys)
	if strings.Join(words, " ") != "a! b! c!" || path.Base(strings.ReplaceAll(keys[2], ":", "/")) != "2" {
		t.Fatalf("words %v keys %v", words, keys)
	}

	refused, _, _ := e.Start(StartRequest{Path: "p/fan-wait.md", Trigger: TriggerManual})
	got := waitStatus(t, e, refused.ID, jobs.Failed)
	if !strings.Contains(got.Error, "wait: false") {
		t.Errorf("error %q", got.Error)
	}
	if kids := childrenOf(t, e, "parent:"+refused.ID+":"); len(kids) != 0 {
		t.Errorf("a waited run started in an each: %+v", kids)
	}
}

// A pipeline that starts itself stops after max_run_depth runs below the
// first.
func TestPipelineRunNode_AChainStops(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/loop.md"] = pipelineNote(`
nodes:
  - {id: again, use: pipeline.run, with: {pipeline: loop, wait: false}}
`)
	if _, _, err := e.Start(StartRequest{Path: "p/loop.md", Trigger: TriggerManual}); err != nil {
		t.Fatal(err)
	}
	var runs []jobs.Run
	eventually(t, func() bool {
		runs, _ = e.Runner().Store().Runs(jobs.RunFilter{Ref: "p/loop.md"})
		ended := 0
		for _, r := range runs {
			if r.Status.Terminal() {
				ended++
			}
		}
		return len(runs) == e.cfg.MaxRunDepth+1 && ended == len(runs)
	}, "the chain ended")
	time.Sleep(50 * time.Millisecond) // a sixth would have started by now
	runs, _ = e.Runner().Store().Runs(jobs.RunFilter{Ref: "p/loop.md"})
	failed := 0
	for _, r := range runs {
		if r.Status == jobs.Failed {
			failed++
			if !strings.Contains(r.Error, "may start none") {
				t.Errorf("error %q", r.Error)
			}
		}
	}
	if len(runs) != e.cfg.MaxRunDepth+1 || failed != 1 {
		t.Fatalf("%d runs, %d failed", len(runs), failed)
	}
}

// A parent canceled while it waits cancels its child.
func TestPipelineRunNode_CanceledParentStopsTheChild(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.notes["p/hold.md"] = pipelineNote(`
nodes:
  - {id: hold, use: wait.call, with: {timeout: 1h}}
`)
	host.notes["p/parent.md"] = pipelineNote(`
nodes:
  - {id: call, use: pipeline.run, with: {pipeline: hold}}
`)
	run, _, _ := e.Start(StartRequest{Path: "p/parent.md", Trigger: TriggerManual})
	waitStatus(t, e, run.ID, jobs.Waiting)
	kids := childrenOf(t, e, "parent:"+run.ID+":")
	if len(kids) != 1 {
		t.Fatalf("children %+v", kids)
	}
	if err := e.Runner().Cancel(run.ID); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, kids[0].ID, jobs.Canceled)
}

func TestPipelineRunNode_Refusals(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.enabled["q"] = true
	host.notes["q/echo.md"] = pipelineNote(childEcho)
	for name, with := range map[string]string{
		"outside": `{pipeline: ../q/echo}`,
		"missing": `{pipeline: nowhere}`,
		"input":   `{pipeline: q/echo, input: "a text"}`,
		"timeout": `{pipeline: q/echo, timeout: soon}`,
	} {
		host.notes["p/"+name+".md"] = pipelineNote("nodes:\n  - {id: call, use: pipeline.run, with: " + with + "}\n")
		run, _, err := e.Start(StartRequest{Path: "p/" + name + ".md", Trigger: TriggerManual})
		if err != nil {
			t.Fatal(err)
		}
		if got := waitStatus(t, e, run.ID, jobs.Failed); got.Error == "" {
			t.Errorf("%s: no error", name)
		}
	}
	if runs, _ := e.Runner().Store().Runs(jobs.RunFilter{Ref: "q/echo.md"}); len(runs) != 0 {
		t.Errorf("a pipeline of another project ran: %+v", runs)
	}
}

// The key of a child names the run it resumes: nobody else may give one,
// and a child of another project resumes nothing.
func TestPipelineRunNode_OnlyTheEngineKeysAChild(t *testing.T) {
	e, host := newEngine(t, Config{})
	host.enabled["q"] = true
	host.notes["p/hold.md"] = pipelineNote(`
nodes:
  - {id: hold, use: wait.call, with: {timeout: 1h}}
`)
	host.notes["q/echo.md"] = pipelineNote(childEcho)
	run, _, err := e.Start(StartRequest{Path: "p/hold.md", Trigger: TriggerManual})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, jobs.Waiting)
	for _, trig := range []string{TriggerManual, TriggerExternal} {
		for _, key := range []string{"parent:" + run.ID + ":hold", "spawned:" + run.ID + ":hold"} {
			if _, _, err := e.Start(StartRequest{Path: "q/echo.md", Trigger: trig, IdemKey: key}); err == nil {
				t.Errorf("%s started with the key %s", trig, key)
			}
		}
	}
	other, _, err := e.Start(StartRequest{Path: "q/echo.md", Trigger: TriggerPipeline, IdemKey: "parent:" + run.ID + ":hold", Input: map[string]any{"word": "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, other.ID, jobs.Done)
	time.Sleep(100 * time.Millisecond)
	if st := stepOf(t, e, run.ID, "hold"); st.Status != jobs.Waiting {
		t.Fatalf("a run of another project resumed the step: %+v", st)
	}
}

// A run starts at most max_run_children runs: the element past it fails
// the step, and the children it started stay.
func TestPipelineRunNode_ChildrenCap(t *testing.T) {
	e, host := newEngine(t, Config{MaxRunChildren: 2, MaxRunDepth: 6})
	if e.cfg.MaxRunDepth != 6 {
		t.Fatalf("depth %d", e.cfg.MaxRunDepth)
	}
	host.notes["p/echo.md"] = pipelineNote(childEcho)
	host.notes["p/fan.md"] = pipelineNote(`
nodes:
  - {id: list, use: starlark, with: {code: "output = ['a', 'b', 'c']"}}
  - {id: fan, use: pipeline.run, each: "{{ list }}", with: {pipeline: echo, input: {word: "{{ item }}"}, wait: false}}
`)
	run, _, _ := e.Start(StartRequest{Path: "p/fan.md", Trigger: TriggerManual})
	got := waitStatus(t, e, run.ID, jobs.Failed)
	if !strings.Contains(got.Error, "max_run_children") {
		t.Errorf("error %q", got.Error)
	}
	if kids := childrenOf(t, e, "spawned:"+run.ID+":"); len(kids) != 2 {
		t.Errorf("%d children, want 2", len(kids))
	}
}
