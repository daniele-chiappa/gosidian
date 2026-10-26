package pipeline

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/jobs"
)

// categoryEngine is an engine with the categories cats, a gate whose
// category taglio has one place, and the node bx.node of ext, whose calls
// wait for ext to open.
func categoryEngine(t *testing.T, cats []Category) (*Engine, *fakeHost, *gateExt) {
	t.Helper()
	st, err := jobs.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	limits := map[string]int{}
	for _, c := range cats {
		limits[ResourceCategory+c.Name] = c.MaxRunning
	}
	gate := jobs.NewGate(func(r string) int { return limits[r] })
	runner := jobs.NewRunner(st, jobs.Options{Workers: 3, Poll: 10 * time.Millisecond, Lease: time.Minute, Gate: gate, Fair: true})
	host := newFakeHost()
	e := New(host, runner, Config{Categories: cats, MaxSyncWait: time.Minute}, nil, nil)
	host.types = e.Types()
	ext := &gateExt{ch: make(chan struct{})}
	e.Types().Extend(ext)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { runner.Start(ctx); close(stopped) }()
	t.Cleanup(func() { ext.open(); cancel(); <-stopped; st.Close() })
	return e, host, ext
}

// TestEngine_Categories: a category's priority, its projects, its runs not
// ended, its steps at once, its replace and its wait (IMP-196).
func TestEngine_Categories(t *testing.T) {
	e, host, ext := categoryEngine(t, []Category{
		{Name: "taglio", MaxRunning: 1, MaxRuns: 2, Replace: true, Priority: CategoryLow, Wait: 90 * time.Second},
		{Name: "riservata", Projects: []string{"q"}},
	})
	host.notes["p/pipelines/cut.md"] = pipelineNote("category: taglio\nnodes:\n  - {id: call, use: bx.node}\n")
	host.notes["p/pipelines/res.md"] = pipelineNote("category: riservata\nnodes:\n  - {id: a, use: starlark, with: {code: \"output = 1\"}}\n")
	host.notes["p/pipelines/plain.md"] = pipelineNote("nodes:\n  - {id: a, use: starlark, with: {code: \"output = 1\"}}\n")

	// Reserved to q: p's pipeline does not start.
	if _, _, err := e.Start(StartRequest{Path: "p/pipelines/res.md", Trigger: TriggerManual}); err == nil || !strings.Contains(err.Error(), "not open to project p") {
		t.Fatalf("a reserved category: %v", err)
	}
	// No category: default, normal, no rules.
	plain, _, err := e.Start(StartRequest{Path: "p/pipelines/plain.md", Trigger: TriggerManual})
	if err != nil || plain.Category != CategoryDefault || plain.Priority != jobs.PriorityNormal {
		t.Fatalf("a plain run: %+v %v", plain, err)
	}

	first, _, err := e.Start(StartRequest{Path: "p/pipelines/cut.md", Trigger: TriggerManual, Input: map[string]any{"doc": "a"}})
	if err != nil || first.Category != "taglio" || first.Priority != jobs.PriorityLow {
		t.Fatalf("the first cut: %+v %v", first, err)
	}
	waitStatus(t, e, first.ID, jobs.Running)
	// One step of taglio at a time: the second waits for the category.
	second, _, err := e.Start(StartRequest{Path: "p/pipelines/cut.md", Trigger: TriggerManual, Input: map[string]any{"doc": "b"}})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return stepOf(t, e, second.ID, "call").Status == jobs.Queued }, "the second cut queued")
	run, _ := e.Runner().Store().Run(second.ID)
	steps, _ := e.Runner().Store().Steps(second.ID)
	views := StepViews(run, steps, 100)
	e.Waits(run, views)
	if q := views[0].Queue; q == nil || q.BlockedBy != "cat:taglio" || q.Limit != 1 {
		t.Fatalf("the second cut waits for %+v", views[0].Queue)
	}
	// Two runs not ended: a start from outside is refused, an event's is
	// not, and the same start again (its key) gives its run.
	var full ErrCategoryFull
	if _, _, err := e.Start(StartRequest{Path: "p/pipelines/cut.md", Trigger: TriggerManual, Input: map[string]any{"doc": "c"}}); !errors.As(err, &full) || full.Runs != 2 || full.RetryAfter <= 0 {
		t.Fatalf("a third start: %v", err)
	}
	ev, _, err := e.Start(StartRequest{Path: "p/pipelines/cut.md", Trigger: TriggerSchedule, Input: map[string]any{"doc": "b"}, IdemKey: "schedule:1"})
	if err != nil {
		t.Fatalf("an event's start: %v", err)
	}
	// replace: the schedule's run has the queued second's input: the second
	// is canceled, replaced by it.
	if r, _ := e.Runner().Store().Run(second.ID); r.Status != jobs.Canceled || !strings.Contains(r.Error, "replaced by run "+ev.ID) {
		t.Fatalf("the replaced run: %+v", r)
	}
	if r, _ := e.Runner().Store().Run(first.ID); r.Status != jobs.Running {
		t.Fatalf("the running run was touched: %+v", r)
	}
	if again, existing, err := e.Start(StartRequest{Path: "p/pipelines/cut.md", Trigger: TriggerSchedule, Input: map[string]any{"doc": "b"}, IdemKey: "schedule:1"}); err != nil || !existing || again.ID != ev.ID {
		t.Fatalf("the same start again: %v %v", existing, err)
	}
	if d := e.DefaultWait("p/pipelines/cut.md"); d != 60*time.Second {
		t.Fatalf("the default wait %s: the category's, at most max_sync_wait", d)
	}
	if d := e.DefaultWait("p/pipelines/plain.md"); d != 0 {
		t.Fatalf("a plain pipeline's default wait %s", d)
	}
	infos := e.Infos([]Pipeline{mustPipeline(t, host, "p/pipelines/cut.md"), mustPipeline(t, host, "p/pipelines/plain.md")})
	if infos[0].Category != "taglio" || !infos[0].CategoryRules || infos[1].Category != CategoryDefault || infos[1].CategoryRules {
		t.Fatalf("infos %+v", infos)
	}
	ext.open()
	waitStatus(t, e, first.ID, jobs.Done)
	waitStatus(t, e, ev.ID, jobs.Done)
}

func mustPipeline(t *testing.T, host *fakeHost, path string) Pipeline {
	t.Helper()
	p, err := host.Pipeline(path)
	if err != nil || p.Err != nil {
		t.Fatalf("%s: %v %v", path, err, p.Err)
	}
	return p
}

func TestParse_Category(t *testing.T) {
	s, err := Parse("category: taglio\nnodes: [{id: a, use: starlark, with: {code: \"output = 1\"}}]", nil)
	if err != nil || s.CategoryOf() != "taglio" {
		t.Fatalf("%+v %v", s, err)
	}
	s, _ = Parse("trigger: {on: chat}\nnodes: [{id: a, use: starlark, with: {code: \"output = 1\"}}]", nil)
	if s.CategoryOf() != CategoryChat {
		t.Fatalf("a chat pipeline's category %q", s.CategoryOf())
	}
	if _, err := Parse("category: \"a b\"\nnodes: [{id: a, use: starlark, with: {code: \"output = 1\"}}]", nil); err == nil {
		t.Error("a category with a space")
	}
	if _, err := Parse("trigger: {on: queue, resource: \"cat:taglio\", queued: 3}\nnodes: [{id: a, use: starlark, with: {code: \"output = 1\"}}]", nil); err != nil {
		t.Errorf("a queue trigger on a category: %v", err)
	}
}
