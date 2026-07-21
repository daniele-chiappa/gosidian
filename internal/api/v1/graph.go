package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/gosidian/gosidian/internal/index"
)

// graphNode is the wire shape consumed by the Cytoscape canvas. id is
// the note path (Cytoscape's selector key); label drives the visible
// text; degree powers the Tier 1 min-degree filter and node sizing
// hints client-side.
type graphNode struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Project string `json:"project,omitempty"`
	Degree  int    `json:"degree"`
	// Mtime (unix seconds) powers the 3D "recency" z-mode client-side;
	// 0 (omitted) on synthesized cross-project endpoints.
	Mtime int64 `json:"mtime,omitempty"`
}

// graphEdge mirrors Cytoscape's edge shape — the source/target keys
// reference graphNode.id, count is the (deduplicated) edge weight,
// cross_project flags edges spanning the project filter so the UI can
// dim or stroke them differently.
type graphEdge struct {
	Source       string `json:"source"`
	Target       string `json:"target"`
	Count        int    `json:"count"`
	CrossProject bool   `json:"cross_project,omitempty"`
}

type graphResponse struct {
	Nodes []graphNode `json:"nodes"`
	Edges []graphEdge `json:"edges"`
	Stats graphStats  `json:"stats"`
}

type graphStats struct {
	NodeCount int    `json:"node_count"`
	EdgeCount int    `json:"edge_count"`
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
//	?limit=<n>          — cap total nodes (drops lowest-degree first)
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
	if limit < 0 {
		limit = 0
	}

	rawNodes, rawEdges, err := r.deps.Index.GraphData(project, includeCross)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "graph: "+err.Error())
		return
	}

	// Tag filter: intersect nodes with NotesByTag(InProject). Edges with
	// any endpoint outside the kept set are dropped — we don't synthesize
	// foreign endpoints here (cross_project flag is for project filter).
	keep := graphNodeSet(rawNodes)
	// Per-role visibility: guests keep only nodes in public projects. Edges
	// touching dropped nodes fall away in the materialise step below.
	if princ := principalFromContext(req); !r.seesAllProjects(princ) {
		for path := range keep {
			if !r.canSee(princ, path) {
				delete(keep, path)
			}
		}
	}
	if tag != "" {
		var paths []string
		if project != "" {
			tagged, terr := r.deps.Index.NotesByTagInProject(tag, project)
			if terr != nil {
				WriteError(w, http.StatusInternalServerError, CodeServerInternal, "tag: "+terr.Error())
				return
			}
			for _, n := range tagged {
				paths = append(paths, n.Path)
			}
		} else {
			tagged, terr := r.deps.Index.NotesByTag(tag)
			if terr != nil {
				WriteError(w, http.StatusInternalServerError, CodeServerInternal, "tag: "+terr.Error())
				return
			}
			for _, n := range tagged {
				paths = append(paths, n.Path)
			}
		}
		intersectPaths(keep, paths)
	}

	// Focus + depth: ego-graph BFS. Build adjacency from rawEdges
	// limited to currently-kept nodes, then expand from focus up to
	// depth hops.
	if focus != "" {
		if _, ok := keep[focus]; !ok {
			// Focus node not in working set — empty graph (caller can
			// re-query without filters to find it).
			keep = map[string]struct{}{}
		} else {
			adj := buildAdjacency(rawEdges, keep)
			keep = bfsEgo(focus, adj, depth)
		}
	}

	// Materialise filtered nodes + edges.
	nodes := make([]graphNode, 0, len(keep))
	for _, n := range rawNodes {
		if _, ok := keep[n.Path]; !ok {
			continue
		}
		nodes = append(nodes, graphNode{
			ID:      n.Path,
			Label:   labelOrPath(n.Title, n.Path),
			Project: n.Project,
			Mtime:   n.Mtime,
		})
	}
	edges := make([]graphEdge, 0, len(rawEdges))
	for _, e := range rawEdges {
		if _, okA := keep[e.From]; !okA {
			continue
		}
		if _, okB := keep[e.To]; !okB {
			continue
		}
		edges = append(edges, graphEdge{
			Source:       e.From,
			Target:       e.To,
			Count:        e.Count,
			CrossProject: e.CrossProject,
		})
	}

	// Recompute degree post-filter (the Index.GraphData degrees no
	// longer match if we dropped nodes).
	degree := make(map[string]int, len(nodes))
	for _, e := range edges {
		degree[e.Source]++
		degree[e.Target]++
	}
	for i := range nodes {
		nodes[i].Degree = degree[nodes[i].ID]
	}

	// min_degree cull: prune nodes below threshold + their edges. One
	// pass — clients chain-prune via successive requests if needed.
	if minDegree > 0 {
		alive := make(map[string]struct{}, len(nodes))
		for _, n := range nodes {
			if n.Degree >= minDegree {
				alive[n.ID] = struct{}{}
			}
		}
		nodes = filterNodes(nodes, alive)
		edges = filterEdges(edges, alive)
	}

	truncated := false
	if limit > 0 && len(nodes) > limit {
		// Sort by degree desc, then by path asc for stable output.
		sort.Slice(nodes, func(i, j int) bool {
			if nodes[i].Degree != nodes[j].Degree {
				return nodes[i].Degree > nodes[j].Degree
			}
			return nodes[i].ID < nodes[j].ID
		})
		nodes = nodes[:limit]
		alive := make(map[string]struct{}, len(nodes))
		for _, n := range nodes {
			alive[n.ID] = struct{}{}
		}
		edges = filterEdges(edges, alive)
		truncated = true
	}

	// Stable order in the response so the ETag is deterministic.
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Source != edges[j].Source {
			return edges[i].Source < edges[j].Source
		}
		return edges[i].Target < edges[j].Target
	})

	resp := graphResponse{
		Nodes: nodes,
		Edges: edges,
		Stats: graphStats{
			NodeCount: len(nodes),
			EdgeCount: len(edges),
			Truncated: truncated,
			Filter:    summarizeFilter(project, tag, focus, depth, minDegree, limit),
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

func graphNodeSet(nodes []index.GraphNode) map[string]struct{} {
	out := make(map[string]struct{}, len(nodes))
	for _, n := range nodes {
		out[n.Path] = struct{}{}
	}
	return out
}

func intersectPaths(keep map[string]struct{}, want []string) {
	wantSet := make(map[string]struct{}, len(want))
	for _, p := range want {
		wantSet[p] = struct{}{}
	}
	for p := range keep {
		if _, ok := wantSet[p]; !ok {
			delete(keep, p)
		}
	}
}

func buildAdjacency(edges []index.GraphEdge, keep map[string]struct{}) map[string][]string {
	adj := make(map[string][]string)
	for _, e := range edges {
		if _, okA := keep[e.From]; !okA {
			continue
		}
		if _, okB := keep[e.To]; !okB {
			continue
		}
		adj[e.From] = append(adj[e.From], e.To)
		adj[e.To] = append(adj[e.To], e.From)
	}
	return adj
}

func bfsEgo(start string, adj map[string][]string, depth int) map[string]struct{} {
	visited := map[string]struct{}{start: {}}
	frontier := []string{start}
	for hop := 0; hop < depth && len(frontier) > 0; hop++ {
		next := []string{}
		for _, node := range frontier {
			for _, neighbour := range adj[node] {
				if _, seen := visited[neighbour]; seen {
					continue
				}
				visited[neighbour] = struct{}{}
				next = append(next, neighbour)
			}
		}
		frontier = next
	}
	return visited
}

func filterNodes(nodes []graphNode, alive map[string]struct{}) []graphNode {
	out := nodes[:0]
	for _, n := range nodes {
		if _, ok := alive[n.ID]; ok {
			out = append(out, n)
		}
	}
	return out
}

func filterEdges(edges []graphEdge, alive map[string]struct{}) []graphEdge {
	out := edges[:0]
	for _, e := range edges {
		if _, okA := alive[e.Source]; !okA {
			continue
		}
		if _, okB := alive[e.Target]; !okB {
			continue
		}
		out = append(out, e)
	}
	return out
}

func labelOrPath(title, path string) string {
	if title != "" {
		return title
	}
	return path
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

func summarizeFilter(project, tag, focus string, depth, minDegree, limit int) string {
	parts := []string{}
	if project != "" {
		parts = append(parts, "project="+project)
	}
	if tag != "" {
		parts = append(parts, "tag="+tag)
	}
	if focus != "" {
		parts = append(parts, "focus="+focus+"@"+strconv.Itoa(depth))
	}
	if minDegree > 0 {
		parts = append(parts, "min_degree="+strconv.Itoa(minDegree))
	}
	if limit > 0 {
		parts = append(parts, "limit="+strconv.Itoa(limit))
	}
	return strings.Join(parts, ",")
}
