// Package linkgraph builds the graph of the vault's wiki-links with the
// filters of the Graph window: a project, a tag, a focus and its depth, a
// minimum degree, a limit. The web UI draws it (/api/v1/graph); memory_get
// says the ```graph blocks of a note with it (IMP-224, F-32).
package linkgraph

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/gosidian/gosidian/internal/index"
)

// Node is a note of the graph. ID is its path; Degree counts its links
// within the graph.
type Node struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Project string `json:"project,omitempty"`
	Degree  int    `json:"degree"`
	// Mtime (unix seconds) powers the 3D "recency" z-mode client-side;
	// 0 (omitted) on synthesized cross-project endpoints.
	Mtime int64 `json:"mtime,omitempty"`
}

// Edge is a link from Source to Target; Count is the (deduplicated) edge
// weight, CrossProject flags an edge that spans the project filter.
type Edge struct {
	Source       string `json:"source"`
	Target       string `json:"target"`
	Count        int    `json:"count"`
	CrossProject bool   `json:"cross_project,omitempty"`
}

// Options are the filters of a graph. Depth counts only with a Focus (2
// when 0); Limit 0 keeps every node. Visible says the notes the reader may
// see; nil, all of them.
type Options struct {
	Project      string
	Tag          string
	Focus        string
	Depth        int
	MinDegree    int
	Limit        int
	IncludeCross bool
	Visible      func(path string) bool
}

// Graph is the filtered graph, its nodes and edges ordered by path. Total
// is the node count before the limit cut it.
type Graph struct {
	Nodes     []Node
	Edges     []Edge
	Total     int
	Truncated bool
}

// Build reads the links from the index and filters them.
func Build(idx *index.Index, o Options) (Graph, error) {
	if o.Depth <= 0 {
		o.Depth = 2
	}
	rawNodes, rawEdges, err := idx.GraphData(o.Project, o.IncludeCross)
	if err != nil {
		return Graph{}, fmt.Errorf("graph: %w", err)
	}

	// Tag filter: intersect nodes with NotesByTag(InProject). Edges with
	// any endpoint outside the kept set are dropped — we don't synthesize
	// foreign endpoints here (cross_project flag is for project filter).
	keep := nodeSet(rawNodes)
	// Per-reader visibility. Edges touching dropped nodes fall away in the
	// materialise step below.
	if o.Visible != nil {
		for path := range keep {
			if !o.Visible(path) {
				delete(keep, path)
			}
		}
	}
	if o.Tag != "" {
		tagged, err := idx.NotesByTagInProject(o.Tag, o.Project)
		if err != nil {
			return Graph{}, fmt.Errorf("tag: %w", err)
		}
		paths := make([]string, 0, len(tagged))
		for _, n := range tagged {
			paths = append(paths, n.Path)
		}
		intersectPaths(keep, paths)
	}

	// Focus + depth: ego-graph BFS. Build adjacency from rawEdges
	// limited to currently-kept nodes, then expand from focus up to
	// depth hops.
	if o.Focus != "" {
		if _, ok := keep[o.Focus]; !ok {
			// Focus node not in working set — empty graph (caller can
			// re-query without filters to find it).
			keep = map[string]struct{}{}
		} else {
			keep = bfsEgo(o.Focus, buildAdjacency(rawEdges, keep), o.Depth)
		}
	}

	// Materialise filtered nodes + edges.
	nodes := make([]Node, 0, len(keep))
	for _, n := range rawNodes {
		if _, ok := keep[n.Path]; !ok {
			continue
		}
		nodes = append(nodes, Node{ID: n.Path, Label: labelOrPath(n.Title, n.Path), Project: n.Project, Mtime: n.Mtime})
	}
	edges := make([]Edge, 0, len(rawEdges))
	for _, e := range rawEdges {
		if _, okA := keep[e.From]; !okA {
			continue
		}
		if _, okB := keep[e.To]; !okB {
			continue
		}
		edges = append(edges, Edge{Source: e.From, Target: e.To, Count: e.Count, CrossProject: e.CrossProject})
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
	if o.MinDegree > 0 {
		alive := make(map[string]struct{}, len(nodes))
		for _, n := range nodes {
			if n.Degree >= o.MinDegree {
				alive[n.ID] = struct{}{}
			}
		}
		nodes = filterNodes(nodes, alive)
		edges = filterEdges(edges, alive)
	}

	g := Graph{Total: len(nodes)}
	if o.Limit > 0 && len(nodes) > o.Limit {
		// The most linked first, then by path for a stable cut; the focus
		// of an ego graph stays whatever its degree.
		byDegree(nodes, o.Focus)
		nodes = nodes[:o.Limit]
		alive := make(map[string]struct{}, len(nodes))
		for _, n := range nodes {
			alive[n.ID] = struct{}{}
		}
		edges = filterEdges(edges, alive)
		g.Truncated = true
	}

	// Stable order in the response so the ETag is deterministic.
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Source != edges[j].Source {
			return edges[i].Source < edges[j].Source
		}
		return edges[i].Target < edges[j].Target
	})
	g.Nodes, g.Edges = nodes, edges
	return g, nil
}

