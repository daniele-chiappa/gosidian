package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/jobs"
)

// Kind is the job kind of an automation's action on the job runner
// (IMP-166): the rules are read on the engine's clock as before, and the
// handoff or snapshot a rule fires runs as a run of one step, with tries,
// a persistent queue, and a resume after a restart.
const Kind = "automation"

// actionStep is the one step of an action's run.
const actionStep = "action"

// actionAttempts are the tries of an action; the wait before each new one
// doubles from actionBackoff.
const actionAttempts = 3

var actionBackoff = 30 * time.Second

// actionInput is the input of an action's run: what the rule's action
// needs, and the keys of the state it fires (a slot, or the rows').
type actionInput struct {
	Rule     string    `json:"rule"`
	Action   string    `json:"action"` // the rule's Action, for the log
	Snapshot bool      `json:"snapshot,omitempty"`
	Summary  string    `json:"summary,omitempty"`
	Items    []string  `json:"items,omitempty"`
	Keys     []string  `json:"keys"`
	At       time.Time `json:"at"` // the tick that fired it
}

// SetRunner makes the engine queue the actions of its rules on r, which
// runs them through the engine. Without a runner the engine acts at once.
func (e *Engine) SetRunner(r *jobs.Runner) {
	r.Register(Kind, e)
	e.runner = r
}

// queue submits the action of r on the runner and marks its keys as
// pending in rs, so that the next ticks do not fire them again.
func (e *Engine) queue(db Database, r Rule, rs *RuleState, in actionInput, run Run, fail func(error) (Run, bool)) (Run, bool) {
	input, err := json.Marshal(in)
	if err != nil {
		return fail(err)
	}
	job, _, err := e.runner.Submit(jobs.Run{Kind: Kind, Ref: db.Path, Project: db.Project, Trigger: Kind, Input: input, Actor: Kind},
		[]jobs.Step{{Name: actionStep}})
	if err != nil {
		return fail(fmt.Errorf("queue the action: %w", err))
	}
	if rs.Pending == nil {
		rs.Pending = map[string]string{}
	}
	for _, k := range in.Keys {
		rs.Pending[k] = job.ID
	}
	run.Job = job.ID
	return run, true
}

// Step runs an action: it reads the rule again and writes its handoff or
// snapshot. A failure tries again, actionAttempts times in all; a rule no
// longer there fails at once.
func (e *Engine) Step(_ context.Context, run jobs.Run, st jobs.Step, _ []jobs.Step) jobs.Outcome {
	var in actionInput
	if err := json.Unmarshal(run.Input, &in); err != nil {
		return jobs.Outcome{Status: jobs.Failed, Error: "the action's input: " + err.Error()}
	}
	db, r, err := e.findRule(run.Project, run.Ref, in.Rule)
	if err != nil {
		return jobs.Outcome{Status: jobs.Failed, Error: err.Error()}
	}
	var path string
	if in.Snapshot {
		path, err = e.host.Snapshot(db, r, in.At)
	} else {
		path, err = e.host.Handoff(db, r, in.Summary, in.Items, in.At)
	}
	if err != nil {
		if st.Attempt < actionAttempts {
			return jobs.Outcome{Status: jobs.Queued, Error: err.Error(), RetryAt: time.Now().Add(actionBackoff << (st.Attempt - 1))}
		}
		return jobs.Outcome{Status: jobs.Failed, Error: err.Error()}
	}
	out, _ := json.Marshal(map[string]string{"path": path})
	return jobs.Outcome{Status: jobs.Done, Output: out}
}

// findRule reads the rule named name of the database note at ref.
func (e *Engine) findRule(project, ref, name string) (Database, Rule, error) {
	dbs, err := e.host.Databases(project)
	if err != nil {
		return Database{}, Rule{}, err
	}
	for _, db := range dbs {
		if db.Path != ref {
			continue
		}
		for _, r := range db.Rules {
			if r.Name == name {
				return db, r, nil
			}
		}
	}
	return Database{}, Rule{}, fmt.Errorf("the rule %q of %s is gone", name, ref)
}

// Next queues the action's step, the run's only one; its end is the run's.
func (e *Engine) Next(_ jobs.Run, steps []jobs.Step) ([]jobs.StepChange, *jobs.RunChange) {
	var ch []jobs.StepChange
	for _, st := range steps {
		if st.Status == jobs.Pending {
			ch = append(ch, jobs.StepChange{Name: st.Name, Status: jobs.Queued})
		}
	}
	return ch, nil
}

