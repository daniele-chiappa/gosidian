package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"sort"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/gosidian/gosidian/internal/jobs"
)

// Env is what a node sees while it runs.
type Env struct {
	Run     jobs.Run
	Node    *Node
	Project string
	// Scope is what its expressions read: trigger, run, the outputs of
	// the nodes before it (and item, index inside an each).
	Scope  map[string]any
	Engine *Engine
	Log    *slog.Logger
	// tokens are the LLM tokens the run used before this node.
	tokens int
}

// Type is a kind of node: it gets its settings with the expressions
// resolved, and returns its output, a JSON value.
type Type interface {
	Run(ctx context.Context, env *Env, with map[string]any) (any, error)
}

// TypeFunc is a Type as a function.
type TypeFunc func(ctx context.Context, env *Env, with map[string]any) (any, error)

// Run calls f.
func (f TypeFunc) Run(ctx context.Context, env *Env, with map[string]any) (any, error) {
	return f(ctx, env, with)
}

// Registry holds the node types.
type Registry struct {
	types map[string]Type
	ext   Extension
}

// Extension adds node types the registry looks up by name: the plugins'
// (IMP-168), which come and go while the server runs.
type Extension interface {
	Known(use string) bool
	Type(use string) (Type, bool)
}

// Extend adds the node types of x, after the base ones. Call it before
// the engine runs.
func (r *Registry) Extend(x Extension) { r.ext = x }

// NewRegistry is a registry with the base node types.
func NewRegistry() *Registry {
	r := &Registry{types: map[string]Type{}}
	r.Register("note.read", TypeFunc(noteRead))
	r.Register("note.write", TypeFunc(noteWrite))
	r.Register("wait.call", TypeFunc(waitCall))
	r.Register("starlark", TypeFunc(runStarlark))
	r.Register("http.request", TypeFunc(httpRequest))
	r.Register("llm.chat", TypeFunc(llmChat))
	r.Register("llm.describe", TypeFunc(llmDescribe))
	r.Register("in.list", TypeFunc(inList))
	r.Register("in.move", TypeFunc(inMove))
	return r
}

// Register adds or replaces a node type.
func (r *Registry) Register(name string, t Type) { r.types[name] = t }

// Known says whether a node type exists.
func (r *Registry) Known(use string) bool {
	if _, ok := r.types[use]; ok {
		return true
	}
	return r.ext != nil && r.ext.Known(use)
}

// Why says why use is no node type, when an extension knows (a plugin
// whose manifest is not accepted yet).
func (r *Registry) Why(use string) string {
	if h, ok := r.ext.(interface{ Why(use string) string }); ok {
		return h.Why(use)
	}
	return ""
}

