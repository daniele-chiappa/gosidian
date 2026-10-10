package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Outcome is how a step ended, as its Executor reports it.
type Outcome struct {
	// Status is Done, Failed, Waiting (suspended until a call resumes it
	// or WaitUntil passes) or Queued (try again from RetryAt).
	Status    Status
	Output    json.RawMessage
	Error     string
	RetryAt   time.Time
	WaitUntil time.Time
}

// Executor runs the steps of one kind of run.
type Executor interface {
	// Step runs one step. steps is the run as it stands, the step among
	// them. ctx ends with the step's lease or when the run is canceled.
	Step(ctx context.Context, run Run, step Step, steps []Step) Outcome
	// Next is called when a step changed: it says which pending steps
	// become queued, skipped or canceled, and, when the run ends, how. A
	// nil RunChange leaves the run's status to the runner: running while
	// steps are queued or running, waiting while only waits are left,
	// and, once every step ended, done, or failed if one failed.
	Next(run Run, steps []Step) ([]StepChange, *RunChange)
	// Ended is told that the run reached an end state.
	Ended(run Run, steps []Step)
}

// Options tunes a Runner.
type Options struct {
	Workers int           // steps run at once; default 2
	Poll    time.Duration // how often the queue is read when nothing wakes it; default 2s
	Lease   time.Duration // how long a step may run; default 15m
	// Retention is how long the runs that ended are kept; 0 keeps them.
	Retention time.Duration
	Logger    *slog.Logger
	Now       func() time.Time
	// OnChange is told, after the change is saved, that a run changed: a
	// step started or ended, the run's status moved. For live updates.
	OnChange func(runID string)
}

// Runner runs the queued steps of the store on a pool of goroutines.
type Runner struct {
	store *Store
	opts  Options
	wake  chan struct{}

	mu       sync.Mutex
	execs    map[string]Executor
	running  map[string]map[string]context.CancelFunc // run → step → cancel
	runLocks map[string]*sync.Mutex
}

// NewRunner is a runner on store.
func NewRunner(store *Store, opts Options) *Runner {
	if opts.Workers <= 0 {
		opts.Workers = 2
	}
	if opts.Poll <= 0 {
		opts.Poll = 2 * time.Second
	}
	if opts.Lease <= 0 {
		opts.Lease = 15 * time.Minute
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Runner{
		store:    store,
		opts:     opts,
		wake:     make(chan struct{}, 1),
		execs:    map[string]Executor{},
		running:  map[string]map[string]context.CancelFunc{},
		runLocks: map[string]*sync.Mutex{},
	}
}

func (r *Runner) changed(runID string) {
	if r.opts.OnChange != nil {
		r.opts.OnChange(runID)
	}
}

// SetOnChange sets Options.OnChange after the runner is built (the
// executor that wants it comes later).
func (r *Runner) SetOnChange(fn func(runID string)) { r.opts.OnChange = fn }

// Store is the runner's store.
func (r *Runner) Store() *Store { return r.store }

// Register sets the executor of a kind of run.
func (r *Runner) Register(kind string, e Executor) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.execs[kind] = e
}

func (r *Runner) executor(kind string) (Executor, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.execs[kind]
	if !ok {
		return nil, fmt.Errorf("jobs: no executor for kind %q", kind)
	}
	return e, nil
}

// runLock serializes the changes to one run: two of its steps that end
// together must not both decide what comes next from a stale view.
func (r *Runner) runLock(id string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.runLocks[id]
	if !ok {
		m = &sync.Mutex{}
		r.runLocks[id] = m
	}
	return m
}

func (r *Runner) forgetRun(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.runLocks, id)
	delete(r.running, id)
}

// Wake reads the queue now instead of at the next poll.
func (r *Runner) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Submit stores a run and queues the steps that can start. With an
// idempotency key already used for the same Ref, the stored run comes
// back and nothing starts: existing is true.
func (r *Runner) Submit(run Run, steps []Step) (Run, bool, error) {
	if _, err := r.executor(run.Kind); err != nil {
		return Run{}, false, err
	}
	stored, existing, err := r.store.CreateRun(run, steps)
	if err != nil || existing {
		return stored, existing, err
	}
	if err := r.advance(stored.ID); err != nil {
		return stored, false, err
	}
	r.Wake()
	now, err := r.store.Run(stored.ID)
	return now, false, err
}

// Resume ends a waiting step with output, and the run goes on. step ""
// resumes the only waiting step of the run.
func (r *Runner) Resume(runID, step string, output json.RawMessage) error {
	l := r.runLock(runID)
	l.Lock()
	steps, err := r.store.Steps(runID)
	if err != nil {
		l.Unlock()
		return err
	}
	var target *Step
	for i := range steps {
		if steps[i].Status != Waiting || (step != "" && steps[i].Name != step) {
			continue
		}
		if target != nil {
			l.Unlock()
			return fmt.Errorf("%w: more than one step waits, name one", ErrState)
		}
		target = &steps[i]
	}
	if target == nil {
		l.Unlock()
		if len(steps) == 0 {
			return ErrNotFound
		}
		return fmt.Errorf("%w: no step waits", ErrState)
	}
	err = r.store.apply(runID, []StepChange{{Name: target.Name, Status: Done, Output: output, Finished: r.opts.Now()}}, nil)
	l.Unlock()
	if err != nil {
		return err
	}
	if err := r.advance(runID); err != nil {
		return err
	}
	r.Wake()
	return nil
}

