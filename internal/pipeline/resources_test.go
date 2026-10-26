package pipeline

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/jobs"
)

// fakeExt is a plugin's node type, fx.node, for the resources of its steps.
type fakeExt struct{}

func (fakeExt) Known(use string) bool { return use == "fx.node" }
func (fakeExt) Type(use string) (Type, bool) {
	if use != "fx.node" {
		return nil, false
	}
	return TypeFunc(func(context.Context, *Env, map[string]any) (any, error) { return map[string]any{"ok": true}, nil }), true
}
func (fakeExt) Resources(use string) []string {
	if use != "fx.node" {
		return nil
	}
	return []string{"plugin:fx", "res:gpu"}
}

// TestEngine_StepResources: a step queued takes the resources of its node
// (IMP-196): an llm node its provider, from an expression too, and the
// provider's shared ones; a plugin's node what the plugin says; the others
// none.
func TestEngine_StepResources(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"model":"m","choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()
	e, host := newEngine(t, Config{Providers: []Provider{
		{Name: "first", BaseURL: srv.URL + "/v1", Model: "m"},
		{Name: "gpu", BaseURL: srv.URL + "/v1", Model: "m", Resources: []string{"gpu"}},
	}})
	e.Types().Extend(fakeExt{})
	host.notes["p/pipelines/res.md"] = pipelineNote(`
nodes:
  - {id: pick, use: starlark, with: {code: "output = 'gpu'"}}
  - {id: ask, use: llm.chat, with: {prompt: hi, provider: "{{ pick }}"}}
  - {id: plain, use: llm.chat, with: {prompt: hi}}
  - {id: plug, use: fx.node}
`)
	run, _, err := e.Start(StartRequest{Path: "p/pipelines/res.md", Trigger: TriggerManual})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, jobs.Done)
	// Every step takes its run's category too: default here (IMP-196).
	for name, want := range map[string][]string{
		"pick":  {"cat:default"},
		"ask":   {"cat:default", "llm:gpu", "res:gpu"},
		"plain": {"cat:default", "llm:first"},
		"plug":  {"cat:default", "plugin:fx", "res:gpu"},
	} {
		if got := stepOf(t, e, run.ID, name).Resources; !slices.Equal(got, want) {
			t.Errorf("%s: resources %v, want %v", name, got, want)
		}
	}
}

// blockExt is a plugin's node type, bx.node, whose calls wait for release.
type blockExt struct{ release chan struct{} }

// gateExt is a blockExt whose release can be armed again (rearm): the
// calls after it wait again.
type gateExt struct {
	mu sync.Mutex
	ch chan struct{}
}

func (g *gateExt) wait() <-chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.ch
}

func (g *gateExt) open() {
	g.mu.Lock()
	defer g.mu.Unlock()
	select {
	case <-g.ch:
	default:
		close(g.ch)
	}
}

func (g *gateExt) rearm() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.ch = make(chan struct{})
}

func (*gateExt) Known(use string) bool { return use == "bx.node" }
func (g *gateExt) Type(use string) (Type, bool) {
	if use != "bx.node" {
		return nil, false
	}
	return TypeFunc(func(ctx context.Context, _ *Env, _ map[string]any) (any, error) {
		select {
		case <-g.wait():
		case <-ctx.Done():
		}
		return map[string]any{"ok": true}, nil
	}), true
}
func (*gateExt) Resources(use string) []string {
	if use != "bx.node" {
		return nil
	}
	return []string{"plugin:bx"}
}

func (blockExt) Known(use string) bool { return use == "bx.node" }
func (b blockExt) Type(use string) (Type, bool) {
	if use != "bx.node" {
		return nil, false
	}
	return TypeFunc(func(ctx context.Context, _ *Env, _ map[string]any) (any, error) {
		select {
		case <-b.release:
		case <-ctx.Done():
		}
		return map[string]any{"ok": true}, nil
	}), true
}
func (blockExt) Resources(use string) []string {
	if use != "bx.node" {
		return nil
	}
	return []string{"plugin:bx"}
}

// TestEngine_QueueAndWaits: the queue by resource, with the places taken
// and the limit, and a queued step's reason: its plugin is full.
func TestEngine_QueueAndWaits(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	gate := jobs.NewGate(func(r string) int { return map[string]int{"plugin:bx": 1}[r] })
	runner := jobs.NewRunner(st, jobs.Options{Workers: 2, InteractiveWorkers: 1, Poll: 10 * time.Millisecond, Lease: time.Minute, Gate: gate})
	host := newFakeHost()
	e := New(host, runner, Config{}, nil, nil)
	host.types = e.Types()
	ext := blockExt{release: make(chan struct{})}
	e.Types().Extend(ext)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { runner.Start(ctx); close(stopped) }()
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(ext.release) }); cancel(); <-stopped; st.Close() })

	host.notes["p/pipelines/b.md"] = pipelineNote("nodes:\n  - {id: call, use: bx.node}\n")
	first, _, _ := e.Start(StartRequest{Path: "p/pipelines/b.md", Trigger: TriggerManual})
	waitStatus(t, e, first.ID, jobs.Running)
	second, _, _ := e.Start(StartRequest{Path: "p/pipelines/b.md", Trigger: TriggerManual})
	eventually(t, func() bool { return stepOf(t, e, second.ID, "call").Status == jobs.Queued }, "the second step queued")

	q, err := e.Queue(nil)
	if err != nil {
		t.Fatal(err)
	}
	// The category default, with no limit, and the plugin.
	if q.Queued != 1 || q.Running != 1 || q.Workers != 2 || q.InteractiveWorkers != 1 || len(q.Resources) != 2 {
		t.Fatalf("queue %+v", q)
	}
	if r := q.Resources[0]; r.Resource != "cat:default" || r.Queued != 1 || r.Running != 1 || r.Limit != 0 {
		t.Fatalf("category %+v", r)
	}
	if r := q.Resources[1]; r.Resource != "plugin:bx" || r.Queued != 1 || r.Running != 1 || r.Limit != 1 {
		t.Fatalf("resource %+v", r)
	}
	if q, _ := e.Queue([]string{"other"}); q.Queued != 0 || q.Running != 0 {
		t.Fatalf("another project's queue %+v", q)
	}
	run, _ := e.Runner().Store().Run(second.ID)
	steps, _ := e.Runner().Store().Steps(second.ID)
	views := StepViews(run, steps, 1000)
	e.Waits(run, views)
	w := views[0].Queue
	if w == nil || w.BlockedBy != "plugin:bx" || w.Running != 1 || w.Limit != 1 || w.Ahead != 0 || views[0].QueuedAt.IsZero() {
		t.Fatalf("the queued step: %+v %+v", views[0], w)
	}
	once.Do(func() { close(ext.release) })
	waitStatus(t, e, second.ID, jobs.Done)
}

