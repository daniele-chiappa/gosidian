package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/jobs"
)

// Kind is the job kind of a pipeline run.
const Kind = "pipeline"

// Pipeline is a pipeline note as the host finds it.
type Pipeline struct {
	Path    string `json:"path"`
	Project string `json:"project"`
	Title   string `json:"title"`
	Spec    *Spec  `json:"-"`
	// Err says why the note is not a usable pipeline (its block does not
	// parse, its project does not run pipelines).
	Err error `json:"-"`
}

// Host is what the engine acts through: the server, which reads and writes
// as the pipeline's identity, inside the pipeline's project only.
type Host interface {
	// Pipelines lists the pipeline notes of a project, or of every
	// project that runs pipelines for "".
	Pipelines(project string) ([]Pipeline, error)
	// Pipeline loads one pipeline note.
	Pipeline(path string) (Pipeline, error)
	// Enabled says whether a project runs pipelines (use_pipelines).
	Enabled(project string) bool
	// ReadNote reads a note of the project.
	ReadNote(project, path string) (NoteData, error)
	// WriteNote writes a note of the project, as the pipeline: actor is
	// pipeline:<the pipeline note's path>, for the audit.
	WriteNote(project, path string, content []byte, mode WriteMode, actor string) (WriteResult, error)
	// ReadAttachment reads an attachment of the project.
	ReadAttachment(project, path string) ([]byte, error)
	// Changed is told that a run changed, for the live updates.
	Changed(run jobs.Run)
}

// Secrets resolves the name of a secret to its value.
type Secrets interface {
	Get(name string) (string, error)
}

// Provider is a language model service with an OpenAI-compatible API
// (Ollama, vLLM, llama.cpp, OpenRouter).
type Provider struct {
	Name    string
	BaseURL string // up to /v1
	// APIKeySecret names the secret with the key; "" for none (Ollama).
	APIKeySecret string
	Model        string // the default model
}

// Config tunes the engine.
type Config struct {
	MaxInputBytes  int // a run's input; default 256 KiB
	MaxOutputBytes int // a step's output; default 1 MiB
	// MaxLLMTokens is the most LLM tokens a run may use; 0: no cap.
	MaxLLMTokens int
	// MaxWait is the longest a wait.call may wait; default 7 days.
	MaxWait time.Duration
	// StarlarkSteps caps a starlark script; default 10 million.
	StarlarkSteps uint64
	// HTTPAllowlist holds the URL prefixes http.request and the
	// callbacks may call; empty: none.
	HTTPAllowlist []string
	// PublicURL is the server's address for the resume URLs
	// ({{ run.resume_url }}); "" gives a path.
	PublicURL string
	Providers []Provider
	// Location is the zone of the schedule triggers; nil: local.
	Location *time.Location
}

// Engine runs pipelines on a job runner.
type Engine struct {
	host    Host
	runner  *jobs.Runner
	types   *Registry
	cfg     Config
	secrets Secrets
	client  *http.Client
	log     *slog.Logger
	now     func() time.Time
}

// New is an engine acting through host, running on runner (which it
// registers itself with), with the base node types.
func New(host Host, runner *jobs.Runner, cfg Config, secrets Secrets, logger *slog.Logger) *Engine {
	if cfg.MaxInputBytes <= 0 {
		cfg.MaxInputBytes = 256 << 10
	}
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = 1 << 20
	}
	if cfg.MaxWait <= 0 {
		cfg.MaxWait = 7 * 24 * time.Hour
	}
	if cfg.StarlarkSteps == 0 {
		cfg.StarlarkSteps = 10_000_000
	}
	if cfg.Location == nil {
		cfg.Location = time.Local
	}
	if logger == nil {
		logger = slog.Default()
	}
	e := &Engine{host: host, runner: runner, types: NewRegistry(), cfg: cfg, secrets: secrets, log: logger,
		now: func() time.Time { return time.Now().UTC() }}
	e.client = guardedClient(e)
	runner.Register(Kind, e)
	runner.SetOnChange(func(id string) {
		if run, err := runner.Store().Run(id); err == nil {
			host.Changed(run)
		}
	})
	return e
}