// byDegree sorts the nodes by degree, the most linked first, then by path;
// focus, when set, comes first.
func byDegree(nodes []Node, focus string) {
	sort.Slice(nodes, func(i, j int) bool {
		if fi, fj := nodes[i].ID == focus, nodes[j].ID == focus; fi != fj {
			return fi
		}
		if nodes[i].Degree != nodes[j].Degree {
			return nodes[i].Degree > nodes[j].Degree
		}
		return nodes[i].ID < nodes[j].ID
	})
}

// Filter is the filters as the web UI's graph stats name them.
func Filter(project, tag, focus string, depth, minDegree, limit int) string {
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

// Text says a graph in words, as memory_get gives it to an agent: what it
// shows, how many notes and links, and its top most linked notes.
func Text(o Options, g Graph, top int) string {
	var b strings.Builder
	b.WriteString("The links of ")
	if o.Project != "" {
		b.WriteString("project " + o.Project)
	} else {
		b.WriteString("every project")
	}
	var with []string
	if o.Tag != "" {
		with = append(with, "tag "+o.Tag)
	}
	if o.Focus != "" {
		depth := o.Depth
		if depth <= 0 {
			depth = 2
		}
		with = append(with, fmt.Sprintf("within %d of [[%s]]", depth, strings.TrimSuffix(o.Focus, ".md")))
	}
	if o.MinDegree > 0 {
		with = append(with, fmt.Sprintf("at least %d links each", o.MinDegree))
	}
	if len(with) > 0 {
		b.WriteString(" (" + strings.Join(with, ", ") + ")")
	}
	fmt.Fprintf(&b, ": %s, %s", plural(len(g.Nodes), "note", "notes"), plural(len(g.Edges), "link", "links"))
	if g.Truncated {
		fmt.Fprintf(&b, ", the %d most linked of %d", len(g.Nodes), g.Total)
	}
	b.WriteString(".")
	// The notes with a link, the most linked first.
	var linked []Node
	for _, n := range g.Nodes {
		if n.Degree > 0 {
			linked = append(linked, n)
		}
	}
	if top > 0 && len(linked) > 0 {
		byDegree(linked, "")
		if len(linked) > top {
			linked = linked[:top]
		}
		names := make([]string, 0, len(linked))
		for _, n := range linked {
			names = append(names, fmt.Sprintf("[[%s]] %d", strings.TrimSuffix(n.ID, ".md"), n.Degree))
		}
		b.WriteString(" Most linked: " + strings.Join(names, ", ") + ".")
	}
	return b.String()
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func nodeSet(nodes []index.GraphNode) map[string]struct{} {
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

func filterNodes(nodes []Node, alive map[string]struct{}) []Node {
	out := nodes[:0]
	for _, n := range nodes {
		if _, ok := alive[n.ID]; ok {
			out = append(out, n)
		}
	}
	return out
}

func filterEdges(edges []Edge, alive map[string]struct{}) []Edge {
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
