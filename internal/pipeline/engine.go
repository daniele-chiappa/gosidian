package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/gosidian/gosidian/internal/clock"
	"github.com/gosidian/gosidian/internal/infolder"
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
	// ReadAttachment reads an attachment of the project, or a file of its
	// IN folder.
	ReadAttachment(project, path string) ([]byte, error)
	// StatFile describes an attachment of the project, or a file of its IN
	// folder, with its SHA-256; OpenFile opens one for reading (a plugin
	// fetching it by reference).
	StatFile(project, path string) (FileStat, error)
	OpenFile(project, path string) (io.ReadSeekCloser, FileStat, error)
	// StoreAttachment stores data as an attachment of the project, as the
	// pipeline (actor), named after its content with name's extension. It
	// starts no ingest pipeline: a run must not start runs on the files it
	// makes.
	StoreAttachment(project, name string, data []byte, actor string) (string, error)
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
	// APIKeyEnv names the environment variable with the key instead.
	APIKeyEnv string
	Model     string // the default model
	// Resources are the shared resources its calls take a place in, as
	// [[pipelines.resources]] names them (IMP-196).
	Resources []string
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
	// MaxRunDepth is how many runs may start each other in a chain
	// through pipeline.run; default 4. MaxRunChildren is how many runs
	// one run may start through it; default 1000.
	MaxRunDepth    int
	MaxRunChildren int
	// MaxListItems caps what a node lists or is asked for in one call:
	// a page of note.list, note.sections' only; default 5000.
	MaxListItems int
	// MaxChunkMeta caps the meta of a chunk of note.sections, in bytes of
	// JSON; default 16 KiB.
	MaxChunkMeta int
	// HTTPAllowlist holds the URL prefixes http.request and the
	// callbacks may call; empty: none.
	HTTPAllowlist []string
	// PublicURL is the server's address for the resume URLs
	// ({{ run.resume_url }}); "" gives a path.
	PublicURL string
	Providers []Provider
	// Location is the zone of the schedule triggers; nil: local.
	Location *time.Location
	// FileTicketTTL is how long a plugin may fetch a file it got by
	// reference; default 15 minutes (the server's step timeout).
	FileTicketTTL time.Duration
	// MaxSyncWait is the longest a start may wait for its run to end, over
	// HTTP or MCP; default 60 seconds.
	MaxSyncWait time.Duration
	// RunTokenRate is how many runs one run token starts in a minute, and
	// how many resumes one run's key makes; default 60. A chat's widget
	// starts a run per message of everyone who chats with it.
	RunTokenRate int
	// Categories are the rules of the categories of pipelines (IMP-196,
	// [[pipelines.categories]]); a category without rules has no limits.
	Categories []Category
	// Tasks are the limits of the teams' tasks (IMP-195, [agents]).
	Tasks TaskConfig
}

// Category is the rules of a category of pipelines (IMP-196).
type Category struct {
	Name string
	// MaxRunning is the steps of its runs running at once; 0: no limit.
	MaxRunning int
	// MaxRuns is its runs not ended: beyond, a start from outside answers
	// ErrCategoryFull; 0: no limit.
	MaxRuns int
	// Replace cancels, when a run starts, the runs of the same pipeline
	// with the same input still queued.
	Replace bool
	// Priority is interactive, normal ("") or low.
	Priority string
	// Wait is how long a start that says nothing waits for its run; at
	// most MaxSyncWait.
	Wait time.Duration
	// Projects are the projects whose pipelines may name it; none: all.
	Projects []string
}

// ErrCategoryFull refuses a start whose category has its most runs not
// ended (IMP-196): try again after RetryAfter.
type ErrCategoryFull struct {
	Category   string
	Runs       int
	RetryAfter time.Duration
}

func (e ErrCategoryFull) Error() string {
	return fmt.Sprintf("the category %s has %d runs not ended, its most ([[pipelines.categories]] max_runs): try again in a while", e.Category, e.Runs)
}

