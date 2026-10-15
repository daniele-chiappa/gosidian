package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/graphspec"
	"github.com/gosidian/gosidian/internal/plugins"
)

// The graphs of the plugins (IMP-183): /api/v1/graph?source=<plugin>/<graph>
// asks a plugin exposed in the project for its nodes and edges (the chunks
// of an archive of vectors and their nearest neighbours, say), and
// /api/v1/graph/sources lists what a project's Graph window may show.

const (
	// maxGraphArgs caps the JSON of a graph's arguments, a format rather
	// than a cap of the graph ([web] graph_max_nodes and graph_max_edges).
	maxGraphArgs = 4 << 10
	// graphCacheTTL keeps a graph that costs the plugin seconds (the
	// neighbours of thousands of vectors); refresh=1 asks again.
	graphCacheTTL  = 15 * time.Minute
	graphCacheSize = 32
)

// pluginGraphNode is a node of a plugin's graph: link names a note of the
// vault it opens, text what it shows; x, y and z fix its place, for a
// layout of the plugin's.
type pluginGraphNode struct {
	ID     string   `json:"id"`
	Label  string   `json:"label"`
	Group  string   `json:"group,omitempty"`
	Link   string   `json:"link,omitempty"`
	Text   string   `json:"text,omitempty"`
	Degree int      `json:"degree"`
	X      *float64 `json:"x,omitempty"`
	Y      *float64 `json:"y,omitempty"`
	Z      *float64 `json:"z,omitempty"`
}

// pluginGraphEdge is an edge: weight is the plugin's (a similarity), count
// 1, as the canvas of the links reads it.
type pluginGraphEdge struct {
	Source string  `json:"source"`
	Target string  `json:"target"`
	Count  int     `json:"count"`
	Weight float64 `json:"weight,omitempty"`
}

type pluginGraphResponse struct {
	Source string            `json:"source"`
	Nodes  []pluginGraphNode `json:"nodes"`
	Edges  []pluginGraphEdge `json:"edges"`
	Stats  graphStats        `json:"stats"`
	Cached bool              `json:"cached,omitempty"`
}

type graphCacheEntry struct {
	at   time.Time
	body pluginGraphResponse
}

// graphCache keeps the plugins' graphs a while, by source, project,
// arguments and accepted manifest.
type graphCache struct {
	mu      sync.Mutex
	entries map[string]graphCacheEntry
}

func (c *graphCache) get(key string) (pluginGraphResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || time.Since(e.at) > graphCacheTTL {
		return pluginGraphResponse{}, false
	}
	return e.body, true
}

func (c *graphCache) put(key string, body pluginGraphResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]graphCacheEntry{}
	}
	if len(c.entries) >= graphCacheSize {
		oldest := ""
		for k, e := range c.entries {
			if oldest == "" || e.at.Before(c.entries[oldest].at) {
				oldest = k
			}
		}
		delete(c.entries, oldest)
	}
	c.entries[key] = graphCacheEntry{at: time.Now(), body: body}
}

func (r *Router) canSeeProject(req *http.Request, project string) bool {
	return principalFromContext(req).CanAccessProject(project, r.accessConfig())
}

// graphSource is a source of a project's Graph window.
type graphSource struct {
	Source      string          `json:"source"`
	Plugin      string          `json:"plugin,omitempty"`
	Graph       string          `json:"graph,omitempty"`
	Description string          `json:"description,omitempty"`
	Input       json.RawMessage `json:"input_schema,omitempty"`
	Status      string          `json:"status,omitempty"`
}

// handleGraphSources answers GET /api/v1/graph/sources?project=<p>: the
// links, and the graphs of the plugins exposed in the project.
func (r *Router) handleGraphSources(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	project := strings.TrimSpace(req.URL.Query().Get("project"))
	out := []graphSource{{Source: graphspec.SourceLinks}}
	if r.deps.Plugins != nil && project != "" && r.canSeeProject(req, project) {
		for _, g := range r.deps.Plugins.Graphs() {
			if !slices.Contains(r.deps.Plugins.ExposedIn(g.Plugin), project) {
				continue
			}
			st, _ := r.deps.Plugins.State(g.Plugin)
			out = append(out, graphSource{Source: g.Source, Plugin: g.Plugin, Graph: g.Spec.Name,
				Description: g.Spec.Description, Input: g.Spec.Input, Status: st.Status})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"project": project, "sources": out})
}