// Cancel stops a run: the steps not ended yet are canceled, the running
// ones see their context end.
func (r *Runner) Cancel(runID string) error {
	l := r.runLock(runID)
	l.Lock()
	run, err := r.store.Run(runID)
	if err != nil {
		l.Unlock()
		return err
	}
	if run.Status.Terminal() {
		l.Unlock()
		return fmt.Errorf("%w: the run already ended (%s)", ErrState, run.Status)
	}
	steps, err := r.store.Steps(runID)
	if err != nil {
		l.Unlock()
		return err
	}
	now := r.opts.Now()
	var changes []StepChange
	for _, st := range steps {
		if !st.Status.Terminal() {
			changes = append(changes, StepChange{Name: st.Name, Status: Canceled, Error: "canceled", Finished: now})
		}
	}
	err = r.store.apply(runID, changes, &RunChange{Status: Canceled, Error: "canceled", Finished: now})
	r.mu.Lock()
	for _, cancel := range r.running[runID] {
		cancel()
	}
	r.mu.Unlock()
	l.Unlock()
	if err != nil {
		return err
	}
	r.changed(runID)
	r.ended(runID)
	return nil
}

// advance asks the run's executor what comes after the run's current
// state, and applies it.
func (r *Runner) advance(runID string) error {
	l := r.runLock(runID)
	l.Lock()
	run, err := r.store.Run(runID)
	if err != nil {
		l.Unlock()
		return err
	}
	if run.Status.Terminal() {
		l.Unlock()
		return nil
	}
	e, err := r.executor(run.Kind)
	if err != nil {
		l.Unlock()
		return err
	}
	steps, err := r.store.Steps(runID)
	if err != nil {
		l.Unlock()
		return err
	}
	changes, rc := e.Next(run, steps)
	steps = applied(steps, changes)
	if rc == nil {
		rc = derive(run, steps, r.opts.Now())
	}
	if rc != nil && rc.Status.Terminal() {
		// The steps left behind by the end of the run.
		for _, st := range steps {
			if !st.Status.Terminal() {
				changes = append(changes, StepChange{Name: st.Name, Status: Canceled, Error: "the run ended", Finished: rc.Finished})
			}
		}
	}
	err = r.store.apply(runID, changes, rc)
	l.Unlock()
	if err != nil {
		return err
	}
	r.changed(runID)
	if rc != nil && rc.Status.Terminal() {
		r.ended(runID)
	}
	return nil
}

// applied is steps with changes applied, as the store will have them.
func applied(steps []Step, changes []StepChange) []Step {
	out := append([]Step(nil), steps...)
	for _, c := range changes {
		for i := range out {
			if out[i].Name == c.Name && !out[i].Status.Terminal() {
				out[i].Status = c.Status
				if c.Error != "" {
					out[i].Error = c.Error
				}
			}
		}
	}
	return out
}

// derive is the run's status from its steps, when the executor leaves it
// to the runner: nil when it does not change. A pending step waits for
// others: it keeps the run going only while some step can still move.
func derive(run Run, steps []Step, now time.Time) *RunChange {
	active, waiting, pending, failed := false, false, "", ""
	for _, st := range steps {
		switch st.Status {
		case Queued, Running:
			active = true
		case Waiting:
			waiting = true
		case Pending:
			if pending == "" {
				pending = st.Name
			}
		case Failed:
			if failed == "" {
				failed = st.Name + ": " + st.Error
			}
		}
	}
	switch {
	case active:
		if run.Status == Waiting {
			return &RunChange{Status: Running}
		}
		return nil
	case waiting:
		if run.Status != Waiting {
			return &RunChange{Status: Waiting}
		}
		return nil
	case failed != "":
		return &RunChange{Status: Failed, Error: failed, Finished: now}
	case pending != "":
		// Nothing can move any more, yet a step never started: the
		// executor left it pending.
		return &RunChange{Status: Failed, Error: pending + ": it could not start", Finished: now}
	default:
		return &RunChange{Status: Done, Finished: now}
	}
}

func (r *Runner) ended(runID string) {
	defer r.forgetRun(runID)
	run, err := r.store.Run(runID)
	if err != nil {
		return
	}
	e, err := r.executor(run.Kind)
	if err != nil {
		return
	}
	steps, err := r.store.Steps(runID)
	if err != nil {
		return
	}
	e.Ended(run, steps)
}