// Names lists the base node types.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.types))
	for n := range r.types {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (r *Registry) get(use string) (Type, bool) {
	if t, ok := r.types[use]; ok {
		return t, true
	}
	if r.ext != nil {
		return r.ext.Type(use)
	}
	return nil, false
}

// WaitFor is returned by a node that suspends the run until a call
// resumes it (wait.call), or Until passes.
type WaitFor struct{ Until time.Time }

func (WaitFor) Error() string { return "waiting" }

// RetryAfter is returned by a node that knows when to try again (a
// service answering 429 or 503).
type RetryAfter struct {
	After time.Duration
	Err   error
}

func (r RetryAfter) Error() string { return r.Err.Error() }
func (r RetryAfter) Unwrap() error { return r.Err }

// --- settings ---

func str(with map[string]any, key string) string {
	if v, ok := with[key]; ok && v != nil {
		return render(v)
	}
	return ""
}

func required(with map[string]any, key string) (string, error) {
	v := strings.TrimSpace(str(with, key))
	if v == "" {
		return "", fmt.Errorf("with.%s is required", key)
	}
	return v, nil
}

func number(with map[string]any, key string, def float64) float64 {
	switch t := with[key].(type) {
	case float64:
		return t
	case int:
		return float64(t)
	}
	return def
}

// projectPath resolves a path a node names: inside the run's project. A
// path that does not start with the project is relative to it.
func (env *Env) projectPath(p string) (string, error) {
	p = strings.TrimSpace(strings.TrimPrefix(p, "/"))
	if p == "" {
		return "", errors.New("an empty path")
	}
	if !strings.HasPrefix(p, env.Project+"/") {
		p = env.Project + "/" + p
	}
	clean := path.Clean(p)
	if clean != p || strings.Contains(p, "..") {
		return "", fmt.Errorf("path %q: it must be a plain path inside the project", p)
	}
	return clean, nil
}

// ReadAttachment reads an attachment the node names, inside the run's
// project.
func (env *Env) ReadAttachment(p string) (string, []byte, error) {
	rel, err := env.projectPath(p)
	if err != nil {
		return "", nil, err
	}
	data, err := env.Engine.host.ReadAttachment(env.Project, rel)
	return rel, data, err
}

// StoreAttachment stores a file a node made as an attachment of the run's
// project, as the pipeline; it starts no ingest pipeline. name gives the
// extension. It returns the attachment's vault path.
func (env *Env) StoreAttachment(name string, data []byte) (string, error) {
	return env.Engine.host.StoreAttachment(env.Project, name, data, Actor(env.Run.Ref))
}

// --- note.read, note.write ---

// NoteData is a note as note.read returns it.
type NoteData struct {
	Path        string         `json:"path"`
	Title       string         `json:"title"`
	Frontmatter map[string]any `json:"frontmatter,omitempty"`
	Body        string         `json:"body"`
	ETag        string         `json:"etag"`
}

func noteRead(_ context.Context, env *Env, with map[string]any) (any, error) {
	p, err := required(with, "path")
	if err != nil {
		return nil, err
	}
	rel, err := env.projectPath(p)
	if err != nil {
		return nil, err
	}
	return env.Engine.host.ReadNote(env.Project, rel)
}

// WriteMode is how note.write treats an existing note.
type WriteMode string

const (
	WriteUpsert  WriteMode = "upsert"
	WriteCreate  WriteMode = "create"
	WriteReplace WriteMode = "replace"
)

// WriteResult is note.write's output.
type WriteResult struct {
	Path    string `json:"path"`
	ETag    string `json:"etag"`
	Created bool   `json:"created"`
}

func noteWrite(_ context.Context, env *Env, with map[string]any) (any, error) {
	p, err := required(with, "path")
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
	mode := WriteMode(str(with, "mode"))
	switch mode {
	case "":
		mode = WriteUpsert
	case WriteUpsert, WriteCreate, WriteReplace:
	default:
		return nil, fmt.Errorf("with.mode %q: upsert, create or replace", mode)
	}
	fm := map[string]any{}
	if m, ok := with["frontmatter"].(map[string]any); ok {
		for k, v := range m {
			fm[k] = v
		}
	}
	if t := str(with, "title"); t != "" {
		fm["title"] = t
	}
	// Where the note comes from (ADR-035): the pipeline, the run and, for
	// a document that arrived, the document.
	fm["pipeline"] = "[[" + strings.TrimSuffix(env.Run.Ref, ".md") + "]]"
	fm["pipeline_run"] = env.Run.ID
	if src, _ := env.Scope["trigger"].(map[string]any); src != nil {
		if f, _ := src["file"].(string); f != "" {
			fm["source"] = "[[" + f + "]]"
		}
	}
	content, err := withFrontmatter(fm, str(with, "body"))
	if err != nil {
		return nil, err
	}
	return env.Engine.host.WriteNote(env.Project, rel, content, mode, Actor(env.Run.Ref))
}

// withFrontmatter is a note: the frontmatter as YAML, then the body.
func withFrontmatter(fm map[string]any, body string) ([]byte, error) {
	y, err := yaml.Marshal(fm)
	if err != nil {
		return nil, fmt.Errorf("frontmatter: %w", err)
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(y)
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimLeft(body, "\n"))
	if !strings.HasSuffix(body, "\n") {
		b.WriteString("\n")
	}
	return []byte(b.String()), nil
}

// Actor is who a pipeline writes as, in the audit.
func Actor(pipelinePath string) string { return "pipeline:" + pipelinePath }

// --- wait.call ---

func waitCall(_ context.Context, env *Env, with map[string]any) (any, error) {
	d := DefaultWaitTime
	if s := str(with, "timeout"); s != "" {
		v, err := time.ParseDuration(s)
		if err != nil || v <= 0 {
			return nil, fmt.Errorf("with.timeout %q is a duration (30m, 24h)", s)
		}
		d = v
	}
	if max := env.Engine.cfg.MaxWait; max > 0 && d > max {
		return nil, fmt.Errorf("with.timeout %s is beyond the longest wait, %s", d, max)
	}
	return nil, WaitFor{Until: env.Engine.now().Add(d)}
}
