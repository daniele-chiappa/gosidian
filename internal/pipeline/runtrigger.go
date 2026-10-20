package pipeline

import (
	"slices"
	"strings"

	"github.com/gosidian/gosidian/internal/jobs"
)

// The run trigger (IMP-206): the end of a run starts the pipelines of its
// project that take it, its status among theirs and its pipeline matching
// their glob, once per pipeline and run that ended: a project has a
// pipeline that tells someone its runs fail.
//
// No loop. A run started by a run trigger starts none when it ends, nor do
// the runs it starts with pipeline.run, at any depth: the runs a run
// trigger starts end the chain. And what the pipelines on run write, and
// the ones they start, starts no commit pipeline (Committed): a commit run
// that fails would start one of them again, which would write again.

// maxRunError caps the error of the run that ended in the trigger of the
// run it starts: a long one would not fit in a run's input, and no run
// would start.
const maxRunError = 4 << 10

// TakesRun says whether a run trigger takes the end of a run of the
// pipeline rel (relative to the project): its status is one of the
// trigger's, rel matches its glob, with or without .md.
func (t Trigger) TakesRun(rel, status string) bool {
	if t.On != TriggerRun || !slices.Contains(t.Status, status) {
		return false
	}
	return t.Pipelines == "" || globMatch(t.Pipelines, rel) || globMatch(t.Pipelines, strings.TrimSuffix(rel, ".md"))
}

// startOnRun starts the pipelines on run of run's project that take its
// end. The run that ended is the idempotency key: it starts each of them
// once.
func (e *Engine) startOnRun(run jobs.Run, steps []jobs.Step) {
	if !e.host.Enabled(run.Project) || e.fromRunTrigger(run) {
		return
	}
	ps, err := e.host.Pipelines(run.Project)
	if err != nil {
		e.log.Warn("pipeline: listing the pipelines for the end of a run", "run", run.ID, "err", err)
		return
	}
	rel := strings.TrimPrefix(run.Ref, run.Project+"/")
	var input map[string]any
	for _, p := range ps {
		if p.Err != nil || !p.Spec.Trigger.TakesRun(rel, string(run.Status)) {
			continue
		}
		if input == nil {
			input = map[string]any{"run": run.ID, "pipeline": run.Ref, "project": run.Project, "status": run.Status,
				"error": clipHead(run.Error, maxRunError), "failed_step": failedStep(run, steps)}
		}
		if _, _, err := e.Start(StartRequest{Path: p.Path, Trigger: TriggerRun, Input: input, Actor: Actor(run.Ref), IdemKey: "run:" + run.ID}); err != nil {
			e.log.Warn("pipeline: a run on the end of another", "pipeline", p.Path, "run", run.ID, "err", err)
		}
	}
}

// fromRunTrigger says whether a run trigger started run, or a run that
// started it through pipeline.run. A chain it cannot read to its start (a
// parent pruned, longer than max_run_depth) counts as one: the rule may
// leave out a notice, never start a loop.
func (e *Engine) fromRunTrigger(run jobs.Run) bool {
	for range e.cfg.MaxRunDepth + 1 {
		switch run.Trigger {
		case TriggerRun:
			return true
		case TriggerPipeline:
		default:
			return false
		}
		parent, _, _, ok := parentOf(run.IdemKey)
		if !ok {
			return true
		}
		p, err := e.runner.Store().Run(parent)
		if err != nil {
			return true
		}
		run = p
	}
	return true
}

// failedStep is the node that failed run, as Next names it: the first
// that failed without on_error: continue; "" when the run did not fail,
// or no node did (its spec did not read).
func failedStep(run jobs.Run, steps []jobs.Step) string {
	if run.Status != jobs.Failed {
		return ""
	}
	spec, err := SpecOf(run)
	if err != nil {
		return ""
	}
	for _, st := range steps {
		if n, ok := spec.Node(st.Name); st.Status == jobs.Failed && (!ok || n.OnError != "continue") {
			return st.Name
		}
	}
	return ""
}
