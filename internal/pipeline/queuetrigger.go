package pipeline

import (
	"math"
	"sync"
	"time"

	"github.com/gosidian/gosidian/internal/jobs"
)

// The queue trigger (IMP-196): a pipeline of the project starts when the
// project's queue of steps grows long (queued steps at least) or one of its
// steps has waited long (waited), of every step or of one resource. It
// starts once each time the queue becomes long, and again only after the
// queue was short once: an episode. Its runs are interactive, so that they
// go before the queue they tell about.
//
// The episodes live in memory: after a restart a queue still long starts
// the trigger again. A queue across the projects is the metrics' (the
// gauges of /metrics), which a monitoring of the server watches.

// queueEpisodes are the queue triggers whose queue is long, since when, by
// the pipeline's path.
type queueEpisodes struct {
	mu    sync.Mutex
	since map[string]time.Time
}

// TickQueues starts the queue triggers whose queue has become long, and
// ends the episodes of those whose queue is short again.
func (e *Engine) TickQueues() {
	ps, err := e.host.Pipelines("")
	if err != nil {
		e.log.Warn("pipeline: listing the pipelines for the queue triggers", "err", err)
		return
	}
	now := e.now()
	views := map[string]QueueView{}
	seen := map[string]bool{}
	for _, p := range ps {
		if p.Err != nil || p.Spec.Trigger.On != TriggerQueue || !e.host.Enabled(p.Project) {
			continue
		}
		seen[p.Path] = true
		v, ok := views[p.Project]
		if !ok {
			if v, err = e.Queue([]string{p.Project}); err != nil {
				e.log.Warn("pipeline: reading the queue for a queue trigger", "pipeline", p.Path, "err", err)
				continue
			}
			views[p.Project] = v
		}
		t := p.Spec.Trigger
		queued, oldest, running, limit := v.Queued, v.OldestWait, v.Running, 0
		if t.Resource != "" {
			queued, oldest, running, limit = 0, 0, 0, e.runner.Gate().Limit(t.Resource)
			for _, r := range v.Resources {
				if r.Resource == t.Resource {
					queued, oldest, running, limit = r.Queued, r.OldestWait, r.Running, r.Limit
				}
			}
		}
		long := (t.Queued > 0 && queued >= t.Queued) || (t.Waited > 0 && oldest >= t.Waited.Seconds())
		e.queueLong.mu.Lock()
		if e.queueLong.since == nil {
			e.queueLong.since = map[string]time.Time{}
		}
		since, was := e.queueLong.since[p.Path]
		start := long && !was
		switch {
		case start:
			since = now
			e.queueLong.since[p.Path] = now
		case !long && was:
			delete(e.queueLong.since, p.Path)
		}
		e.queueLong.mu.Unlock()
		if !start {
			continue
		}
		// Whole seconds: the input goes into a message as it is.
		input := map[string]any{"project": p.Project, "resource": t.Resource, "queued": queued, "running": running, "limit": limit,
			"oldest_wait_seconds": math.Round(oldest), "threshold": map[string]any{"queued": t.Queued, "waited_seconds": t.Waited.Seconds()}}
		key := "queue:" + since.UTC().Format(time.RFC3339Nano)
		if _, _, err := e.Start(StartRequest{Path: p.Path, Trigger: TriggerQueue, Input: input, Actor: "queue", IdemKey: key, Priority: jobs.PriorityInteractive}); err != nil {
			e.log.Warn("pipeline: a queue trigger's run", "pipeline", p.Path, "err", err)
		}
	}
	// The pipelines no longer there, or no longer on queue, forget theirs.
	e.queueLong.mu.Lock()
	for path := range e.queueLong.since {
		if !seen[path] {
			delete(e.queueLong.since, path)
		}
	}
	e.queueLong.mu.Unlock()
}
