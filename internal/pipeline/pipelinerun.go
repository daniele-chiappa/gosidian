package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/jobs"
)

// pipeline.run (IMP-186): a run starts a pipeline of its own project with
// an input, and waits for it to end or not. One pipeline does a job many
// others need (describing the images of an article), and each pipeline
// that imports documents ends by starting it.
//
// The run it starts, the child, is keyed by its parent: its idempotency
// key is <prefix><parent run>:<node>[:<index>][:<key>], so a step tried
// again finds the same child, and the child knows whom to resume when it
// ends (Ended). A parent waits as wait.call does; the child's end resumes
// it, or fails it when the child did not end well.

// The prefixes of a child's idempotency key: a child its parent waits for,
// and one it does not.
const (
	keyWaited  = "parent:"
	keySpawned = "spawned:"
)

// MaxRunDepth is how many runs may start each other in a chain through
// pipeline.run: a pipeline that starts itself stops there.
const MaxRunDepth = 4

func pipelineRun(_ context.Context, env *Env, with map[string]any) (any, error) {
	p, err := required(with, "pipeline")
	if err != nil {
		return nil, err
	}
	rel, err := env.projectPath(p)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(rel, ".md") {
		rel += ".md"
	}
	input := map[string]any{}
	if v := with["input"]; v != nil {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, errors.New("with.input is a map: the pipeline it starts reads it as its trigger")
		}
		input = m
	}
	wait := true
	if w, ok := with["wait"].(bool); ok {
		wait = w
	}
	index, inEach := env.Scope["index"]
	if wait && inEach {
		return nil, errors.New("in an each, pipeline.run starts its runs with wait: false")
	}
	e := env.Engine
	var until time.Time
	if wait {
		if until, err = e.waitUntil(with); err != nil {
			return nil, err
		}
	}
	if n := e.depth(env.Run); n >= MaxRunDepth {
		return nil, fmt.Errorf("this run was started by %d others in a chain of pipeline.run, and may start none (at most %d)", n, MaxRunDepth)
	}

	key := keySpawned
	if wait {
		key = keyWaited
	}
	key += env.Run.ID + ":" + env.Node.ID
	if inEach {
		if i, ok := index.(float64); ok {
			key += ":" + strconv.Itoa(int(i))
		}
	}
	if k := strings.TrimSpace(str(with, "key")); k != "" {
		key += ":" + k
	}
	child, existing, err := e.Start(StartRequest{Path: rel, Trigger: TriggerPipeline, Input: input, Actor: Actor(env.Run.Ref), IdemKey: key})
	if err != nil {
		return nil, err
	}
	if !wait {
		return map[string]any{"run": child.ID, "pipeline": child.Ref, "status": child.Status, "existing": existing}, nil
	}
	if child.Status.Terminal() {
		// It ended before this step could wait (or a try before this one
		// started it): its outcome is the step's.
		steps, err := e.runner.Store().Steps(child.ID)
		if err != nil {
			return nil, err
		}
		res := childResult(child, steps, e.cfg.MaxOutputBytes)
		if child.Status != jobs.Done {
			return nil, FailedWith{Output: res, Err: childError(child)}
		}
		return res, nil
	}
	return nil, WaitFor{Until: until}
}

// parentOf reads a child's idempotency key: the run that started it, the
// node, and whether that node waits for it.
func parentOf(idemKey string) (run, node string, waited, ok bool) {
	rest, waited := strings.CutPrefix(idemKey, keyWaited)
	if !waited {
		var spawned bool
		if rest, spawned = strings.CutPrefix(idemKey, keySpawned); !spawned {
			return "", "", false, false
		}
	}
	parts := strings.SplitN(rest, ":", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false, false
	}
	return parts[0], parts[1], waited, true
}

// depth is how many runs started run, one another, through pipeline.run:
// 0 for a run nothing started.
func (e *Engine) depth(run jobs.Run) int {
	d := 0
	for d <= MaxRunDepth {
		parent, _, _, ok := parentOf(run.IdemKey)
		if !ok {
			return d
		}
		d++
		p, err := e.runner.Store().Run(parent)
		if err != nil {
			return d
		}
		run = p
	}
	return d
}

