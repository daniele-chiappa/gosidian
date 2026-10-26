package automation

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/jobs"
)

// syncHost is a fakeHost the runner's goroutines may call: its writes are
// counted under a lock, can fail, and can wait for a gate.
type syncHost struct {
	*fakeHost
	mu   sync.Mutex
	fail error
	gate chan struct{}
	n    int
}

func (h *syncHost) Handoff(db Database, r Rule, summary string, items []string, now time.Time) (string, error) {
	if h.gate != nil {
		<-h.gate
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.n++
	if h.fail != nil {
		return "", h.fail
	}
	return h.fakeHost.Handoff(db, r, summary, items, now)
}

func (h *syncHost) calls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.n
}

// newRunnerEngine is an engine whose actions run on a started job runner,
// with tries a few milliseconds apart.
func newRunnerEngine(t *testing.T, h Host, now *time.Time) (*Engine, *jobs.Runner) {
	t.Helper()
	old := actionBackoff
	actionBackoff = 10 * time.Millisecond
	t.Cleanup(func() { actionBackoff = old })
	rome, _ := time.LoadLocation("Europe/Rome")
	e, err := New(h, NewStore(filepath.Join(t.TempDir(), "automations.json")), rome, nil)
	if err != nil {
		t.Fatal(err)
	}
	e.SetClock(func() time.Time { return *now })
	st, err := jobs.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	r := jobs.NewRunner(st, jobs.Options{Workers: 2, Poll: 10 * time.Millisecond, Lease: time.Minute})
	e.SetRunner(r)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { r.Start(ctx); close(stopped) }()
	t.Cleanup(func() { cancel(); <-stopped; st.Close() })
	return e, r
}

func pending(e *Engine) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, rs := range e.state.Rules {
		n += len(rs.Pending)
	}
	return n
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("never: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A rule's action runs on the job runner as a run of its own (IMP-166):
// the tick queues it, the runner writes the handoff, the state records it
// as fired, and the next tick fires nothing.
func TestRunner_TheActionRunsOnTheRunner(t *testing.T) {
	rome, _ := time.LoadLocation("Europe/Rome")
	now := time.Date(2026, 10, 5, 8, 0, 0, 0, rome)
	h := &syncHost{fakeHost: demoHost(t)}
	e, r := newRunnerEngine(t, h, &now)

	runs := e.Tick("")
	if len(runs) != 1 || runs[0].Job == "" || len(runs[0].Rows) != 3 || runs[0].Path != "" {
		t.Fatalf("tick: %+v, want one queued handoff of 3 rows", runs)
	}
	waitFor(t, func() bool { return pending(e) == 0 }, "the run ended and was recorded")
	if h.calls() != 1 {
		t.Errorf("handoffs written: %d, want 1", h.calls())
	}
	run, err := r.Store().Run(runs[0].Job)
	if err != nil || run.Kind != Kind || run.Status != jobs.Done || run.Ref != "demo/tasks.md" || run.Project != "demo" {
		t.Errorf("the action's run: %+v, %v", run, err)
	}
	e.mu.Lock()
	last := e.state.Log[len(e.state.Log)-1]
	e.mu.Unlock()
	if last.Job != runs[0].Job || last.Path != "demo/handoffs/h1.md" || len(last.Rows) != 3 || last.Error != "" {
		t.Errorf("the log's last run: %+v", last)
	}
	if again := e.Tick(""); len(again) != 0 || h.calls() != 1 {
		t.Errorf("second tick: %+v, %d handoffs; want nothing more", again, h.calls())
	}
}

// While an action is queued or running, the ticks do not fire it again;
// Await answers with the note it wrote.
func TestRunner_NoSecondActionWhilePending(t *testing.T) {
	rome, _ := time.LoadLocation("Europe/Rome")
	now := time.Date(2026, 10, 5, 8, 0, 0, 0, rome)
	h := &syncHost{fakeHost: demoHost(t), gate: make(chan struct{})}
	e, _ := newRunnerEngine(t, h, &now)

	runs := e.Tick("")
	if len(runs) != 1 {
		t.Fatalf("tick: %+v", runs)
	}
	for i := 0; i < 3; i++ {
		if again := e.Tick(""); len(again) != 0 {
			t.Fatalf("tick %d while pending fired %+v", i, again)
		}
	}
	plan, err := e.Plan("demo", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	for _, pr := range plan.Rules {
		if pr.Name == "Deadlines near" && (pr.Fires || len(pr.Fired) != 3) {
			t.Errorf("plan while pending: fires %v, fired %d; want the queued rows as done", pr.Fires, len(pr.Fired))
		}
	}
	close(h.gate)
	got := e.Await(runs, 5*time.Second)
	if got[0].Path != "demo/handoffs/h1.md" || got[0].Error != "" {
		t.Errorf("Await: %+v", got[0])
	}
	if h.calls() != 1 {
		t.Errorf("handoffs written: %d, want 1", h.calls())
	}
}

// An action that keeps failing is tried actionAttempts times, then leaves
// its error on the rule and its rows free: the next tick queues it again.
func TestRunner_FailureTriesThenFreesTheRule(t *testing.T) {
	rome, _ := time.LoadLocation("Europe/Rome")
	now := time.Date(2026, 10, 5, 8, 0, 0, 0, rome)
	h := &syncHost{fakeHost: demoHost(t), fail: errors.New("disk full")}
	e, _ := newRunnerEngine(t, h, &now)

	got := e.Await(e.Tick(""), 5*time.Second)
	if len(got) != 1 || got[0].Error != "disk full" {
		t.Fatalf("Await: %+v, want the failure", got)
	}
	if h.calls() != actionAttempts {
		t.Errorf("tries: %d, want %d", h.calls(), actionAttempts)
	}
	e.mu.Lock()
	rs := e.state.Rules[Rule{Name: "Deadlines near"}.Key("demo/tasks.md")]
	errMsg, fired, pend := rs.Error, len(rs.Fired), len(rs.Pending)
	e.mu.Unlock()
	if errMsg != "disk full" || fired != 0 || pend != 0 {
		t.Errorf("rule state: error %q, fired %d, pending %d", errMsg, fired, pend)
	}

	h.mu.Lock()
	h.fail = nil
	h.mu.Unlock()
	got = e.Await(e.Tick(""), 5*time.Second)
	if len(got) != 1 || got[0].Path == "" {
		t.Fatalf("the next tick: %+v, want the action queued again and written", got)
	}
	e.mu.Lock()
	errMsg = rs.Error
	e.mu.Unlock()
	if errMsg != "" {
		t.Errorf("error after the action worked: %q", errMsg)
	}
}

// A pending action whose run is gone from the jobs database (a database
// deleted, a restart without it) is freed, and fires again.
func TestRunner_ReconcileFreesARunThatIsGone(t *testing.T) {
	rome, _ := time.LoadLocation("Europe/Rome")
	now := time.Date(2026, 10, 5, 8, 0, 0, 0, rome)
	h := &syncHost{fakeHost: demoHost(t)}
	e, _ := newRunnerEngine(t, h, &now)

	e.mu.Lock()
	rs := e.state.rule(Rule{Name: "Deadlines near"}.Key("demo/tasks.md"), now)
	rs.Pending = map[string]string{"demo/tasks/late.md@2026-10-04": "no-such-run"}
	e.mu.Unlock()
	got := e.Await(e.Tick(""), 5*time.Second)
	if len(got) != 1 || len(got[0].Rows) != 3 || got[0].Path == "" {
		t.Fatalf("tick: %+v, want the freed row handed off with the others", got)
	}
}
