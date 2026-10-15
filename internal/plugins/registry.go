package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/gosidian/gosidian/internal/pipeline"
)

// Config registers a plugin ([[pipelines.plugins]]).
type Config struct {
	Name string
	URL  string // the MCP endpoint (Streamable HTTP)
	// TokenSecret names the secret with its bearer token; "" for none.
	TokenSecret string
	// Timeout caps a call; 0: the step's own timeout.
	Timeout time.Duration
	// FilesURL is the server's address as the plugin reaches it, for the
	// files a node with file_refs fetches; "" is the engine's PublicURL.
	FilesURL string
}

// Secrets resolves a secret's name to its value.
type Secrets interface {
	Get(name string) (string, error)
}

// The status of a plugin.
const (
	StatusOK         = "ok"         // up, serving the accepted manifest
	StatusUnaccepted = "unaccepted" // up, its manifest not accepted yet
	StatusChanged    = "changed"    // up, serving another manifest than the accepted one
	StatusDown       = "down"       // unreachable, or serving no valid manifest
	StatusUnknown    = "unknown"    // not checked yet
)

// checkTimeout caps a health check.
const checkTimeout = 20 * time.Second

// reserved are no plugin's name: the base node types' prefixes, and memory,
// whose tools (memory_*) a plugin's would collide with (<plugin>_<tool>).
var reserved = map[string]bool{"note": true, "wait": true, "starlark": true, "http": true, "llm": true, "pipeline": true, "in": true, "memory": true}

// State is what the server knows of a plugin.
type State struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	// Version and SHA are the live manifest's.
	Version string `json:"version,omitempty"`
	SHA     string `json:"sha,omitempty"`
	// Nodes are the node types the pipelines may use: the accepted
	// manifest's.
	Nodes    []NodeInfo `json:"nodes,omitempty"`
	Accepted *Accepted  `json:"accepted,omitempty"`
	Checked  time.Time  `json:"checked,omitempty"`
	// Live is the manifest the plugin serves (show, accept).
	Live *Manifest `json:"-"`
}

// NodeInfo names a node type of a plugin.
type NodeInfo struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

type liveState struct {
	manifest *Manifest
	sha      string
	err      error
	checked  time.Time
}

// Registry is the registered plugins: their accepted manifests, their
// last health check, and the pipeline.Extension of their node types.
type Registry struct {
	plugins map[string]Config
	order   []string
	store   *store
	secrets Secrets
	version string
	log     *slog.Logger

	mu   sync.Mutex
	live map[string]liveState
}

// New checks the registrations and opens the accepted manifests of
// stateDir. version is the server's, told to the plugins.
func New(cfgs []Config, stateDir string, sec Secrets, version string, logger *slog.Logger) (*Registry, error) {
	if logger == nil {
		logger = slog.Default()
	}
	r := &Registry{plugins: map[string]Config{}, store: newStore(filepath.Join(stateDir, FileName)),
		secrets: sec, version: version, log: logger, live: map[string]liveState{}}
	for _, c := range cfgs {
		switch {
		case !ValidName(c.Name):
			return nil, fmt.Errorf("plugin %q: a name of lower-case letters, digits and -, starting with a letter", c.Name)
		case reserved[c.Name]:
			return nil, fmt.Errorf("plugin %q: a name gosidian keeps (the base node types, memory)", c.Name)
		case r.plugins[c.Name].Name != "":
			return nil, fmt.Errorf("plugin %q registered twice", c.Name)
		}
		u, err := url.Parse(c.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, fmt.Errorf("plugin %q: url %q is not an http(s) address", c.Name, c.URL)
		}
		r.plugins[c.Name] = c
		r.order = append(r.order, c.Name)
	}
	return r, nil
}

// Names lists the registered plugins, in the order of the config.
func (r *Registry) Names() []string { return append([]string(nil), r.order...) }

// Start checks the plugins now and every interval until ctx ends.
func (r *Registry) Start(ctx context.Context, every time.Duration) {
	r.Check(ctx)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.Check(ctx)
		}
	}
}

