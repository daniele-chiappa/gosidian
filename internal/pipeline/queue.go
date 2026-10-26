package pipeline

import (
	"slices"
	"time"

	"github.com/gosidian/gosidian/internal/jobs"
)

// The queue as it is (IMP-196): for the Runs window, memory_pipelines and
// the metrics. The places taken come from the runner's gate, for the whole
// server; the steps queued, from the runs of the projects asked.

// QueueView is the queue of the steps.
type QueueView struct {
	// Queued are the steps whose time has come, Later those waiting for a
	// retry's time; Running the steps under way.
	Queued  int `json:"queued"`
	Later   int `json:"later"`
	Running int `json:"running"`
	// Workers run any step, InteractiveWorkers only an interactive run's.
	Workers            int `json:"workers"`
	InteractiveWorkers int `json:"interactive_workers"`
	// OldestWait is how long the step queued longest has waited, in
	// seconds.
	OldestWait float64         `json:"oldest_wait_seconds"`
	Resources  []ResourceQueue `json:"resources"`
}

// ResourceQueue is the queue of a resource: its steps queued (whose time
// has come), its places taken (by the steps and the agents' calls of the
// whole server) and its limit (0: none).
type ResourceQueue struct {
	Resource   string  `json:"resource"`
	Queued     int     `json:"queued"`
	Running    int     `json:"running"`
	Limit      int     `json:"limit"`
	OldestWait float64 `json:"oldest_wait_seconds"`
}

// Queue is the queue of the steps of the runs of projects (all for nil).
func (e *Engine) Queue(projects []string) (QueueView, error) {
	now := e.now()
	store := e.runner.Store()
	rows, err := store.Queue(projects, now)
	if err != nil {
		return QueueView{}, err
	}
	running, err := store.Running(projects)
	if err != nil {
		return QueueView{}, err
	}
	v := QueueView{Running: running, Resources: []ResourceQueue{}}
	v.Workers, v.InteractiveWorkers = e.runner.Workers()
	byRes := map[string]*ResourceQueue{}
	get := func(r string) *ResourceQueue {
		if q, ok := byRes[r]; ok {
			return q
		}
		q := &ResourceQueue{Resource: r}
		byRes[r] = q
		return q
	}
	wait := func(oldest time.Time) float64 {
		if oldest.IsZero() {
			return 0
		}
		return max(now.Sub(oldest).Seconds(), 0)
	}
	for _, row := range rows {
		v.Queued += row.Ready
		v.Later += row.Queued - row.Ready
		w := wait(row.Oldest)
		v.OldestWait = max(v.OldestWait, w)
		for _, r := range row.Resources {
			q := get(r)
			q.Queued += row.Ready
			q.OldestWait = max(q.OldestWait, w)
		}
	}
	gate := e.runner.Gate()
	for _, u := range gate.Running() {
		get(u.Resource).Running = u.Running
	}
	for r, q := range byRes {
		q.Limit = gate.Limit(r)
		v.Resources = append(v.Resources, *q)
	}
	slices.SortFunc(v.Resources, func(a, b ResourceQueue) int {
		switch {
		case a.Resource < b.Resource:
			return -1
		case a.Resource > b.Resource:
			return 1
		}
		return 0
	})
	return v, nil
}

// Waits says, in each queued step of views, why it waits: the steps the
// queue takes before it, and the first of its resources that is full.
func (e *Engine) Waits(run jobs.Run, views []StepView) {
	now := e.now()
	gate := e.runner.Gate()
	for i := range views {
		v := &views[i]
		if v.Status != jobs.Queued {
			continue
		}
		q := &StepQueue{}
		if n, err := e.runner.Store().Ahead(run, v.Name, now); err == nil {
			q.Ahead = n
		}
		if r := gate.Full(v.Resources); r != "" {
			q.BlockedBy, q.Limit = r, gate.Limit(r)
			for _, u := range gate.Running() {
				if u.Resource == r {
					q.Running = u.Running
				}
			}
		}
		v.Queue = q
	}
}