// categoryFullRetry is the Retry-After of a start refused by its category.
const categoryFullRetry = 30 * time.Second

// Category is the rules of category name, ok false when the server has
// none for it.
func (e *Engine) Category(name string) (Category, bool) {
	for _, c := range e.cfg.Categories {
		if c.Name == name {
			return c, true
		}
	}
	return Category{Name: name}, false
}

// DefaultWait is how long a start of the pipeline at path waits when it
// says nothing: its category's wait, at most MaxSyncWait.
func (e *Engine) DefaultWait(path string) time.Duration {
	p, err := e.host.Pipeline(path)
	if err != nil || p.Err != nil || p.Spec == nil {
		return 0
	}
	c, _ := e.Category(p.Spec.CategoryOf())
	return e.SyncWait(c.Wait)
}

// MaxOutputBytes is a step's output in bytes ([pipelines]
// max_output_bytes): the longest section note.sections cuts, which the cut
// view takes as its own.
func (e *Engine) MaxOutputBytes() int { return e.cfg.MaxOutputBytes }

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
	// in is the state of the IN folders (SetIN), nil when none.
	in *infolder.Service
	// tickets are the files the plugins fetch by reference.
	tickets fileTickets
	// commits says whether the vault's git sync makes commits, which start
	// the commit pipelines (SetCommits); nil: it does not.
	commits func() bool
	// committer makes vault.commit's commits (SetCommitter); nil: git
	// sync is off.
	committer Committer
	// waits are the callers waiting for a run to change (Wait).
	waits waiters
	// streams are the runs followed as they write (StartStream).
	streams streams
	// queueLong are the queue triggers whose queue is long, since when.
	queueLong queueEpisodes
	// tasks is the state of the task trigger (IMP-195).
	tasks taskLoop
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
	for _, n := range []struct {
		dst *int
		def int
	}{{&cfg.MaxRunDepth, 4}, {&cfg.MaxRunChildren, 1000}, {&cfg.MaxListItems, 5000}, {&cfg.MaxChunkMeta, 16 << 10}} {
		if *n.dst <= 0 {
			*n.dst = n.def
		}
	}
	if cfg.Location == nil {
		cfg.Location = time.Local
	}
	if cfg.FileTicketTTL <= 0 {
		cfg.FileTicketTTL = 15 * time.Minute
	}
	if cfg.MaxSyncWait <= 0 {
		cfg.MaxSyncWait = time.Minute
	}
	if cfg.RunTokenRate <= 0 {
		cfg.RunTokenRate = 60
	}
	if logger == nil {
		logger = slog.Default()
	}
	e := &Engine{host: host, runner: runner, types: NewRegistry(), cfg: cfg, secrets: secrets, log: logger,
		now: func() time.Time { return time.Now().UTC() }}
	e.tasks.kick = make(chan struct{}, 1)
	e.client = guardedClient(e)
	runner.Register(Kind, e)
	runner.SetOnChange(func(id string) {
		e.waits.notify(id)
		// The runner runs the automations' actions too (IMP-166): the web
		// UI hears only of the pipelines'.
		if run, err := runner.Store().Run(id); err == nil && run.Kind == Kind {
			host.Changed(run)
		}
	})
	return e
}

// waiters are the callers waiting for runs to change, each with a channel
// that a change of its run wakes.
type waiters struct {
	mu sync.Mutex
	m  map[string][]chan struct{}
}

func (w *waiters) add(id string) chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.m == nil {
		w.m = map[string][]chan struct{}{}
	}
	ch := make(chan struct{}, 1)
	w.m[id] = append(w.m[id], ch)
	return ch
}

func (w *waiters) remove(id string, ch chan struct{}) {
	w.mu.Lock()
	defer w.mu.Unlock()
	list := w.m[id]
	for i, c := range list {
		if c == ch {
			list = append(list[:i], list[i+1:]...)
			break
		}
	}
	if len(list) == 0 {
		delete(w.m, id)
	} else {
		w.m[id] = list
	}
}

