package pipeline

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/jobs"
)

// syncingHost is a fakeHost that brings a step's writes to disk at its end
// (WriteSyncer), counting the calls, and failing them with fail set.
type syncingHost struct {
	*fakeHost
	syncs atomic.Int32
	fail  atomic.Bool
}

func (h *syncingHost) SyncWrites() error {
	h.syncs.Add(1)
	if h.fail.Load() {
		return errors.New("disk gone")
	}
	return nil
}

func newSyncingEngine(t *testing.T) (*Engine, *syncingHost) {
	t.Helper()
	st, err := jobs.Open(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	runner := jobs.NewRunner(st, jobs.Options{Workers: 2, Poll: 10 * time.Millisecond, Lease: time.Minute})
	host := &syncingHost{fakeHost: newFakeHost()}
	e := New(host, runner, Config{}, fakeSecrets{}, nil)
	host.types = e.Types()
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { runner.Start(ctx); close(stopped) }()
	t.Cleanup(func() { cancel(); <-stopped; st.Close() })
	return e, host
}

// Every step ends by bringing its writes to disk (IMP-185), and a step
// whose writes cannot get there fails, so that it runs again.
func TestEngine_StepSyncsItsWrites(t *testing.T) {
	e, host := newSyncingEngine(t)
	host.notes["p/pipelines/w.md"] = pipelineNote(`
trigger: {on: external}
nodes:
  - id: text
    use: starlark
    with:
      code: |
        output = {"text": "hello"}
  - id: write
    use: note.write
    with: {path: "out/a", body: "{{ text.text }}"}
`)
	run, _, err := e.Start(StartRequest{Path: "p/pipelines/w.md", Trigger: TriggerExternal, Actor: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, run.ID, jobs.Done)
	if n := host.syncs.Load(); n != 2 {
		t.Errorf("SyncWrites called %d times, want once per step (2)", n)
	}

	host.fail.Store(true)
	run, _, err = e.Start(StartRequest{Path: "p/pipelines/w.md", Trigger: TriggerExternal, Actor: "alice", Input: map[string]any{"again": true}})
	if err != nil {
		t.Fatal(err)
	}
	failed := waitStatus(t, e, run.ID, jobs.Failed)
	if !strings.Contains(failed.Error, "bringing the step's writes to disk: disk gone") {
		t.Errorf("run error %q, want the sync's", failed.Error)
	}
}