// Types is the registry of node types, to add more.
func (e *Engine) Types() *Registry { return e.types }

// Runner is the engine's job runner.
func (e *Engine) Runner() *jobs.Runner { return e.runner }

// Config is the engine's configuration.
func (e *Engine) Config() Config { return e.cfg }

// SetClock replaces the clock (tests).
func (e *Engine) SetClock(now func() time.Time) { e.now = now }

// StartRequest starts a run.
type StartRequest struct {
	Path    string // the pipeline note
	Trigger string // manual, external, ingest, schedule
	Input   any    // the trigger's payload, a JSON value
	Actor   string // who started it: an account, a token, the server
	// IdemKey starts the run once: the same key again gives the run
	// it started.
	IdemKey     string
	CallbackURL string
}

// ErrDisabled: the pipeline's project does not run pipelines.
var ErrDisabled = errors.New("the project does not run pipelines (use_pipelines is off)")

// Start checks the pipeline and queues a run of it.
func (e *Engine) Start(req StartRequest) (jobs.Run, bool, error) {
	p, err := e.host.Pipeline(req.Path)
	if err != nil {
		return jobs.Run{}, false, err
	}
	if p.Err != nil {
		return jobs.Run{}, false, p.Err
	}
	if !e.host.Enabled(p.Project) {
		return jobs.Run{}, false, ErrDisabled
	}
	switch req.Trigger {
	case TriggerManual:
	case TriggerExternal:
		if p.Spec.Trigger.On != TriggerExternal && p.Spec.Trigger.On != TriggerManual {
			return jobs.Run{}, false, fmt.Errorf("the pipeline starts on %s, not from outside", p.Spec.Trigger.On)
		}
	case TriggerIngest, TriggerSchedule:
	default:
		return jobs.Run{}, false, fmt.Errorf("unknown trigger %q", req.Trigger)
	}
	if req.CallbackURL != "" && !e.allowed(req.CallbackURL) {
		return jobs.Run{}, false, fmt.Errorf("callback_url %q is not among the addresses the server may call ([pipelines] http_allowlist)", req.CallbackURL)
	}
	input, err := json.Marshal(req.Input)
	if err != nil {
		return jobs.Run{}, false, fmt.Errorf("input: %w", err)
	}
	if len(input) > e.cfg.MaxInputBytes {
		return jobs.Run{}, false, fmt.Errorf("the input is %d bytes, at most %d: pass files by their vault path", len(input), e.cfg.MaxInputBytes)
	}
	spec, err := json.Marshal(p.Spec)
	if err != nil {
		return jobs.Run{}, false, err
	}
	steps := make([]jobs.Step, len(p.Spec.Nodes))
	for i, n := range p.Spec.Nodes {
		steps[i] = jobs.Step{Name: n.ID, Deps: n.After}
	}
	run, existing, err := e.runner.Submit(jobs.Run{
		Kind: Kind, Ref: p.Path, Project: p.Project, Trigger: req.Trigger, Input: input, Spec: spec,
		IdemKey: req.IdemKey, Actor: req.Actor, CallbackURL: req.CallbackURL, ResumeKey: jobs.NewSecret(),
	}, steps)
	return run, existing, err
}

// SpecOf is the spec a run executes, as it was when the run started.
func SpecOf(run jobs.Run) (*Spec, error) {
	var s Spec
	if err := json.Unmarshal(run.Spec, &s); err != nil {
		return nil, fmt.Errorf("the run's spec: %w", err)
	}
	return &s, nil
}

// ResumeURL is the address that resumes a run's waiting step.
func (e *Engine) ResumeURL(run jobs.Run) string {
	return strings.TrimRight(e.cfg.PublicURL, "/") + "/api/v1/runs/" + run.ID + "/resume?key=" + run.ResumeKey
}