func (w *waiters) notify(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, ch := range w.m[id] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Wait returns the run once it ended or waits (a wait.call), d passed, or
// ctx ended: as it stands then. A run that waits only for the runs it
// started (pipeline.run) is still at work: Wait goes on. A change of the
// run wakes it, and it reads the run again every second besides.
func (e *Engine) Wait(ctx context.Context, id string, d time.Duration) (jobs.Run, error) {
	ch := e.waits.add(id)
	defer e.waits.remove(id, ch)
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		run, err := e.runner.Store().Run(id)
		if err != nil || e.Settled(run) {
			return run, err
		}
		select {
		case <-ctx.Done():
			return run, nil
		case <-deadline.C:
			return e.runner.Store().Run(id)
		case <-ch:
		case <-tick.C:
		}
	}
}

// Settled says whether a start that waits returns the run as it is: it
// ended, or it waits for a call from outside (wait.call). One that waits
// only for the runs it started is still at work.
func (e *Engine) Settled(run jobs.Run) bool {
	return run.Status.Terminal() || run.Status == jobs.Waiting && !e.waitsForRuns(run)
}

// waitsForRuns says whether every step the run waits at is a
// pipeline.run's, which waits for a run it started: one the runner ends
// by itself, unlike a wait.call's, which waits for a call from outside. A
// run that waits with no step waiting is between the two: its child ended
// and the step was taken again, the run's status not yet; it is at work.
func (e *Engine) waitsForRuns(run jobs.Run) bool {
	spec, err := SpecOf(run)
	if err != nil {
		return false
	}
	steps, err := e.runner.Store().Steps(run.ID)
	if err != nil {
		return false
	}
	for _, st := range steps {
		if st.Status != jobs.Waiting {
			continue
		}
		if n, ok := spec.Node(st.Name); !ok || n.Use != "pipeline.run" {
			return false
		}
	}
	return true
}

// SyncWait is how long a start that asks to wait w waits: w capped by
// [pipelines] max_sync_wait; 0 for none.
func (e *Engine) SyncWait(w time.Duration) time.Duration {
	if w <= 0 {
		return 0
	}
	return min(w, e.cfg.MaxSyncWait)
}