// childResult is what a waiting parent's step gives: the child run, its
// status and steps, and the outputs of its steps when they fit in a step's
// output.
func childResult(run jobs.Run, steps []jobs.Step, maxOutput int) map[string]any {
	res := map[string]any{"run": run.ID, "pipeline": run.Ref, "status": run.Status, "error": run.Error}
	status := map[string]any{}
	outputs := map[string]json.RawMessage{}
	for _, st := range steps {
		status[st.Name] = st.Status
		if st.Status == jobs.Done && len(st.Output) > 0 {
			outputs[st.Name] = st.Output
		}
	}
	res["steps"] = status
	if b, err := json.Marshal(outputs); err == nil && len(b) <= maxOutput/2 {
		res["outputs"] = outputs
	} else {
		res["outputs_omitted"] = true
	}
	return res
}

func childError(run jobs.Run) error {
	msg := fmt.Sprintf("the run %s of %s %s", run.ID, run.Ref, run.Status)
	if run.Error != "" {
		msg += ": " + run.Error
	}
	return errors.New(msg)
}

// endedChild resumes the parent that waits for run, a child that just
// ended, with its outcome. The parent's step may not wait yet (the child
// ended while the node that started it still ran): then it tries again for
// a while, and stops once that step ended some other way.
func (e *Engine) endedChild(run jobs.Run, steps []jobs.Step) {
	if run.Trigger != TriggerPipeline {
		return
	}
	parent, node, waited, ok := parentOf(run.IdemKey)
	if !ok || !waited {
		return
	}
	// Start gives these keys to pipeline.run alone, which starts a pipeline
	// of its own project: a parent elsewhere is no parent.
	if p, err := e.runner.Store().Run(parent); err != nil || p.Project != run.Project {
		e.log.Warn("pipeline: a child run whose parent is not a run of its project", "run", run.ID, "parent", parent)
		return
	}
	out, err := json.Marshal(childResult(run, steps, e.cfg.MaxOutputBytes))
	if err != nil {
		e.log.Warn("pipeline: the outcome of a child run", "run", run.ID, "err", err)
		return
	}
	status, msg := jobs.Done, ""
	if run.Status != jobs.Done {
		status, msg = jobs.Failed, childError(run).Error()
	}
	if e.resumeParent(parent, node, status, out, msg) {
		return
	}
	go func() {
		delay := 20 * time.Millisecond
		for deadline := time.Now().Add(2 * time.Minute); time.Now().Before(deadline); {
			time.Sleep(delay)
			if e.resumeParent(parent, node, status, out, msg) {
				return
			}
			delay = min(delay*2, 5*time.Second)
		}
		e.log.Warn("pipeline: a child run ended and its parent never waited for it", "run", run.ID, "parent", parent, "node", node)
	}()
}

// resumeParent ends the parent's waiting step; it says whether there is
// nothing more to do: the step ended, or it ended some other way already.
func (e *Engine) resumeParent(parent, node string, status jobs.Status, out json.RawMessage, msg string) bool {
	err := e.runner.EndWait(parent, node, status, out, msg)
	if err == nil {
		return true
	}
	if !errors.Is(err, jobs.ErrState) {
		e.log.Warn("pipeline: resuming the parent of a child run", "parent", parent, "node", node, "err", err)
		return true
	}
	steps, serr := e.runner.Store().Steps(parent)
	if serr != nil {
		return true
	}
	for _, st := range steps {
		if st.Name == node {
			return st.Status.Terminal() // waiting soon, or done another way
		}
	}
	return true
}

// stopChildren cancels the children a run waited for, when it ended
// without them: canceled, or failed (its wait expired).
func (e *Engine) stopChildren(run jobs.Run) {
	kids, err := e.runner.Store().Runs(jobs.RunFilter{IdemPrefix: keyWaited + run.ID + ":", Limit: 500})
	if err != nil {
		e.log.Warn("pipeline: the children of a run", "run", run.ID, "err", err)
		return
	}
	for _, k := range kids {
		if k.Status.Terminal() {
			continue
		}
		if err := e.runner.Cancel(k.ID); err != nil && !errors.Is(err, jobs.ErrState) {
			e.log.Warn("pipeline: canceling a child run", "run", k.ID, "parent", run.ID, "err", err)
		}
	}
}