// scope builds what a step's expressions read.
func (e *Engine) scope(run jobs.Run, steps []jobs.Step) scope {
	sc := scope{
		"trigger": decode(run.Input),
		"run": map[string]any{
			"id": run.ID, "pipeline": run.Ref, "project": run.Project, "trigger": run.Trigger,
			"resume_url": e.ResumeURL(run), "resume_key": run.ResumeKey,
		},
	}
	for _, st := range steps {
		if st.Status == jobs.Done || st.Status == jobs.Failed {
			sc[st.Name] = decode(st.Output)
		} else if st.Status == jobs.Skipped {
			sc[st.Name] = nil
		}
	}
	return sc
}

func tokensUsed(steps []jobs.Step) int {
	n := 0
	var walk func(v any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			if u, ok := t["usage"].(map[string]any); ok {
				if tt, ok := u["total_tokens"].(float64); ok {
					n += int(tt)
				}
			}
			for k, x := range t {
				if k != "usage" {
					walk(x)
				}
			}
		case []any:
			for _, x := range t {
				walk(x)
			}
		}
	}
	for _, st := range steps {
		walk(decode(st.Output))
	}
	return n
}

// Step runs one node of a run (jobs.Executor).
func (e *Engine) Step(ctx context.Context, run jobs.Run, st jobs.Step, steps []jobs.Step) jobs.Outcome {
	spec, err := SpecOf(run)
	if err != nil {
		return jobs.Outcome{Status: jobs.Failed, Error: err.Error()}
	}
	node, ok := spec.Node(st.Name)
	if !ok {
		return jobs.Outcome{Status: jobs.Failed, Error: "the node is not in the pipeline"}
	}
	t, ok := e.types.get(node.Use)
	if !ok {
		return jobs.Outcome{Status: jobs.Failed, Error: fmt.Sprintf("there is no node type %q on this server", node.Use)}
	}
	sc := e.scope(run, steps)
	env := &Env{Run: run, Node: node, Project: run.Project, Scope: sc, Engine: e,
		Log: e.log.With("run", run.ID, "pipeline", run.Ref, "node", node.ID), tokens: tokensUsed(steps)}

	var out any
	if node.Each == "" {
		out, err = e.runOnce(ctx, env, t, node, sc)
	} else {
		out, err = e.runEach(ctx, env, t, node, sc)
	}
	var wait WaitFor
	if errors.As(err, &wait) {
		return jobs.Outcome{Status: jobs.Waiting, WaitUntil: wait.Until}
	}
	if err != nil {
		if st.Attempt < node.Retry.Attempts {
			delay := node.Retry.Backoff << (st.Attempt - 1)
			var ra RetryAfter
			if errors.As(err, &ra) && ra.After > delay {
				delay = ra.After
			}
			return jobs.Outcome{Status: jobs.Queued, Error: err.Error(), RetryAt: e.now().Add(delay)}
		}
		return jobs.Outcome{Status: jobs.Failed, Error: err.Error()}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return jobs.Outcome{Status: jobs.Failed, Error: fmt.Sprintf("the output is not JSON: %v", err)}
	}
	if len(b) > e.cfg.MaxOutputBytes {
		return jobs.Outcome{Status: jobs.Failed, Error: fmt.Sprintf("the output is %d bytes, at most %d: write large content to a note and pass its path", len(b), e.cfg.MaxOutputBytes)}
	}
	return jobs.Outcome{Status: jobs.Done, Output: b}
}

func (e *Engine) runOnce(ctx context.Context, env *Env, t Type, node *Node, sc scope) (any, error) {
	w, err := sc.eval(map[string]any(node.With))
	if err != nil {
		return nil, err
	}
	with, _ := w.(map[string]any)
	if with == nil {
		with = map[string]any{}
	}
	cctx, cancel := context.WithTimeout(ctx, node.Timeout)
	defer cancel()
	out, err := t.Run(cctx, env, with)
	if err != nil && cctx.Err() == context.DeadlineExceeded && ctx.Err() == nil {
		return nil, fmt.Errorf("it did not end within %s: %w", node.Timeout, err)
	}
	return out, err
}