// Check fetches every plugin's manifest, at once.
func (r *Registry) Check(ctx context.Context) {
	var wg sync.WaitGroup
	for _, name := range r.order {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, checkTimeout)
			defer cancel()
			m, err := r.Fetch(cctx, name)
			if ctx.Err() != nil {
				return
			}
			ls := liveState{manifest: m, err: err, checked: time.Now().UTC()}
			if m != nil {
				ls.sha = m.SHA()
			}
			r.mu.Lock()
			before := r.live[name]
			r.live[name] = ls
			r.mu.Unlock()
			if (before.err == nil) != (err == nil) || before.sha != ls.sha {
				if err != nil {
					r.log.Warn("plugin: unreachable", "plugin", name, "err", err)
				} else {
					r.log.Info("plugin: up", "plugin", name, "version", m.Version, "sha", ls.sha[:12])
				}
			}
		}(name)
	}
	wg.Wait()
}

// States is every plugin's state, in the order of the config.
func (r *Registry) States() []State {
	out := make([]State, 0, len(r.order))
	for _, name := range r.order {
		s, _ := r.State(name)
		out = append(out, s)
	}
	return out
}

// State is a plugin's state.
func (r *Registry) State(name string) (State, bool) {
	c, ok := r.plugins[name]
	if !ok {
		return State{}, false
	}
	s := State{Name: name, URL: c.URL, Status: StatusUnknown}
	acc, err := r.store.all()
	if a, ok := acc[name]; ok {
		s.Accepted = &a
		for _, n := range a.Manifest.Nodes {
			s.Nodes = append(s.Nodes, NodeInfo{Type: name + "." + n.Name, Description: n.Description})
		}
	}
	r.mu.Lock()
	ls, checked := r.live[name]
	r.mu.Unlock()
	if checked {
		s.Checked, s.Live, s.SHA = ls.checked, ls.manifest, ls.sha
		if ls.manifest != nil {
			s.Version = ls.manifest.Version
		}
		switch {
		case ls.err != nil:
			s.Status, s.Error = StatusDown, ls.err.Error()
		case s.Accepted == nil:
			s.Status = StatusUnaccepted
		case s.Accepted.SHA != ls.sha:
			s.Status = StatusChanged
		default:
			s.Status = StatusOK
		}
	}
	if err != nil && s.Error == "" {
		s.Error = err.Error()
	}
	return s, true
}

// NodeSchema is a node type of an accepted manifest, with the schemas of
// its settings and output, for the pipeline editor (IMP-169, F4).
type NodeSchema struct {
	Type        string          `json:"type"`
	Plugin      string          `json:"plugin"`
	Status      string          `json:"status"`
	Description string          `json:"description,omitempty"`
	Files       []string        `json:"files,omitempty"`
	Input       json.RawMessage `json:"input_schema,omitempty"`
	Output      json.RawMessage `json:"output_schema,omitempty"`
}

// NodeSchemas lists the node types of the accepted manifests, in the
// order of the config, with each plugin's status.
func (r *Registry) NodeSchemas() []NodeSchema {
	var out []NodeSchema
	for _, s := range r.States() {
		if s.Accepted == nil {
			continue
		}
		for _, n := range s.Accepted.Manifest.Nodes {
			out = append(out, NodeSchema{Type: s.Name + "." + n.Name, Plugin: s.Name, Status: s.Status,
				Description: n.Description, Files: n.Files, Input: n.Input, Output: withFiles(n.Output)})
		}
	}
	return out
}

