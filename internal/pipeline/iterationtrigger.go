package pipeline

import "encoding/json"

// The iteration trigger (IMP-216 phase 2): the server closed an iteration
// of a board at its end, writing its review in its note, and the
// iteration pipelines of its project start, once per pipeline and
// iteration: one sends the review on, an agent writes the retrospective.
// The server closes iterations with pipelines or without; this is what it
// does more with them.

// EventClosed is the one event of the iteration trigger.
const EventClosed = "closed"

// IterationClose is an iteration the server closed: its note, its board,
// its goal and days, the review it wrote, and its cards that were not
// done, which no longer link it.
type IterationClose struct {
	Project   string
	Iteration string
	Board     string
	Goal      string
	Start     string
	End       string
	Review    string
	Deferred  []string
}

// IterationClosed starts the iteration pipelines of c's project. The
// iteration and its last day are the idempotency key. When the review does
// not fit in the run's input it is left out: the run reads it in the note.
func (e *Engine) IterationClosed(c IterationClose) {
	if !e.host.Enabled(c.Project) {
		return
	}
	ps, err := e.host.Pipelines(c.Project)
	if err != nil {
		e.log.Warn("pipeline: listing the pipelines for an iteration", "iteration", c.Iteration, "err", err)
		return
	}
	deferred := c.Deferred
	if deferred == nil {
		deferred = []string{}
	}
	input := map[string]any{"event": EventClosed, "project": c.Project, "iteration": c.Iteration, "board": c.Board,
		"goal": c.Goal, "start": c.Start, "end": c.End, "deferred": deferred, "review": c.Review}
	if b, err := json.Marshal(input); err != nil || len(b) > e.cfg.MaxInputBytes {
		delete(input, "review")
	}
	for _, p := range ps {
		if p.Err != nil || p.Spec == nil || p.Spec.Trigger.On != TriggerIteration {
			continue
		}
		if _, _, err := e.Start(StartRequest{Path: p.Path, Trigger: TriggerIteration, Input: input, Actor: "iteration",
			IdemKey: "iteration:" + c.Iteration + ":" + c.End}); err != nil {
			e.log.Warn("pipeline: an iteration run", "pipeline", p.Path, "iteration", c.Iteration, "err", err)
		}
	}
}