func (e *Engine) runEach(ctx context.Context, env *Env, t Type, node *Node, sc scope) (any, error) {
	list, err := sc.eval(node.Each)
	if err != nil {
		return nil, err
	}
	items, ok := list.([]any)
	if list == nil {
		items, ok = []any{}, true
	}
	if !ok {
		return nil, fmt.Errorf("each %s is not a list", node.Each)
	}
	out := make([]any, 0, len(items))
	for i, it := range items {
		isc := scope{}
		for k, v := range sc {
			isc[k] = v
		}
		isc["item"], isc["index"] = it, float64(i)
		env.Scope = isc
		v, err := e.runOnce(ctx, env, t, node, isc)
		if err != nil {
			var wait WaitFor
			if errors.As(err, &wait) {
				return nil, errors.New("a wait.call cannot run in an each")
			}
			return nil, fmt.Errorf("element %d: %w", i, err)
		}
		out = append(out, v)
	}
	return out, nil
}

// Next queues the nodes whose turn has come (jobs.Executor). A node waits
// for the nodes in its after; it is canceled when one of them failed
// (unless that one continues on error), skipped when all of them were
// skipped or its condition says so.
func (e *Engine) Next(run jobs.Run, steps []jobs.Step) ([]jobs.StepChange, *jobs.RunChange) {
	spec, err := SpecOf(run)
	if err != nil {
		return nil, &jobs.RunChange{Status: jobs.Failed, Error: err.Error(), Finished: e.now()}
	}
	byName := map[string]jobs.Step{}
	for _, st := range steps {
		byName[st.Name] = st
	}
	continues := func(name string) bool {
		n, ok := spec.Node(name)
		return ok && n.OnError == "continue"
	}
	var changes []jobs.StepChange
	sc := e.scope(run, steps)
	now := e.now()
	for _, st := range steps {
		if st.Status != jobs.Pending {
			continue
		}
		node, ok := spec.Node(st.Name)
		if !ok {
			changes = append(changes, jobs.StepChange{Name: st.Name, Status: jobs.Canceled, Error: "not in the pipeline", Finished: now})
			continue
		}
		ready, blocked, allSkipped := true, "", len(node.After) > 0
		for _, d := range node.After {
			ds := byName[d]
			switch ds.Status {
			case jobs.Done:
				allSkipped = false
			case jobs.Skipped:
			case jobs.Failed:
				allSkipped = false
				if !continues(d) {
					blocked = d
				}
			case jobs.Canceled:
				blocked = d
			default:
				ready = false
			}
		}
		switch {
		case blocked != "":
			changes = append(changes, jobs.StepChange{Name: st.Name, Status: jobs.Canceled, Error: blocked + " did not end well", Finished: now})
		case !ready:
		case allSkipped:
			changes = append(changes, jobs.StepChange{Name: st.Name, Status: jobs.Skipped, Finished: now})
		default:
			skip, err := e.skips(node, sc)
			switch {
			case err != nil:
				changes = append(changes, jobs.StepChange{Name: st.Name, Status: jobs.Failed, Error: err.Error(), Finished: now})
			case skip:
				changes = append(changes, jobs.StepChange{Name: st.Name, Status: jobs.Skipped, Finished: now})
			default:
				changes = append(changes, jobs.StepChange{Name: st.Name, Status: jobs.Queued})
			}
		}
	}
	// The end of the run, when every node ended: failed only for a node
	// that failed and does not continue on error.
	after := map[string]jobs.Status{}
	for _, st := range steps {
		after[st.Name] = st.Status
	}
	for _, c := range changes {
		after[c.Name] = c.Status
	}
	failed := ""
	for _, st := range steps {
		s := after[st.Name]
		if !s.Terminal() {
			return changes, nil
		}
		if s == jobs.Failed && !continues(st.Name) && failed == "" {
			failed = st.Name + ": " + firstError(st, changes)
		}
	}
	if failed != "" {
		return changes, &jobs.RunChange{Status: jobs.Failed, Error: failed, Finished: now}
	}
	return changes, &jobs.RunChange{Status: jobs.Done, Finished: now}
}

func firstError(st jobs.Step, changes []jobs.StepChange) string {
	for _, c := range changes {
		if c.Name == st.Name && c.Error != "" {
			return c.Error
		}
	}
	return st.Error
}