// handlePluginGraph answers GET /api/v1/graph?source=<plugin>/<graph>
// &project=<p>&with=<JSON object>[&refresh=1].
func (r *Router) handlePluginGraph(w http.ResponseWriter, req *http.Request, source string) {
	q := req.URL.Query()
	spec := graphspec.Spec{Source: source}
	plugin, graph, ok := spec.Plugin()
	if !ok {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "source is links or <plugin>/<graph>")
		return
	}
	if r.deps.Plugins == nil {
		WriteError(w, http.StatusNotFound, CodeNotFound, "the plugins are off on this server ([pipelines] enabled)")
		return
	}
	project := strings.TrimSpace(q.Get("project"))
	if project == "" {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "project names the project the graph is asked for")
		return
	}
	if !r.canSeeProject(req, project) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "no project "+project)
		return
	}
	if !slices.Contains(r.deps.Plugins.ExposedIn(plugin), project) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "plugin "+plugin+" is not exposed in project "+project+" (gosidian plugin expose "+plugin+" "+project+")")
		return
	}
	args := map[string]any{}
	if raw := q.Get("with"); raw != "" {
		if len(raw) > maxGraphArgs || json.Unmarshal([]byte(raw), &args) != nil {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, "with is a JSON object of the graph's arguments, at most 4 KiB")
			return
		}
	}
	st, _ := r.deps.Plugins.State(plugin)
	sha := ""
	if st.Accepted != nil {
		sha = st.Accepted.SHA
	}
	canon, _ := json.Marshal(args)
	key := strings.Join([]string{source, project, string(canon), sha}, "\x00")
	if q.Get("refresh") != "1" {
		if body, ok := r.graphCache.get(key); ok {
			body.Cached = true
			writeJSON(w, http.StatusOK, body)
			return
		}
	}
	user := UserFromContext(req.Context())
	caller := "web"
	if user != nil {
		caller = "web:" + user.Username
	}
	ctx, cancel := context.WithTimeout(req.Context(), r.web.PluginTimeout)
	defer cancel()
	raw, err := r.deps.Plugins.Graph(ctx, plugin, graph, args, map[string]any{plugins.MetaProject: project, plugins.MetaCaller: caller, plugins.MetaAccess: plugins.AccessRead})
	if r.deps.Audit != nil {
		e := audit.Entry{Source: audit.SourceHTTP, Actor: caller, Action: audit.ActionPluginCall, Path: project, To: source}
		if user != nil {
			e.UserID = user.ID
		}
		_ = r.deps.Audit.Write(e)
	}
	if err != nil {
		// The plugin answered with an error of its own, the arguments
		// refused or its store failing: the reader reads its message, the
		// server is not at fault (BUG-128).
		var refused *plugins.RefusedError
		if errors.As(err, &refused) {
			WriteError(w, http.StatusUnprocessableEntity, CodePluginRefused, err.Error())
			return
		}
		WriteError(w, http.StatusBadGateway, CodeServerInternal, err.Error())
		return
	}
	body, err := r.readPluginGraph(req, raw)
	if err != nil {
		WriteError(w, http.StatusBadGateway, CodeServerInternal, "plugin "+plugin+", graph "+graph+": "+err.Error())
		return
	}
	body.Source = source
	r.graphCache.put(key, body)
	writeJSON(w, http.StatusOK, body)
}

// readPluginGraph checks a plugin's graph: its caps, edges between nodes it
// has, links only to notes the caller sees.
func (r *Router) readPluginGraph(req *http.Request, raw json.RawMessage) (pluginGraphResponse, error) {
	var in struct {
		Nodes []pluginGraphNode `json:"nodes"`
		Edges []struct {
			Source string  `json:"source"`
			Target string  `json:"target"`
			Weight float64 `json:"weight"`
		} `json:"edges"`
		Stats struct {
			Truncated bool   `json:"truncated"`
			Filter    string `json:"filter"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return pluginGraphResponse{}, errInvalidGraph("it is not a graph's JSON: " + err.Error())
	}
	if len(in.Nodes) > r.web.GraphMaxNodes || len(in.Edges) > r.web.GraphMaxEdges {
		return pluginGraphResponse{}, errInvalidGraph(fmt.Sprintf("more than %d nodes or %d edges ([web] graph_max_nodes, graph_max_edges)", r.web.GraphMaxNodes, r.web.GraphMaxEdges))
	}
	princ := principalFromContext(req)
	out := pluginGraphResponse{Nodes: make([]pluginGraphNode, 0, len(in.Nodes)), Edges: []pluginGraphEdge{}}
	index := map[string]int{}
	for _, n := range in.Nodes {
		if n.ID == "" {
			continue
		}
		if _, dup := index[n.ID]; dup {
			continue
		}
		if n.Label == "" {
			n.Label = n.ID
		}
		if n.Link != "" && (!strings.HasSuffix(strings.ToLower(n.Link), ".md") || !r.canSee(princ, n.Link)) {
			n.Link = ""
		}
		if len(n.Text) > 2000 {
			n.Text = n.Text[:2000] + "…"
		}
		n.Degree = 0
		index[n.ID] = len(out.Nodes)
		out.Nodes = append(out.Nodes, n)
	}
	type pair struct{ a, b string }
	seen := map[pair]bool{}
	for _, e := range in.Edges {
		ia, okA := index[e.Source]
		ib, okB := index[e.Target]
		if !okA || !okB || e.Source == e.Target {
			continue
		}
		p := pair{e.Source, e.Target}
		if p.b < p.a {
			p = pair{p.b, p.a}
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		out.Nodes[ia].Degree++
		out.Nodes[ib].Degree++
		out.Edges = append(out.Edges, pluginGraphEdge{Source: e.Source, Target: e.Target, Count: 1, Weight: e.Weight})
	}
	sort.SliceStable(out.Edges, func(i, j int) bool { return out.Edges[i].Weight > out.Edges[j].Weight })
	out.Stats = graphStats{NodeCount: len(out.Nodes), EdgeCount: len(out.Edges), Truncated: in.Stats.Truncated, Filter: in.Stats.Filter}
	return out, nil
}

type errInvalidGraph string

func (e errInvalidGraph) Error() string { return string(e) }