// AnswerOf is the output of the node the run's pipeline names as its
// answer, once that node is done: ok is false when it names none, or the
// node did not end done.
func AnswerOf(run jobs.Run, steps []jobs.Step) (answer any, ok bool) {
	spec, err := SpecOf(run)
	if err != nil || spec.Answer == "" {
		return nil, false
	}
	for _, st := range steps {
		if st.Name == spec.Answer && st.Status == jobs.Done {
			return decode(st.Output), true
		}
	}
	return nil, false
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
	Trigger string // manual, external, ingest, schedule, in, commit, run, pipeline
	Input   any    // the trigger's payload, a JSON value
	Actor   string // who started it: an account, a token, the server
	// IdemKey starts the run once: the same key again gives the run
	// it started.
	IdemKey     string
	CallbackURL string
	// Priority is the least priority of the run (jobs.Priority…): a run
	// that an interactive one starts is interactive too. An interactive
	// pipeline's runs are interactive whatever it says.
	Priority int
	// stream follows the run as its nodes write (StartStream, and the
	// children of a run that has one).
	stream *Stream
	// id, task and team: the run the task trigger starts for a task it
	// took for it (IMP-195), with the id it took it with.
	id, task, team string
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
	case TriggerManual, TriggerPipeline:
	case TriggerExternal:
		switch p.Spec.Trigger.On {
		case TriggerExternal, TriggerManual:
		case TriggerChat:
			// A run token of a chat pipeline starts a chat run.
			req.Trigger = TriggerChat
		default:
			return jobs.Run{}, false, fmt.Errorf("the pipeline starts on %s, not from outside", p.Spec.Trigger.On)
		}
	case TriggerChat:
		if p.Spec.Trigger.On != TriggerChat {
			return jobs.Run{}, false, fmt.Errorf("the pipeline starts on %s, not on chat", p.Spec.Trigger.On)
		}
	case TriggerIngest, TriggerSchedule, TriggerIN, TriggerCommit, TriggerRun, TriggerQueue, TriggerTask:
	default:
		return jobs.Run{}, false, fmt.Errorf("unknown trigger %q", req.Trigger)
	}
	// A chat pipeline started by hand gets a chat's input as well: a
	// manual test is a message.
	if req.Trigger == TriggerChat || (p.Spec.Trigger.On == TriggerChat && req.Trigger == TriggerManual) {
		in, err := CheckChatInput(req.Input)
		if err != nil {
			return jobs.Run{}, false, err
		}
		req.Input = in
	}
	// The category (IMP-196): reserved to some projects, it sets the
	// priority, interactive staying so for the runs an interactive one
	// starts, as before.
	cat, _ := e.Category(p.Spec.CategoryOf())
	if len(cat.Projects) > 0 && !slices.Contains(cat.Projects, p.Project) {
		return jobs.Run{}, false, fmt.Errorf("the category %s is not open to project %s ([[pipelines.categories]] projects)", cat.Name, p.Project)
	}
	priority := jobs.PriorityNormal
	switch cat.Priority {
	case CategoryLow:
		priority = jobs.PriorityLow
	case PriorityInteractive:
		priority = jobs.PriorityInteractive
	}
	if p.Spec.Interactive() || req.Priority == jobs.PriorityInteractive {
		priority = jobs.PriorityInteractive
	}
	// The key of a run pipeline.run starts names the run its end resumes
	// (endedChild): only the engine gives such keys, or anyone could end
	// another run's waiting step.
	if req.Trigger != TriggerPipeline && (strings.HasPrefix(req.IdemKey, keyWaited) || strings.HasPrefix(req.IdemKey, keySpawned)) {
		return jobs.Run{}, false, fmt.Errorf("idempotency_key: %q and %q begin the keys of the runs pipeline.run starts", keyWaited, keySpawned)
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
	run := jobs.Run{
		Kind: Kind, Ref: p.Path, Project: p.Project, Trigger: req.Trigger, Input: input, Spec: spec,
		IdemKey: req.IdemKey, Actor: req.Actor, CallbackURL: req.CallbackURL, ResumeKey: jobs.NewSecret(), Priority: priority,
		Category: cat.Name, Task: req.task, Team: req.team,
	}
	if req.id != "" {
		run.ID = req.id
	}
	// A start from outside beyond the category's runs not ended is refused,
	// unless it is one already made (its key); an event (an arrival, a
	// commit, a schedule) and pipeline.run go to the queue, not lost.
	if cat.MaxRuns > 0 && fromOutside(req.Trigger) {
		again := false
		if req.IdemKey != "" {
			_, err := e.runner.Store().RunByIdem(p.Path, req.IdemKey)
			again = err == nil
		}
		if !again {
			n, err := e.runner.Store().Unended(cat.Name)
			if err != nil {
				return jobs.Run{}, false, err
			}
			if n >= cat.MaxRuns {
				return jobs.Run{}, false, ErrCategoryFull{Category: cat.Name, Runs: n, RetryAfter: categoryFullRetry}
			}
		}
	}
	if req.stream != nil {
		// The stream is there before the run can start: its first node
		// may be the one that writes.
		run.ID = jobs.NewID()
		e.streams.attach(run.ID, req.stream)
	}
	stored, existing, err := e.runner.Submit(run, steps)
	if req.stream != nil && (err != nil || existing) {
		e.streams.detach(run.ID)
		if err == nil && !stored.Status.Terminal() && e.streams.of(stored.ID) == nil {
			e.streams.attach(stored.ID, req.stream)
		}
	}
	if err == nil && !existing && cat.Replace {
		e.replaceTwins(stored)
	}
	return stored, existing, err
}