func (e *Engine) skips(node *Node, sc scope) (bool, error) {
	if node.When != "" {
		v, err := sc.eval(node.When)
		if err != nil {
			return false, err
		}
		if !truthy(v) {
			return true, nil
		}
	}
	if node.Unless != "" {
		v, err := sc.eval(node.Unless)
		if err != nil {
			return false, err
		}
		if truthy(v) {
			return true, nil
		}
	}
	return false, nil
}

// Ended writes the day's line of the pipeline's runs note and calls the
// run's callback (jobs.Executor).
func (e *Engine) Ended(run jobs.Run, steps []jobs.Step) {
	if err := e.summarize(run); err != nil {
		e.log.Warn("pipeline: the runs note", "run", run.ID, "pipeline", run.Ref, "err", err)
	}
	if run.CallbackURL != "" {
		if err := e.callback(run, steps); err != nil {
			e.log.Warn("pipeline: the callback", "run", run.ID, "url", run.CallbackURL, "err", err)
		}
	}
}

// OnAttachment starts the ingest pipelines of a project that take a
// document just stored: att is its vault path, name its original file
// name, note the note it came with ("" for none). Errors are logged:
// the document is stored either way.
func (e *Engine) OnAttachment(project, att, name, note, actor string) {
	if !e.host.Enabled(project) {
		return
	}
	ps, err := e.host.Pipelines(project)
	if err != nil {
		e.log.Warn("pipeline: listing the pipelines for an ingest", "project", project, "err", err)
		return
	}
	ext := strings.TrimPrefix(path.Ext(name), ".")
	if ext == "" {
		ext = strings.TrimPrefix(path.Ext(att), ".")
	}
	folder := ""
	if note != "" {
		folder = strings.TrimPrefix(path.Dir(note), project)
		folder = strings.Trim(folder, "/.")
	}
	stem := strings.TrimSuffix(path.Base(name), path.Ext(name))
	for _, p := range ps {
		if p.Err != nil || !p.Spec.Trigger.Matches(ext, folder) {
			continue
		}
		input := map[string]any{"file": att, "name": name, "stem": stem, "ext": strings.ToLower(ext), "folder": folder, "note": note, "project": project}
		if _, _, err := e.Start(StartRequest{Path: p.Path, Trigger: TriggerIngest, Input: input, Actor: actor, IdemKey: "ingest:" + att}); err != nil {
			e.log.Warn("pipeline: an ingest run", "pipeline", p.Path, "file", att, "err", err)
		}
	}
}

// TickSchedules starts the schedule pipelines whose slot came within the
// last window: once per slot, the slot is the idempotency key. A slot
// older than window (the server was down) is not run late.
func (e *Engine) TickSchedules(window time.Duration) {
	ps, err := e.host.Pipelines("")
	if err != nil {
		e.log.Warn("pipeline: listing the pipelines for the schedules", "err", err)
		return
	}
	now := e.now()
	for _, p := range ps {
		if p.Err != nil || p.Spec.Trigger.On != TriggerSchedule || p.Spec.Trigger.Schedule == nil {
			continue
		}
		slot := p.Spec.Trigger.Schedule.Last(now, e.cfg.Location)
		if slot.IsZero() || now.Sub(slot) > window {
			continue
		}
		key := "schedule:" + slot.UTC().Format(time.RFC3339)
		input := map[string]any{"slot": slot.UTC().Format(time.RFC3339)}
		if _, _, err := e.Start(StartRequest{Path: p.Path, Trigger: TriggerSchedule, Input: input, Actor: "schedule", IdemKey: key}); err != nil {
			e.log.Warn("pipeline: a schedule run", "pipeline", p.Path, "err", err)
		}
	}
}

// StartSchedules ticks the schedule triggers every minute until ctx ends.
func (e *Engine) StartSchedules(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		e.TickSchedules(10 * time.Minute)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Pipelines lists the pipelines of project, or of every project for "".
func (e *Engine) Pipelines(project string) ([]Pipeline, error) { return e.host.Pipelines(project) }
