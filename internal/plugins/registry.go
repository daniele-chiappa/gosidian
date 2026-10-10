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

// reserved are the base node types' prefixes, no plugin's name.
var reserved = map[string]bool{"note": true, "wait": true, "starlark": true, "http": true, "llm": true, "pipeline": true}

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
			return nil, fmt.Errorf("plugin %q: the name of base node types", c.Name)
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

func callTool(ctx context.Context, sess *client.Client, tool string, args map[string]any) (*mcp.CallToolResult, error) {
	var req mcp.CallToolRequest
	req.Params.Name = tool
	if args != nil {
		req.Params.Arguments = args
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
	m, err := ParseManifest(resultJSON(res), name)
	if err != nil {
		return nil, fmt.Errorf("plugin %s: %w", name, err)
	}
	return m, nil
}

// resultJSON is a tool result's structured content, else its text.
func resultJSON(res *mcp.CallToolResult) []byte {
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
