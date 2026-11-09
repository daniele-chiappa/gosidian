package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"

	"github.com/gosidian/gosidian/internal/graphspec"
	"github.com/gosidian/gosidian/internal/linkgraph"
)

// graphNode and graphEdge are the wire shapes of the canvas, built by
// linkgraph (IMP-224, F-32: memory_get builds the same graph).
type (
	graphNode = linkgraph.Node
	graphEdge = linkgraph.Edge
)

type graphResponse struct {
	Nodes []graphNode `json:"nodes"`
	Edges []graphEdge `json:"edges"`
	Stats graphStats  `json:"stats"`
}

type graphStats struct {
	NodeCount int `json:"node_count"`
	EdgeCount int `json:"edge_count"`
	// Total is the node count before the limit cut the graph.
	Total     int    `json:"total"`
	Truncated bool   `json:"truncated"`
	Filter    string `json:"filter,omitempty"`
}

// handleGraph powers the /graph view. Tier 1 server-side filters
// drop the working set before it hits the wire so the client doesn't
// need WebGL to handle larger vaults:
//
//	?project=<slug>     — restrict to notes under top-level project
//	?tag=<name>         — restrict to notes carrying the tag
//	?min_degree=<n>     — drop nodes with fewer than n edges
//	?focus=<path>&depth=<n> — ego-graph BFS n hops from focus
//	?limit=<n>          — cap total nodes (drops lowest-degree first;
//	                      [web] graph_default_nodes when absent,
//	                      graph_max_nodes at most)
//	?include_cross=true — admit cross-project edges (project mode)
//
// Response carries an ETag derived from the filtered payload so
// browsers can short-circuit unchanged graphs (Cache-Control max-age
// 60 keeps the dropdown navigation snappy without staling out edits).
func (r *Router) handleGraph(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	if r.deps.Index == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "index not configured")
		return
	}

	q := req.URL.Query()
	// A plugin's graph (IMP-183): the links are the default source.
	if source := strings.TrimSpace(q.Get("source")); source != "" && source != graphspec.SourceLinks {
		r.handlePluginGraph(w, req, source)
		return
	}
	project := strings.TrimSpace(q.Get("project"))
	tag := strings.TrimSpace(q.Get("tag"))
	focus := strings.TrimSpace(q.Get("focus"))
	includeCross := q.Get("include_cross") == "true"
	minDegree, _ := strconv.Atoi(q.Get("min_degree"))
	depth, _ := strconv.Atoi(q.Get("depth"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if depth <= 0 {
		depth = 2
	}
	// A graph asked without a limit keeps [web] graph_default_nodes: a
	// canvas that simulates and paints every node of a project of
	// thousands of notes freezes the page (BUG-127); one asked with a
	// limit keeps at most graph_max_nodes.
	asked := limit > 0
	if !asked {
		limit = r.web.GraphDefaultNodes
	}
	limit = min(limit, r.web.GraphMaxNodes)

	princ := principalFromContext(req)
	opts := linkgraph.Options{
		Project: project, Tag: tag, Focus: focus, Depth: depth,
		MinDegree: minDegree, Limit: limit, IncludeCross: includeCross,
	}
	// Per-role visibility: guests keep only nodes in public projects.
	if !r.seesAllProjects(princ) {
		opts.Visible = func(path string) bool { return r.canSee(princ, path) }
	}
	g, err := linkgraph.Build(r.deps.Index, opts)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	nodes, edges, total, truncated := g.Nodes, g.Edges, g.Total, g.Truncated

	resp := graphResponse{
		Nodes: nodes,
		Edges: edges,
		Stats: graphStats{
			NodeCount: len(nodes),
			EdgeCount: len(edges),
			Total:     total,
			Truncated: truncated,
			Filter:    linkgraph.Filter(project, tag, focus, depth, minDegree, shownLimit(asked, truncated, limit)),
		},
	}

	etag := hashGraph(resp)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "max-age=60")
	if match := req.Header.Get("If-None-Match"); match != "" && match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	WriteJSON(w, http.StatusOK, resp)
}

func hashGraph(resp graphResponse) string {
	h := sha256.New()
	for _, n := range resp.Nodes {
		h.Write([]byte(n.ID))
		h.Write([]byte{0})
	}
	for _, e := range resp.Edges {
		h.Write([]byte(e.Source))
		h.Write([]byte{0})
		h.Write([]byte(e.Target))
		h.Write([]byte{0})
		var buf [8]byte
		for i, b := 0, e.Count; i < 4; i, b = i+1, b>>8 {
			buf[i] = byte(b)
		}
		h.Write(buf[:4])
	}
	return `"` + hex.EncodeToString(h.Sum(nil)[:16]) + `"`
}

// shownLimit is the limit the filter summary names: one asked for, or the
// default when it cut the graph.
func shownLimit(asked, truncated bool, limit int) int {
	if asked || truncated {
		return limit
	}
	return 0
}
