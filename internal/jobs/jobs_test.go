package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeExec runs each step with the function named like it, and queues a
// pending step once its dependencies are done; a failed or canceled
// dependency cancels it.
type fakeExec struct {
	fns   map[string]func(ctx context.Context, st Step) Outcome
	mu    sync.Mutex
	ended []Run
}

func (f *fakeExec) Step(ctx context.Context, run Run, st Step, _ []Step) Outcome {
	fn, ok := f.fns[st.Name]
	if !ok {
		return Outcome{Status: Done, Output: json.RawMessage(`"` + st.Name + `"`)}
	}
	return fn(ctx, st)
}

func (f *fakeExec) Next(_ Run, steps []Step) ([]StepChange, *RunChange) {
	byName := map[string]Step{}
	for _, st := range steps {
		byName[st.Name] = st
	}
	var out []StepChange
	for _, st := range steps {
		if st.Status != Pending {
			continue
		}
		ready, blocked := true, false
		for _, d := range st.Deps {
			switch byName[d].Status {
			case Done, Skipped:
			case Failed, Canceled:
				blocked = true
			default:
				ready = false
			}
		}
		switch {
		case blocked:
			out = append(out, StepChange{Name: st.Name, Status: Canceled, Error: "a step before it failed"})
		case ready:
			out = append(out, StepChange{Name: st.Name, Status: Queued})
		}
	}
	return out, nil
}

func (f *fakeExec) Ended(run Run, _ []Step) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended = append(f.ended, run)
}