// Start runs the queue until ctx ends. It first puts back the steps a
// previous process left running.
func (r *Runner) Start(ctx context.Context) {
	if n, err := r.store.recoverRunning(r.opts.Now()); err != nil {
		r.opts.Logger.Error("jobs: recovering the steps left running", "err", err)
	} else if n > 0 {
		r.opts.Logger.Info("jobs: steps left running by the last process queued again", "steps", n)
	}
	slots := make(chan struct{}, r.opts.Workers)
	var wg sync.WaitGroup
	defer wg.Wait()
	ticker := time.NewTicker(r.opts.Poll)
	defer ticker.Stop()
	lastPrune := time.Time{}
	for {
		now := r.opts.Now()
		r.expireWaits(now)
		if r.opts.Retention > 0 && now.Sub(lastPrune) > time.Hour {
			lastPrune = now
			if n, err := r.store.Prune(now.Add(-r.opts.Retention)); err != nil {
				r.opts.Logger.Error("jobs: pruning old runs", "err", err)
			} else if n > 0 {
				r.opts.Logger.Info("jobs: old runs pruned", "runs", n)
			}
		}
		free := r.opts.Workers - len(slots)
		if free > 0 {
			steps, err := r.store.claim(now, free, r.opts.Lease)
			if err != nil {
				r.opts.Logger.Error("jobs: reading the queue", "err", err)
			}
			for _, st := range steps {
				slots <- struct{}{}
				wg.Add(1)
				go func(st Step) {
					defer wg.Done()
					defer func() { <-slots; r.Wake() }()
					r.runStep(ctx, st)
				}(st)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-r.wake:
		}
	}
}

func (r *Runner) expireWaits(now time.Time) {
	steps, err := r.store.expiredWaits(now)
	if err != nil {
		r.opts.Logger.Error("jobs: reading the waits", "err", err)
		return
	}
	for _, st := range steps {
		l := r.runLock(st.RunID)
		l.Lock()
		err := r.store.apply(st.RunID, []StepChange{{Name: st.Name, Status: Failed, Error: "no call resumed it before " + st.WaitUntil.Format(time.RFC3339), Finished: now}}, nil)
		l.Unlock()
		if err == nil {
			err = r.advance(st.RunID)
		}
		if err != nil {
			r.opts.Logger.Error("jobs: ending an expired wait", "run", st.RunID, "step", st.Name, "err", err)
		}
	}
}

func (r *Runner) runStep(ctx context.Context, st Step) {
	log := r.opts.Logger.With("run", st.RunID, "step", st.Name, "attempt", st.Attempt)
	run, err := r.store.Run(st.RunID)
	if err != nil {
		log.Error("jobs: loading the run", "err", err)
		return
	}
	e, err := r.executor(run.Kind)
	if err != nil {
		_ = r.store.apply(st.RunID, []StepChange{{Name: st.Name, Status: Failed, Error: err.Error(), Finished: r.opts.Now()}}, nil)
		_ = r.advance(st.RunID)
		return
	}
	steps, err := r.store.Steps(st.RunID)
	if err != nil {
		log.Error("jobs: loading the steps", "err", err)
		return
	}
	sctx, cancel := context.WithDeadline(ctx, st.LeaseUntil)
	r.mu.Lock()
	if r.running[st.RunID] == nil {
		r.running[st.RunID] = map[string]context.CancelFunc{}
	}
	r.running[st.RunID][st.Name] = cancel
	r.mu.Unlock()
	r.changed(st.RunID)
	out := r.safeStep(sctx, e, run, st, steps)
	cancel()
	r.mu.Lock()
	if m := r.running[st.RunID]; m != nil {
		delete(m, st.Name)
	}
	r.mu.Unlock()
	if ctx.Err() != nil && out.Status != Done {
		// The server is stopping: leave the step running, the next
		// process takes it back when its lease passes.
		return
	}
	now := r.opts.Now()
	c := StepChange{Name: st.Name, Status: out.Status, Output: out.Output, Error: out.Error}
	switch out.Status {
	case Done, Failed:
		c.Finished = now
	case Queued:
		c.NotBefore = out.RetryAt
	case Waiting:
		c.WaitUntil = out.WaitUntil
	default:
		c.Status, c.Error, c.Finished = Failed, fmt.Sprintf("the executor answered status %q", out.Status), now
	}
	l := r.runLock(st.RunID)
	l.Lock()
	err = r.store.apply(st.RunID, []StepChange{c}, nil)
	l.Unlock()
	if err == nil {
		err = r.advance(st.RunID)
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		log.Error("jobs: saving the step", "err", err)
	}
}

// safeStep runs a step, turning a panic into a failure.
func (r *Runner) safeStep(ctx context.Context, e Executor, run Run, st Step, steps []Step) (out Outcome) {
	defer func() {
		if p := recover(); p != nil {
			r.opts.Logger.Error("jobs: step panicked", "run", run.ID, "step", st.Name, "panic", p)
			out = Outcome{Status: Failed, Error: fmt.Sprintf("panic: %v", p)}
		}
	}()
	return e.Step(ctx, run, st, steps)
}
