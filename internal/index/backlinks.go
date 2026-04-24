package index

import (
	"strings"
)

type Backlink struct {
	Path  string
	Title string
}

// Backlinks returns notes that link to the given note path.
func (i *Index) Backlinks(path string) ([]Backlink, error) {
	rows, err := i.db.Query(`
        SELECT DISTINCT n.path, n.title
        FROM links l
        JOIN notes n ON n.id = l.src_id
        WHERE l.target_path = ?
        ORDER BY n.path
    `, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Backlink
	for rows.Next() {
		var b Backlink
		if err := rows.Scan(&b.Path, &b.Title); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Outlinks returns resolved outgoing link targets from a note path.
type Outlink struct {
	Target     string
	TargetPath string // empty if unresolved
	Alias      string
}

func (i *Index) Outlinks(path string) ([]Outlink, error) {
	rows, err := i.db.Query(`
        SELECT l.target, COALESCE(l.target_path, ''), COALESCE(l.alias, '')
        FROM links l
        JOIN notes n ON n.id = l.src_id
        WHERE n.path = ?
    `, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Outlink
	for rows.Next() {
		var o Outlink
		if err := rows.Scan(&o.Target, &o.TargetPath, &o.Alias); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// GraphData returns notes and resolved links for the graph view.
//
// If project is non-empty, only notes whose top-level folder matches project
// are returned, and edges whose endpoints both fall inside that project.
//
// Edges are deduplicated as undirected: a wikilink A→B and a reverse B→A
// collapse to a single edge with Count = sum. Self-loops are dropped.
// Node.Degree is the number of unique undirected edges the node takes part in.
type GraphNode struct {
	Path    string
	Title   string
	Project string
	Degree  int
}
type GraphEdge struct {
	From         string
	To           string
	Count        int
	CrossProject bool // at least one endpoint lies outside the selected project
}

func topLevel(path string) string {
	if idx := strings.Index(path, "/"); idx > 0 {
		return path[:idx]
	}
	return ""
}

// GraphData returns nodes + undirected aggregated edges for the graph view.
// When project == "" every note is included. When project != "":
//   - includeCross=false → behaviour pre-v1.6: only edges whose both
//     endpoints live under the project are returned.
//   - includeCross=true → edges with exactly one endpoint under the project
//     are also returned, and the "foreign" endpoint is synthesized as a node
//     so the UI can draw it. Such edges carry CrossProject=true.
func (i *Index) GraphData(project string, includeCross bool) ([]GraphNode, []GraphEdge, error) {
	var (
		nodeRows interface {
			Next() bool
			Scan(...any) error
			Close() error
			Err() error
		}
		err error
	)
	if project == "" {
		nodeRows, err = i.db.Query(`SELECT path, title FROM notes ORDER BY path`)
	} else {
		nodeRows, err = i.db.Query(
			`SELECT path, title FROM notes WHERE path LIKE ? ORDER BY path`,
			project+"/%",
		)
	}
	if err != nil {
		return nil, nil, err
	}

	nodeByPath := make(map[string]*GraphNode)
	var nodes []GraphNode
	for nodeRows.Next() {
		var n GraphNode
		if err := nodeRows.Scan(&n.Path, &n.Title); err != nil {
			nodeRows.Close()
			return nil, nil, err
		}
		n.Project = topLevel(n.Path)
		nodes = append(nodes, n)
	}
	nodeRows.Close()
	if err := nodeRows.Err(); err != nil {
		return nil, nil, err
	}

	// When include_cross_project is on we also need to resolve paths for
	// notes outside the project that are referenced by edges — so build a
	// secondary lookup of every note's title.
	allTitles := map[string]string{}
	if project != "" && includeCross {
		titleRows, terr := i.db.Query(`SELECT path, title FROM notes`)
		if terr != nil {
			return nil, nil, terr
		}
		for titleRows.Next() {
			var p, t string
			if err := titleRows.Scan(&p, &t); err != nil {
				titleRows.Close()
				return nil, nil, err
			}
			allTitles[p] = t
		}
		titleRows.Close()
	}

	// Index by path for quick membership + degree accumulation.
	for i := range nodes {
		nodeByPath[nodes[i].Path] = &nodes[i]
	}

	edgeRows, err := i.db.Query(`
        SELECT s.path, l.target_path
        FROM links l
        JOIN notes s ON s.id = l.src_id
        WHERE l.target_path IS NOT NULL AND l.target_path <> ''
    `)
	if err != nil {
		return nil, nil, err
	}
	defer edgeRows.Close()

	type edgeKey struct{ a, b string }
	type edgeAcc struct {
		count        int
		crossProject bool
	}
	agg := make(map[edgeKey]*edgeAcc)
	// Track foreign endpoints we decided to admit so we emit nodes for them.
	foreign := make(map[string]struct{})

	for edgeRows.Next() {
		var from, to string
		if err := edgeRows.Scan(&from, &to); err != nil {
			return nil, nil, err
		}
		if from == to {
			continue
		}

		_, fromIn := nodeByPath[from]
		_, toIn := nodeByPath[to]
		switch {
		case fromIn && toIn:
			// standard intra-project edge
		case (fromIn || toIn) && project != "" && includeCross:
			// one endpoint outside the selected project — admitted.
			if !fromIn {
				foreign[from] = struct{}{}
			}
			if !toIn {
				foreign[to] = struct{}{}
			}
		default:
			continue
		}

		a, b := from, to
		if a > b {
			a, b = b, a
		}
		k := edgeKey{a, b}
		cur := agg[k]
		if cur == nil {
			cur = &edgeAcc{}
			agg[k] = cur
		}
		cur.count++
		// The edge is cross-project if at least one endpoint is foreign.
		if _, ok := nodeByPath[from]; !ok {
			cur.crossProject = true
		}
		if _, ok := nodeByPath[to]; !ok {
			cur.crossProject = true
		}
	}
	if err := edgeRows.Err(); err != nil {
		return nil, nil, err
	}

	// Synthesize nodes for foreign endpoints + index them for degree bumping.
	for p := range foreign {
		n := GraphNode{Path: p, Title: allTitles[p], Project: topLevel(p)}
		if n.Title == "" {
			n.Title = p
		}
		nodes = append(nodes, n)
		nodeByPath[p] = &nodes[len(nodes)-1]
	}

	edges := make([]GraphEdge, 0, len(agg))
	for k, acc := range agg {
		edges = append(edges, GraphEdge{
			From:         k.a,
			To:           k.b,
			Count:        acc.count,
			CrossProject: acc.crossProject,
		})
		if n := nodeByPath[k.a]; n != nil {
			n.Degree++
		}
		if n := nodeByPath[k.b]; n != nil {
			n.Degree++
		}
	}

	return nodes, edges, nil
}

// Projects returns the sorted list of distinct top-level folders containing notes.
func (i *Index) Projects() ([]string, error) {
	rows, err := i.db.Query(`
        SELECT DISTINCT substr(path, 1, instr(path, '/') - 1) AS project
        FROM notes
        WHERE instr(path, '/') > 0
        ORDER BY project
    `)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		if p != "" {
			out = append(out, p)
		}
	}
	return out, rows.Err()
}