// endedCount is how many runs Ended saw, once n of them did or after a
// second: Ended runs right after the run's status is saved.
func (f *fakeExec) endedCount(n int) int {
	deadline := time.Now().Add(time.Second)
	for {
		f.mu.Lock()
		got := len(f.ended)
		f.mu.Unlock()
		if got >= n || time.Now().After(deadline) {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func newRunner(t *testing.T, path string, exec *fakeExec) (*Runner, context.CancelFunc, chan struct{}) {
	t.Helper()
	if path == "" {
		path = filepath.Join(t.TempDir(), "jobs.db")
	}
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRunner(st, Options{Workers: 2, Poll: 10 * time.Millisecond, Lease: time.Minute})
	r.Register("test", exec)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { r.Start(ctx); close(stopped) }()
	t.Cleanup(func() { cancel(); <-stopped; st.Close() })
	return r, cancel, stopped
}

func chain(names ...string) []Step {
	var out []Step
	for i, n := range names {
		st := Step{Name: n}
		if i > 0 {
			st.Deps = []string{names[i-1]}
		}
		out = append(out, st)
	}
	return out
}

func waitRun(t *testing.T, r *Runner, id string, want Status) Run {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		run, err := r.Store().Run(id)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status == want {
			return run
		}
		if time.Now().After(deadline) {
			steps, _ := r.Store().Steps(id)
			t.Fatalf("run %s is %s, want %s; steps %+v", id, run.Status, want, steps)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func stepByName(t *testing.T, r *Runner, run, name string) Step {
	t.Helper()
	steps, err := r.Store().Steps(run)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range steps {
		if st.Name == name {
			return st
		}
	}
	t.Fatalf("no step %q", name)
	return Step{}
}

func TestRunner_RunsTheStepsInOrder(t *testing.T) {
	var order []string
	var mu sync.Mutex
	rec := func(name string) func(context.Context, Step) Outcome {
		return func(context.Context, Step) Outcome {
			mu.Lock()
			order = append(order, name)
			mu.Unlock()
			return Outcome{Status: Done, Output: json.RawMessage(`{"n":"` + name + `"}`)}
		}
	}
	exec := &fakeExec{fns: map[string]func(context.Context, Step) Outcome{"a": rec("a"), "b": rec("b"), "c": rec("c")}}
	r, _, _ := newRunner(t, "", exec)
	run, existing, err := r.Submit(Run{Kind: "test", Ref: "p/x.md", Project: "p"}, chain("a", "b", "c"))
	if err != nil || existing {
		t.Fatal(err, existing)
	}
	got := waitRun(t, r, run.ID, Done)
	if got.Started.IsZero() || got.Finished.IsZero() {
		t.Fatalf("times not stamped: %+v", got)
	}
	if strings.Join(order, ",") != "a,b,c" {
		t.Fatalf("order %v", order)
	}
	if out := stepByName(t, r, run.ID, "c").Output; string(out) != `{"n":"c"}` {
		t.Fatalf("output %s", out)
	}
	if exec.endedCount(1) != 1 {
		t.Fatalf("Ended called %d times", exec.endedCount(1))
	}
}

func TestRunner_SameIdempotencyKeyStartsOnce(t *testing.T) {
	exec := &fakeExec{}
	r, _, _ := newRunner(t, "", exec)
	a, _, err := r.Submit(Run{Kind: "test", Ref: "p/x.md", Project: "p", IdemKey: "doc-1"}, chain("a"))
	if err != nil {
		t.Fatal(err)
	}
	b, existing, err := r.Submit(Run{Kind: "test", Ref: "p/x.md", Project: "p", IdemKey: "doc-1"}, chain("a"))
	if err != nil || !existing || b.ID != a.ID {
		t.Fatalf("second submit: %v existing=%v id %s vs %s", err, existing, b.ID, a.ID)
	}
	// Another pipeline may use the same key.
	c, existing, _ := r.Submit(Run{Kind: "test", Ref: "p/y.md", Project: "p", IdemKey: "doc-1"}, chain("a"))
	if existing || c.ID == a.ID {
		t.Fatal("a key of another pipeline matched")
	}
	runs, _ := r.Store().Runs(RunFilter{Ref: "p/x.md"})
	if len(runs) != 1 {
		t.Fatalf("%d runs of p/x.md", len(runs))
	}
}

func TestRunner_RetriesUntilTheStepWorks(t *testing.T) {
	var calls atomic.Int32
	exec := &fakeExec{fns: map[string]func(context.Context, Step) Outcome{
		"a": func(_ context.Context, st Step) Outcome {
			if calls.Add(1) < 3 {
				return Outcome{Status: Queued, Error: "busy", RetryAt: time.Now()}
			}
			return Outcome{Status: Done}
		},
	}}
	r, _, _ := newRunner(t, "", exec)
	run, _, _ := r.Submit(Run{Kind: "test", Ref: "p/x.md", Project: "p"}, chain("a"))
	waitRun(t, r, run.ID, Done)
	if st := stepByName(t, r, run.ID, "a"); st.Attempt != 3 {
		t.Fatalf("attempt %d, want 3", st.Attempt)
	}
}

func TestRunner_AFailureStopsTheRun(t *testing.T) {
	exec := &fakeExec{fns: map[string]func(context.Context, Step) Outcome{
		"b": func(context.Context, Step) Outcome { return Outcome{Status: Failed, Error: "boom"} },
	}}
	r, _, _ := newRunner(t, "", exec)
	run, _, _ := r.Submit(Run{Kind: "test", Ref: "p/x.md", Project: "p"}, chain("a", "b", "c"))
	got := waitRun(t, r, run.ID, Failed)
	if !strings.Contains(got.Error, "b: boom") {
		t.Fatalf("run error %q", got.Error)
	}
	if st := stepByName(t, r, run.ID, "c"); st.Status != Canceled {
		t.Fatalf("c is %s", st.Status)
	}
	if exec.endedCount(1) != 1 {
		t.Fatalf("Ended called %d times", exec.endedCount(1))
	}
}

func TestRunner_APanicFailsTheStep(t *testing.T) {
	exec := &fakeExec{fns: map[string]func(context.Context, Step) Outcome{
		"a": func(context.Context, Step) Outcome { panic("oops") },
	}}
	r, _, _ := newRunner(t, "", exec)
	run, _, _ := r.Submit(Run{Kind: "test", Ref: "p/x.md", Project: "p"}, chain("a"))
	got := waitRun(t, r, run.ID, Failed)
	if !strings.Contains(got.Error, "panic: oops") {
		t.Fatalf("run error %q", got.Error)
	}
}

func TestRunner_AWaitingStepGoesOnWhenResumed(t *testing.T) {
	exec := &fakeExec{fns: map[string]func(context.Context, Step) Outcome{
		"wait": func(context.Context, Step) Outcome {
			return Outcome{Status: Waiting, WaitUntil: time.Now().Add(time.Hour)}
		},
	}}
	r, _, _ := newRunner(t, "", exec)
	run, _, _ := r.Submit(Run{Kind: "test", Ref: "p/x.md", Project: "p"}, chain("a", "wait", "c"))
	waitRun(t, r, run.ID, Waiting)
	if err := r.Resume(run.ID, "", json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	waitRun(t, r, run.ID, Done)
	if out := stepByName(t, r, run.ID, "wait").Output; string(out) != `{"ok":true}` {
		t.Fatalf("resumed output %s", out)
	}
	if err := r.Resume(run.ID, "", nil); !errors.Is(err, ErrState) {
		t.Fatalf("resume of an ended run: %v", err)
	}
}

func TestRunner_AWaitThatExpiresFails(t *testing.T) {
	exec := &fakeExec{fns: map[string]func(context.Context, Step) Outcome{
		"wait": func(context.Context, Step) Outcome {
			return Outcome{Status: Waiting, WaitUntil: time.Now().Add(30 * time.Millisecond)}
		},
	}}
	r, _, _ := newRunner(t, "", exec)
	run, _, _ := r.Submit(Run{Kind: "test", Ref: "p/x.md", Project: "p"}, chain("wait", "c"))
	got := waitRun(t, r, run.ID, Failed)
	if !strings.Contains(got.Error, "no call resumed it") {
		t.Fatalf("run error %q", got.Error)
	}
}

func TestRunner_CancelStopsARunningStep(t *testing.T) {
	started := make(chan struct{})
	exec := &fakeExec{fns: map[string]func(context.Context, Step) Outcome{
		"slow": func(ctx context.Context, _ Step) Outcome {
			close(started)
			<-ctx.Done()
			return Outcome{Status: Failed, Error: ctx.Err().Error()}
		},
	}}
	r, _, _ := newRunner(t, "", exec)
	run, _, _ := r.Submit(Run{Kind: "test", Ref: "p/x.md", Project: "p"}, chain("slow", "after"))
	<-started
	if err := r.Cancel(run.ID); err != nil {
		t.Fatal(err)
	}
	waitRun(t, r, run.ID, Canceled)
	// The late failure of the canceled step does not overwrite it.
	time.Sleep(50 * time.Millisecond)
	if st := stepByName(t, r, run.ID, "slow"); st.Status != Canceled {
		t.Fatalf("slow is %s", st.Status)
	}
	if st := stepByName(t, r, run.ID, "after"); st.Status != Canceled {
		t.Fatalf("after is %s", st.Status)
	}
	if err := r.Cancel(run.ID); !errors.Is(err, ErrState) {
		t.Fatalf("second cancel: %v", err)
	}
}

func TestRunner_AStepLeftRunningIsTakenBackAfterARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	run, _, err := st.CreateRun(Run{Kind: "test", Ref: "p/x.md", Project: "p"}, []Step{{Name: "a", Status: Queued}})
	if err != nil {
		t.Fatal(err)
	}
	// A process claims the step and dies before it ends.
	if got, err := st.claim(time.Now(), 1, time.Hour); err != nil || len(got) != 1 {
		t.Fatal(err, got)
	}
	st.Close()

	exec := &fakeExec{}
	r, _, _ := newRunner(t, path, exec)
	waitRun(t, r, run.ID, Done)
	if s := stepByName(t, r, run.ID, "a"); s.Attempt != 2 {
		t.Fatalf("attempt %d, want 2", s.Attempt)
	}
}

func TestStore_RunTokens(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	tok, secret, err := st.CreateToken("p/pipelines/a.md", "scanner", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, TokenPrefix) || len(secret) < len(TokenPrefix)+64 {
		t.Fatalf("secret %q", secret)
	}
	got, err := st.CheckToken(secret)
	if err != nil || got.ID != tok.ID || got.Ref != "p/pipelines/a.md" {
		t.Fatalf("check: %v %+v", err, got)
	}
	if _, err := st.CheckToken(secret + "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a wrong secret: %v", err)
	}
	if _, err := st.CheckToken("gosidian_abc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an MCP token: %v", err)
	}
	list, _ := st.Tokens("p/pipelines/a.md")
	if len(list) != 1 || list[0].LastUsed.IsZero() {
		t.Fatalf("list %+v", list)
	}
	if err := st.RevokeToken(tok.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CheckToken(secret); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a revoked token: %v", err)
	}
}

func TestStore_PruneKeepsTheRunsNotEnded(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	old, _, _ := st.CreateRun(Run{Kind: "test", Ref: "p/x.md", Project: "p"}, chain("a"))
	_ = st.apply(old.ID, nil, &RunChange{Status: Done, Finished: time.Now().Add(-48 * time.Hour)})
	live, _, _ := st.CreateRun(Run{Kind: "test", Ref: "p/x.md", Project: "p"}, chain("a"))
	n, err := st.Prune(time.Now().Add(-24 * time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("pruned %d: %v", n, err)
	}
	if _, err := st.Run(old.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("the old run is still there")
	}
	if steps, _ := st.Steps(old.ID); len(steps) != 0 {
		t.Fatal("its steps are still there")
	}
	if _, err := st.Run(live.ID); err != nil {
		t.Fatal("the live run is gone")
	}
}