// TestEngine_QueueTrigger: a queue grown long starts the queue trigger's
// pipeline once, with the queue as its input, an interactive run; once the
// queue is short again, the next time it is long starts it again.
func TestEngine_QueueTrigger(t *testing.T) {
	st, err := jobs.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	gate := jobs.NewGate(func(r string) int { return map[string]int{"plugin:bx": 1}[r] })
	runner := jobs.NewRunner(st, jobs.Options{Workers: 2, Poll: 10 * time.Millisecond, Lease: time.Minute, Gate: gate})
	host := newFakeHost()
	e := New(host, runner, Config{}, nil, nil)
	host.types = e.Types()
	ext := &gateExt{ch: make(chan struct{})}
	e.Types().Extend(ext)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { runner.Start(ctx); close(stopped) }()
	t.Cleanup(func() { ext.open(); cancel(); <-stopped; st.Close() })

	host.notes["p/pipelines/b.md"] = pipelineNote("nodes:\n  - {id: call, use: bx.node}\n")
	host.notes["p/pipelines/alert.md"] = pipelineNote("trigger: {on: queue, resource: plugin:bx, queued: 1}\nnodes:\n  - {id: say, use: starlark, with: {input: \"{{ trigger }}\", code: \"output = input\"}}\n")
	alerts := func() []jobs.Run {
		runs, _ := e.Runner().Store().Runs(jobs.RunFilter{Ref: "p/pipelines/alert.md"})
		return runs
	}
	e.TickQueues()
	if n := len(alerts()); n != 0 {
		t.Fatalf("%d alerts with an empty queue", n)
	}
	first, _, _ := e.Start(StartRequest{Path: "p/pipelines/b.md", Trigger: TriggerManual})
	waitStatus(t, e, first.ID, jobs.Running)
	second, _, _ := e.Start(StartRequest{Path: "p/pipelines/b.md", Trigger: TriggerManual})
	eventually(t, func() bool { return stepOf(t, e, second.ID, "call").Status == jobs.Queued }, "the second step queued")
	e.TickQueues()
	e.TickQueues()
	got := alerts()
	if len(got) != 1 {
		t.Fatalf("%d alerts in one episode", len(got))
	}
	a := waitStatus(t, e, got[0].ID, jobs.Done)
	var in map[string]any
	_ = json.Unmarshal(a.Input, &in)
	if a.Trigger != TriggerQueue || a.Priority != jobs.PriorityInteractive || in["resource"] != "plugin:bx" || in["queued"] != float64(1) || in["running"] != float64(1) || in["limit"] != float64(1) || in["project"] != "p" {
		t.Fatalf("the alert run: %+v %v", a, in)
	}
	ext.open()
	waitStatus(t, e, second.ID, jobs.Done)
	e.TickQueues() // short again: the episode ends
	ext.rearm()
	third, _, _ := e.Start(StartRequest{Path: "p/pipelines/b.md", Trigger: TriggerManual})
	waitStatus(t, e, third.ID, jobs.Running)
	fourth, _, _ := e.Start(StartRequest{Path: "p/pipelines/b.md", Trigger: TriggerManual})
	eventually(t, func() bool { return stepOf(t, e, fourth.ID, "call").Status == jobs.Queued }, "the fourth step queued")
	e.TickQueues()
	if n := len(alerts()); n != 2 {
		t.Fatalf("%d alerts after a second episode", n)
	}
}

func TestParse_QueueTrigger(t *testing.T) {
	s, err := Parse("trigger: {on: queue, waited: 30m}\nnodes: [{id: a, use: starlark, with: {code: \"output = 1\"}}]", nil)
	if err != nil || s.Trigger.Waited != 30*time.Minute || s.Trigger.Resource != "" {
		t.Fatalf("%+v %v", s, err)
	}
	for _, bad := range []string{
		"trigger: {on: queue}",
		"trigger: {on: queue, queued: -1}",
		"trigger: {on: queue, waited: soon}",
		"trigger: {on: queue, queued: 5, resource: gpu}",
		"trigger: {on: manual, queued: 5}",
	} {
		if _, err := Parse(bad+"\nnodes: [{id: a, use: starlark, with: {code: \"output = 1\"}}]", nil); err == nil {
			t.Errorf("%s: accepted", bad)
		}
	}
}