// Ended records the action's end in its rule's state. The runner calls it
// on its own goroutine, which a tick holding the state may be waiting on
// (a Submit): the record waits for the state on another goroutine.
func (e *Engine) Ended(run jobs.Run, steps []jobs.Step) {
	go func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.settle(run, steps) {
			e.save()
		}
	}()
}

// settle records how an action's run ended: done fires its slot or rows;
// failed or canceled leaves them to fire again at the next tick, with the
// error on the rule, logged once until it changes. It says whether the
// state changed. The caller holds e.mu.
func (e *Engine) settle(run jobs.Run, steps []jobs.Step) bool {
	var in actionInput
	if json.Unmarshal(run.Input, &in) != nil {
		return false
	}
	rs := e.state.Rules[Rule{Name: in.Rule}.Key(run.Ref)]
	if rs == nil {
		return false
	}
	var mine []string
	for _, k := range in.Keys {
		if rs.Pending[k] == run.ID {
			mine = append(mine, k)
			delete(rs.Pending, k)
		}
	}
	if len(mine) == 0 {
		return false
	}
	at := run.Finished
	if at.IsZero() {
		at = e.now()
	}
	entry := Run{At: at, Database: run.Ref, Rule: in.Rule, Action: in.Action, Job: run.ID}
	if run.Status == jobs.Done {
		for _, k := range mine {
			rs.Fired[k] = at
			if isRowKey(k) {
				entry.Rows = append(entry.Rows, k[:strings.LastIndex(k, "@")])
			}
		}
		rs.Error = ""
		entry.Path = outputPath(steps)
		e.state.log(entry)
		return true
	}
	msg := stepError(run, steps)
	if rs.Error == msg {
		return true
	}
	rs.Error = msg
	entry.Error = msg
	e.log.Warn("automations: rule failed", "database", run.Ref, "rule", in.Rule, "run", run.ID, "err", msg)
	e.state.log(entry)
	return true
}

// reconcile settles the pending actions whose runs ended while the engine
// was not told (a restart between the end and the save), and frees those
// whose runs are gone from the jobs database, to fire again. The caller
// holds e.mu.
func (e *Engine) reconcile() {
	ids := map[string]bool{}
	for _, rs := range e.state.Rules {
		for _, id := range rs.Pending {
			ids[id] = true
		}
	}
	store := e.runner.Store()
	for id := range ids {
		run, err := store.Run(id)
		if errors.Is(err, jobs.ErrNotFound) {
			for _, rs := range e.state.Rules {
				for k, pid := range rs.Pending {
					if pid == id {
						delete(rs.Pending, k)
					}
				}
			}
			continue
		}
		if err != nil || !run.Status.Terminal() {
			continue
		}
		steps, err := store.Steps(id)
		if err != nil {
			continue
		}
		e.settle(run, steps)
	}
}

// Await waits up to d for the actions that runs queued to end, and returns
// runs with the note each wrote or why it failed: memory_automations
// run:true answers as when the engine acted at once.
func (e *Engine) Await(runs []Run, d time.Duration) []Run {
	if e.runner == nil {
		return runs
	}
	store := e.runner.Store()
	deadline := time.Now().Add(d)
	out := append([]Run(nil), runs...)
	for i := range out {
		if out[i].Job == "" {
			continue
		}
		for {
			run, err := store.Run(out[i].Job)
			if err == nil && run.Status.Terminal() {
				steps, _ := store.Steps(run.ID)
				e.mu.Lock()
				if e.settle(run, steps) {
					e.save()
				}
				e.mu.Unlock()
				if run.Status == jobs.Done {
					out[i].Path = outputPath(steps)
				} else {
					out[i].Error = stepError(run, steps)
				}
				break
			}
			if err != nil || time.Now().After(deadline) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	return out
}

func outputPath(steps []jobs.Step) string {
	for _, st := range steps {
		var o struct {
			Path string `json:"path"`
		}
		if st.Name == actionStep && json.Unmarshal(st.Output, &o) == nil {
			return o.Path
		}
	}
	return ""
}

func stepError(run jobs.Run, steps []jobs.Step) string {
	for _, st := range steps {
		if st.Name == actionStep && st.Error != "" {
			return st.Error
		}
	}
	if run.Error != "" {
		return run.Error
	}
	return "the action's run ended " + string(run.Status)
}
