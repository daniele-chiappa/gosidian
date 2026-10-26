package jobs

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGate_TakesAllOrNone(t *testing.T) {
	g := NewGate(func(r string) int { return map[string]int{"a": 1, "b": 2}[r] })
	if !g.TryTake([]string{"a"}) || g.TryTake([]string{"a"}) {
		t.Fatal("a has one place")
	}
	if !g.TryTake([]string{"b", "c"}) {
		t.Fatal("b has a place left, c no limit")
	}
	// a is full: b is not taken either.
	if g.TryTake([]string{"b", "a"}) {
		t.Fatal("a full")
	}
	if got := g.Running(); len(got) != 3 || got[0] != (ResourceUse{"a", 1, 1}) || got[1] != (ResourceUse{"b", 1, 2}) || got[2] != (ResourceUse{"c", 1, 0}) {
		t.Fatalf("running %+v", got)
	}
	if g.Full([]string{"c", "a"}) != "a" || g.Full([]string{"b"}) != "" {
		t.Fatal("full")
	}
	g.Release([]string{"a"})
	if !g.TryTake([]string{"a", "b"}) || g.TryTake([]string{"b"}) {
		t.Fatal("after the release")
	}
	// No gate, no limits.
	var none *Gate
	if !none.TryTake([]string{"a"}) || none.Full([]string{"a"}) != "" || none.Take(context.Background(), []string{"a"}) != nil {
		t.Fatal("a nil gate limits")
	}
	none.Release([]string{"a"})
}

func TestGate_TakeWaitsForARelease(t *testing.T) {
	g := NewGate(func(string) int { return 1 })
	released := make(chan struct{}, 1)
	g.onRelease = func() { released <- struct{}{} }
	g.TryTake([]string{"gpu"})
	done := make(chan error, 1)
	go func() { done <- g.Take(context.Background(), []string{"gpu"}) }()
	select {
	case <-done:
		t.Fatal("took a full resource")
	case <-time.After(50 * time.Millisecond):
	}
	g.Release([]string{"gpu"})
	<-released
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := g.Take(ctx, []string{"gpu"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a full resource until the deadline: %v", err)
	}
	// A new limit counts at once.
	g.SetLimit(func(string) int { return 2 })
	if !g.TryTake([]string{"gpu"}) {
		t.Fatal("the new limit")
	}
}

// TestRunner_AResourceLimit: two steps of a plugin with one place never run
// together, and a step of nothing waits behind neither; the one that waits
// stays in the queue, with when it entered it.
func TestRunner_AResourceLimit(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	var now, most atomic.Int32
	var once sync.Once
	ocr := func(ctx context.Context, _ Step) Outcome {
		n := now.Add(1)
		for {
			m := most.Load()
			if n <= m || most.CompareAndSwap(m, n) {
				break
			}
		}
		defer now.Add(-1)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return Outcome{Status: Done}
	}
	exec := &fakeExec{
		fns: map[string]func(context.Context, Step) Outcome{"ocr": ocr},
		res: map[string][]string{"ocr": {"plugin:ocr", "res:gpu"}},
	}
	gate := NewGate(func(r string) int { return map[string]int{"plugin:ocr": 1}[r] })
	r := NewRunner(st, Options{Workers: 3, Poll: 10 * time.Millisecond, Lease: time.Minute, Gate: gate})
	r.Register("test", exec)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { r.Start(ctx); close(stopped) }()
	t.Cleanup(func() { once.Do(func() { close(release) }); cancel(); <-stopped; st.Close() })

	first, _, _ := r.Submit(Run{Kind: "test", Ref: "a.md", Project: "p"}, chain("ocr"))
	waitRun(t, r, first.ID, Running)
	second, _, _ := r.Submit(Run{Kind: "test", Ref: "b.md", Project: "p"}, chain("ocr"))
	light, _, _ := r.Submit(Run{Kind: "test", Ref: "c.md", Project: "p"}, chain("note"))
	// The light run, queued after the second, ends while the plugin is full.
	waitRun(t, r, light.ID, Done)
	queued := stepByName(t, r, second.ID, "ocr")
	if queued.Status != Queued || queued.QueuedAt.IsZero() || len(queued.Resources) != 2 || queued.Resources[0] != "plugin:ocr" {
		t.Fatalf("the second ocr step: %+v", queued)
	}
	if got := gate.Running(); len(got) != 2 || got[0].Resource != "plugin:ocr" || got[0].Running != 1 || got[0].Limit != 1 {
		t.Fatalf("the gate: %+v", got)
	}
	once.Do(func() { close(release) })
	waitRun(t, r, first.ID, Done)
	waitRun(t, r, second.ID, Done)
	if most.Load() != 1 {
		t.Fatalf("%d ocr steps at once", most.Load())
	}
	// The place goes back right after the step's end is saved.
	for deadline := time.Now().Add(time.Second); len(gate.Running()) > 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("places left taken: %+v", gate.Running())
		}
	}
}

// TestRunner_FairAmongProjects: with the fair claim, a project that queued
// many steps first and one that queued a few after take turns, where the
// queue's order would run all of the first one's before.
func TestRunner_FairAmongProjects(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var order []string
	exec := &fakeExec{fns: map[string]func(context.Context, Step) Outcome{
		"s": func(_ context.Context, s Step) Outcome {
			mu.Lock()
			defer mu.Unlock()
			run, _ := st.Run(s.RunID)
			order = append(order, run.Project)
			return Outcome{Status: Done}
		},
	}}
	var ids []string
	r := NewRunner(st, Options{Workers: 1, Poll: 10 * time.Millisecond, Lease: time.Minute, Fair: true})
	r.Register("test", exec)
	for i := 0; i < 4; i++ {
		run, _, _ := r.Submit(Run{Kind: "test", Ref: "big.md", Project: "big"}, chain("s"))
		ids = append(ids, run.ID)
	}
	for i := 0; i < 2; i++ {
		run, _, _ := r.Submit(Run{Kind: "test", Ref: "small.md", Project: "small"}, chain("s"))
		ids = append(ids, run.ID)
	}
	// A low run goes after the normal ones, whatever their project.
	low, _, _ := r.Submit(Run{Kind: "test", Ref: "low.md", Project: "small", Priority: PriorityLow}, chain("s"))
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { r.Start(ctx); close(stopped) }()
	t.Cleanup(func() { cancel(); <-stopped; st.Close() })
	for _, id := range append(ids, low.ID) {
		waitRun(t, r, id, Done)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"big", "small", "big", "small", "big", "big", "small"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("order %v, want %v", order, want)
	}
}
