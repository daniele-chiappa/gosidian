package pipeline

import (
	"encoding/json"
	"time"

	"github.com/gosidian/gosidian/internal/jobs"
)

// The pipelines, runs and steps as the MCP tools and the REST API show
// them.

// Info is a pipeline in a listing.
type Info struct {
	Path    string   `json:"path"`
	Title   string   `json:"title"`
	Project string   `json:"project"`
	Trigger string   `json:"trigger,omitempty"`
	Nodes   []string `json:"nodes,omitempty"`
	Problem string   `json:"problem,omitempty"`
	// Notice is what keeps a pipeline from starting by itself, without
	// stopping it by hand: a commit trigger with git sync off.
	Notice  string    `json:"notice,omitempty"`
	LastRun *RunBrief `json:"last_run,omitempty"`
	// Category is the category of its runs (IMP-196); CategoryRules says
	// whether the server has rules for it.
	Category      string `json:"category,omitempty"`
	CategoryRules bool   `json:"category_rules,omitempty"`
}

// RunBrief is a run without its steps.
type RunBrief struct {
	ID       string      `json:"id"`
	Pipeline string      `json:"pipeline"`
	Project  string      `json:"project"`
	Status   jobs.Status `json:"status"`
	Trigger  string      `json:"trigger"`
	Actor    string      `json:"actor"`
	Error    string      `json:"error,omitempty"`
	Created  time.Time   `json:"created"`
	Started  time.Time   `json:"started,omitzero"`
	Finished time.Time   `json:"finished,omitzero"`
	// Interactive: its steps go first, and to the interactive workers;
	// Low: they go after the others' (IMP-196).
	Interactive bool `json:"interactive,omitempty"`
	Low         bool `json:"low,omitempty"`
	// Category is the category of its pipeline (IMP-196).
	Category string `json:"category,omitempty"`
}

// Brief is r without its steps.
func Brief(r jobs.Run) *RunBrief {
	return &RunBrief{ID: r.ID, Pipeline: r.Ref, Project: r.Project, Status: r.Status, Trigger: r.Trigger, Actor: r.Actor,
		Error: r.Error, Created: r.Created, Started: r.Started, Finished: r.Finished, Interactive: r.Priority >= jobs.PriorityInteractive,
		Low: r.Priority < jobs.PriorityNormal, Category: r.Category}
}

// Briefs is runs without their steps.
func Briefs(runs []jobs.Run) []*RunBrief {
	out := make([]*RunBrief, 0, len(runs))
	for _, r := range runs {
		out = append(out, Brief(r))
	}
	return out
}

// StepView is a step of a run.
type StepView struct {
	Name      string      `json:"name"`
	Use       string      `json:"use,omitempty"`
	Status    jobs.Status `json:"status"`
	Attempt   int         `json:"attempt,omitempty"`
	Error     string      `json:"error,omitempty"`
	Output    any         `json:"output,omitempty"`
	Truncated bool        `json:"output_truncated,omitempty"`
	Started   time.Time   `json:"started,omitzero"`
	Finished  time.Time   `json:"finished,omitzero"`
	WaitUntil time.Time   `json:"wait_until,omitzero"`
	// Resources are what the step takes a place in (IMP-196); a queued
	// step says since when it waits, and why (Engine.Waits).
	Resources []string   `json:"resources,omitempty"`
	QueuedAt  time.Time  `json:"queued_at,omitzero"`
	NotBefore time.Time  `json:"not_before,omitzero"`
	Queue     *StepQueue `json:"queue,omitempty"`
}

// StepQueue is why a queued step waits: the steps the queue takes before
// it, and the first of its resources that is full, with its places taken
// and its limit.
type StepQueue struct {
	Ahead     int    `json:"ahead"`
	BlockedBy string `json:"blocked_by,omitempty"`
	Running   int    `json:"running,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// StepViews is a run's steps in the order of its nodes: an output larger
// than max bytes is cut, and says so.
func StepViews(run jobs.Run, steps []jobs.Step, max int) []StepView {
	spec, _ := SpecOf(run)
	out := make([]StepView, 0, len(steps))
	for _, st := range steps {
		v := StepView{Name: st.Name, Status: st.Status, Attempt: st.Attempt, Error: st.Error, Started: st.Started, Finished: st.Finished, WaitUntil: st.WaitUntil,
			Resources: st.Resources}
		if st.Status == jobs.Queued {
			v.QueuedAt, v.NotBefore = st.QueuedAt, st.NotBefore
		}
		if spec != nil {
			if n, ok := spec.Node(st.Name); ok {
				v.Use = n.Use
			}
		}
		if len(st.Output) > 0 {
			if len(st.Output) > max {
				v.Output, v.Truncated = string(st.Output[:max]), true
			} else {
				var o any
				if json.Unmarshal(st.Output, &o) == nil {
					v.Output = o
				}
			}
		}
		out = append(out, v)
	}
	return out
}

// Infos lists ps with their last run.
func Infos(ps []Pipeline, store *jobs.Store) []Info {
	out := make([]Info, 0, len(ps))
	for _, p := range ps {
		info := Info{Path: p.Path, Title: p.Title, Project: p.Project}
		if p.Spec != nil {
			info.Trigger = p.Spec.Trigger.On
			for _, n := range p.Spec.Nodes {
				info.Nodes = append(info.Nodes, n.ID+" ("+n.Use+")")
			}
		}
		if p.Err != nil {
			info.Problem = p.Err.Error()
		}
		if runs, _ := store.Runs(jobs.RunFilter{Ref: p.Path, Limit: 1}); len(runs) == 1 {
			info.LastRun = Brief(runs[0])
		}
		out = append(out, info)
	}
	return out
}