// withFiles adds to an object's output schema the files a plugin node
// gives back, which gosidian stores as attachments of the project.
func withFiles(out json.RawMessage) json.RawMessage {
	var o map[string]any
	if len(out) == 0 || json.Unmarshal(out, &o) != nil {
		o = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	props, ok := o["properties"].(map[string]any)
	if !ok {
		if t, _ := o["type"].(string); t != "object" && t != "" {
			return out
		}
		props = map[string]any{}
		o["properties"] = props
	}
	if _, has := props["files"]; !has {
		props["files"] = map[string]any{"type": "array", "description": "The files it gave back, stored as attachments",
			"items": map[string]any{"type": "object", "properties": map[string]any{
				"name": map[string]any{"type": "string"}, "path": map[string]any{"type": "string"},
				"mime": map[string]any{"type": "string"}, "markdown": map[string]any{"type": "string"}}}}
	}
	b, err := json.Marshal(o)
	if err != nil {
		return out
	}
	return b
}

// Fetch asks a plugin for its manifest.
func (r *Registry) Fetch(ctx context.Context, name string) (*Manifest, error) {
	c, ok := r.plugins[name]
	if !ok {
		return nil, fmt.Errorf("no plugin %q in [[pipelines.plugins]]", name)
	}
	sess, err := r.connect(ctx, c)
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	return fetchManifest(ctx, sess, name)
}

// Accept fetches a plugin's manifest and accepts it, when its SHA starts
// with sha (at least 8 characters, from gosidian plugin show): the
// owner accepts what they read.
func (r *Registry) Accept(ctx context.Context, name, sha, by string) (*Accepted, error) {
	if len(sha) < 8 {
		return nil, errors.New("give at least the first 8 characters of the manifest's sha (gosidian plugin show)")
	}
	m, err := r.Fetch(ctx, name)
	if err != nil {
		return nil, err
	}
	live := m.SHA()
	if !strings.HasPrefix(live, strings.ToLower(sha)) {
		return nil, fmt.Errorf("plugin %s serves the manifest %s now, not %s: read it again with gosidian plugin show %s", name, live[:12], sha, name)
	}
	a := &Accepted{SHA: live, Manifest: *m, At: time.Now().UTC(), By: by}
	if err := r.store.set(name, a); err != nil {
		return nil, err
	}
	return a, nil
}

// Forget drops a plugin's accepted manifest: its nodes stop.
func (r *Registry) Forget(name string) error {
	if _, ok := r.plugins[name]; !ok {
		return fmt.Errorf("no plugin %q in [[pipelines.plugins]]", name)
	}
	return r.store.set(name, nil)
}

// split reads "<plugin>.<node>".
func split(use string) (string, string, bool) {
	p, n, ok := strings.Cut(use, ".")
	return p, n, ok && p != "" && n != ""
}

// FilesURL is the address a plugin fetches the files it gets by reference
// from, when its config sets one; "" for the server's.
func (r *Registry) FilesURL(plugin string) string {
	return r.plugins[plugin].FilesURL
}

// accepted is the accepted manifest's node for use.
func (r *Registry) accepted(use string) (Config, Accepted, NodeSpec, bool) {
	p, n, ok := split(use)
	if !ok {
		return Config{}, Accepted{}, NodeSpec{}, false
	}
	c, ok := r.plugins[p]
	if !ok {
		return Config{}, Accepted{}, NodeSpec{}, false
	}
	acc, _ := r.store.all()
	a, ok := acc[p]
	if !ok {
		return Config{}, Accepted{}, NodeSpec{}, false
	}
	spec, ok := a.Manifest.Node(n)
	return c, a, spec, ok
}

// Known says whether use is a node of an accepted plugin manifest
// (pipeline.Extension).
func (r *Registry) Known(use string) bool {
	_, _, _, ok := r.accepted(use)
	return ok
}

// Type is the node type of use (pipeline.Extension).
func (r *Registry) Type(use string) (pipeline.Type, bool) {
	if !r.Known(use) {
		return nil, false
	}
	return node{r: r, use: use}, true
}

// Why says why use is no node type, when it names a plugin.
func (r *Registry) Why(use string) string {
	p, n, ok := split(use)
	if !ok || reserved[p] {
		return ""
	}
	if _, ok := r.plugins[p]; !ok {
		return fmt.Sprintf("no plugin %q is registered ([[pipelines.plugins]])", p)
	}
	acc, _ := r.store.all()
	a, ok := acc[p]
	if !ok {
		return fmt.Sprintf("the manifest of plugin %s is not accepted yet (gosidian plugin show %s, then accept)", p, p)
	}
	if _, ok := a.Manifest.Node(n); !ok {
		names := make([]string, 0, len(a.Manifest.Nodes))
		for _, nd := range a.Manifest.Nodes {
			names = append(names, p+"."+nd.Name)
		}
		sort.Strings(names)
		return fmt.Sprintf("plugin %s has no node %q (it has %s)", p, n, strings.Join(names, ", "))
	}
	return ""
}

// connect opens a session with a plugin.
func (r *Registry) connect(ctx context.Context, c Config) (*client.Client, error) {
	headers := map[string]string{}
	if c.TokenSecret != "" {
		if r.secrets == nil {
			return nil, fmt.Errorf("plugin %s: its token is the secret %q, and the server has no secrets key", c.Name, c.TokenSecret)
		}
		tok, err := r.secrets.Get(c.TokenSecret)
		if err != nil {
			return nil, fmt.Errorf("plugin %s: its token: %w", c.Name, err)
		}
		headers["Authorization"] = "Bearer " + tok
	}
	// No redirects: the token goes to the registered address only.
	hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	sess, err := client.NewStreamableHttpClient(c.URL, transport.WithHTTPHeaders(headers), transport.WithHTTPBasicClient(hc))
	if err != nil {
		return nil, fmt.Errorf("plugin %s: %w", c.Name, err)
	}
	if err := sess.Start(ctx); err != nil {
		sess.Close()
		return nil, fmt.Errorf("plugin %s: %w", c.Name, err)
	}
	var init mcp.InitializeRequest
	init.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	init.Params.ClientInfo = mcp.Implementation{Name: "gosidian", Version: r.version}
	if _, err := sess.Initialize(ctx, init); err != nil {
		sess.Close()
		return nil, fmt.Errorf("plugin %s: %w", c.Name, err)
	}
	return sess, nil
}

// session opens a session with a plugin serving its accepted manifest. The
// manifest is read again at every call: a plugin that changed it waits for
// the owner's new accept.
func (r *Registry) session(ctx context.Context, c Config, acc Accepted) (*client.Client, error) {
	sess, err := r.connect(ctx, c)
	if err != nil {
		return nil, err
	}
	live, err := fetchManifest(ctx, sess, c.Name)
	if err != nil {
		sess.Close()
		return nil, err
	}
	if sha := live.SHA(); sha != acc.SHA {
		sess.Close()
		return nil, fmt.Errorf("plugin %s serves another manifest (version %s, %s) than the accepted one (version %s, %s): gosidian plugin show %s, then gosidian plugin accept %s <sha>",
			c.Name, live.Version, sha[:12], acc.Manifest.Version, acc.SHA[:12], c.Name, c.Name)
	}
	return sess, nil
}

func callTool(ctx context.Context, sess *client.Client, tool string, args map[string]any) (*mcp.CallToolResult, error) {
	return callToolMeta(ctx, sess, tool, args, nil)
}

// callToolMeta calls a tool with _meta fields too.
func callToolMeta(ctx context.Context, sess *client.Client, tool string, args, meta map[string]any) (*mcp.CallToolResult, error) {
	var req mcp.CallToolRequest
	req.Params.Name = tool
	if args != nil {
		req.Params.Arguments = args
	}
	if len(meta) > 0 {
		req.Params.Meta = mcp.NewMetaFromMap(meta)
	}
	return sess.CallTool(ctx, req)
}

func fetchManifest(ctx context.Context, sess *client.Client, name string) (*Manifest, error) {
	res, err := callTool(ctx, sess, ManifestTool, nil)
	if err != nil {
		return nil, fmt.Errorf("plugin %s: %s: %w", name, ManifestTool, err)
	}
	if res.IsError {
		return nil, fmt.Errorf("plugin %s: %s: %s", name, ManifestTool, resultText(res))
	}
	m, err := ParseManifest(ResultJSON(res), name)
	if err != nil {
		return nil, fmt.Errorf("plugin %s: %w", name, err)
	}
	return m, nil
}

// ResultJSON is a tool result's structured content, else its text.
func ResultJSON(res *mcp.CallToolResult) []byte {
	if len(res.RawStructuredContent) > 0 {
		return res.RawStructuredContent
	}
	if res.StructuredContent != nil {
		if b, err := json.Marshal(res.StructuredContent); err == nil {
			return b
		}
	}
	return []byte(resultText(res))
}

func resultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if t, ok := mcp.AsTextContent(c); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}