// fromOutside says whether a run of trigger was started from outside (by
// hand, over HTTP or MCP, a run token, a chat), which a full category may
// refuse.
func fromOutside(trigger string) bool {
	switch trigger {
	case TriggerManual, TriggerExternal, TriggerChat:
		return true
	}
	return false
}

// replaceTwins cancels the runs of run's pipeline with its input still
// queued, no step of theirs started (IMP-196, a category's replace): the
// newest start stands for them.
func (e *Engine) replaceTwins(run jobs.Run) {
	ids, err := e.runner.Store().QueuedTwins(run.Ref, run.Input, run.ID)
	if err != nil {
		e.log.Warn("pipeline: the runs a start replaces", "pipeline", run.Ref, "err", err)
		return
	}
	for _, id := range ids {
		if err := e.runner.CancelFor(id, "replaced by run "+run.ID); err != nil && !errors.Is(err, jobs.ErrState) {
			e.log.Warn("pipeline: replacing a queued run", "run", id, "err", err)
		}
	}
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
			// When it started, for a report that dates what it writes: the
			// time of the notes' zone, with its offset.
			"created": clock.NoteTime(run.Created),
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
	if ws, ok := e.host.(WriteSyncer); ok {
		if serr := ws.SyncWrites(); serr != nil && err == nil {
			err = fmt.Errorf("bringing the step's writes to disk: %w", serr)
		}
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
		var fw FailedWith
		if errors.As(err, &fw) {
			if b, merr := json.Marshal(fw.Output); merr == nil && len(b) <= e.cfg.MaxOutputBytes {
				// What a failed step spent counts too (IMP-195).
				e.addTaskCost(run, b)
				return jobs.Outcome{Status: jobs.Failed, Error: err.Error(), Output: b}
			}
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
	e.addTaskCost(run, b)
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
				if node.Use == "pipeline.run" {
					// It waits for this element's run, whose end runs the
					// node again from the first element (IMP-201).
					return nil, err
				}
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
	// A node skipped, canceled or failed here can decide the nodes waiting
	// on it at once: the passes go on until one decides none of those, or
	// a run whose last step was skipped would stop with its next ones
	// pending (BUG-126). A queued node decides nothing until it ran.
	decided := map[string]bool{}
	for pass := 0; pass <= len(steps); pass++ {
		released := false
		for _, st := range steps {
			if st.Status != jobs.Pending || decided[st.Name] {
				continue
			}
			c, ok := e.decide(run, spec, st.Name, byName, continues, sc, now)
			if !ok {
				continue
			}
			changes = append(changes, c)
			decided[st.Name] = true
			ns := byName[st.Name]
			ns.Status = c.Status
			byName[st.Name] = ns
			// The nodes decided after it in this call read it as a later
			// call would (scope): a skipped or failed node gives nothing,
			// it is not unknown.
			if c.Status == jobs.Skipped || c.Status == jobs.Failed {
				sc[st.Name] = nil
			}
			if c.Status != jobs.Queued {
				released = true
			}
		}
		if !released {
			break
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

// decide is what becomes of a pending node, given the steps it waits for:
// queued, skipped, canceled or failed; false while it must wait.
func (e *Engine) decide(run jobs.Run, spec *Spec, name string, byName map[string]jobs.Step, continues func(string) bool, sc scope, now time.Time) (jobs.StepChange, bool) {
	node, ok := spec.Node(name)
	if !ok {
		return jobs.StepChange{Name: name, Status: jobs.Canceled, Error: "not in the pipeline", Finished: now}, true
	}
	ready, blocked, allSkipped := true, "", len(node.After) > 0
	for _, d := range node.After {
		switch byName[d].Status {
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
		return jobs.StepChange{Name: name, Status: jobs.Canceled, Error: blocked + " did not end well", Finished: now}, true
	case !ready:
		return jobs.StepChange{}, false
	case allSkipped:
		return jobs.StepChange{Name: name, Status: jobs.Skipped, Finished: now}, true
	}
	skip, err := e.skips(node, sc)
	switch {
	case err != nil:
		return jobs.StepChange{Name: name, Status: jobs.Failed, Error: err.Error(), Finished: now}, true
	case skip:
		return jobs.StepChange{Name: name, Status: jobs.Skipped, Finished: now}, true
	}
	return jobs.StepChange{Name: name, Status: jobs.Queued, Resources: e.resources(run, node, sc)}, true
}

// resources are what the step of node takes a place in while it runs
// (IMP-196): the category of its run, and the provider of language models
// of an llm node or the plugin of a plugin's node, with the shared
// resources the server's config gives them. The provider is the one with
// names when the step is queued, or the default one.
func (e *Engine) resources(run jobs.Run, node *Node, sc scope) []string {
	var out []string
	if run.Category != "" {
		out = append(out, ResourceCategory+run.Category)
	}
	// The agents of a team's task (IMP-195): its team's places and the
	// installation's.
	if run.Team != "" && (e.agentNode(node) || node.Use == "task.run") {
		out = append(out, ResourceTeam+strings.TrimSuffix(run.Team, ".md"), ResourceAgents)
	}
	if node.Use == "task.run" || node.Use == "task.pr" {
		// The executor's plugin, which the node calls.
		return append(out, e.executorResources(run)...)
	}
	return append(out, e.nodeResources(node, sc)...)
}

func (e *Engine) nodeResources(node *Node, sc scope) []string {
	if strings.HasPrefix(node.Use, "llm.") {
		name := ""
		if v, ok := node.With["provider"]; ok {
			if x, err := sc.eval(v); err == nil {
				name, _ = x.(string)
			}
		}
		p, err := e.provider(strings.TrimSpace(name))
		if err != nil {
			return nil
		}
		out := []string{ResourceLLM + p.Name}
		for _, r := range p.Resources {
			out = append(out, ResourceShared+r)
		}
		return out
	}
	if _, base := e.types.types[node.Use]; base {
		return nil
	}
	if x, ok := e.types.ext.(interface{ Resources(use string) []string }); ok {
		return x.Resources(node.Use)
	}
	return nil
}

// The kinds of resource a step takes a place in (IMP-196).
const (
	ResourcePlugin   = "plugin:"
	ResourceLLM      = "llm:"
	ResourceShared   = "res:"
	ResourceCategory = "cat:"
)

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

// Ended writes the day's line of the pipeline's runs note, calls the
// run's callback and starts the pipelines on run that take its end
// (jobs.Executor).
func (e *Engine) Ended(run jobs.Run, steps []jobs.Step) {
	e.tickets.dropRun(run.ID)
	if e.in != nil {
		// The files the run took, and left working, are done or not.
		if err := e.in.RunEnded(run.ID, run.Status == jobs.Done); err != nil {
			e.log.Warn("pipeline: the IN files of a run", "run", run.ID, "err", err)
		}
	}
	if err := e.summarize(run); err != nil {
		e.log.Warn("pipeline: the runs note", "run", run.ID, "pipeline", run.Ref, "err", err)
	}
	if run.CallbackURL != "" {
		if err := e.callback(run, steps); err != nil {
			e.log.Warn("pipeline: the callback", "run", run.ID, "url", run.CallbackURL, "err", err)
		}
	}
	// pipeline.run (IMP-186): a parent waits for this run, and this run's
	// own children wait for nobody once it ended without them.
	e.endedChild(run, steps)
	if run.Status != jobs.Done {
		e.stopChildren(run)
	}
	e.startOnRun(run, steps)
	e.taskRunEnded(run)
	e.forgetRunWrites(run.ID)
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
		// The key stays in UTC whatever the zones: a key that changed with
		// TZ would start the slot's run a second time.
		key := "schedule:" + slot.UTC().Format(time.RFC3339)
		input := map[string]any{"slot": clock.NoteTime(slot)}
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
		e.TickQueues()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Pipelines lists the pipelines of project, or of every project for "".
func (e *Engine) Pipelines(project string) ([]Pipeline, error) { return e.host.Pipelines(project) }
