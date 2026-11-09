package linkgraph

import "testing"

// A graph in words, as memory_get gives it: what it shows, its counts, and
// its most linked notes; the ones with no link are not "most linked"
// (IMP-224, F-32).
func TestText(t *testing.T) {
	g := Graph{
		Nodes: []Node{{ID: "p/a.md", Degree: 1}, {ID: "p/b.md", Degree: 2}, {ID: "p/c.md", Degree: 1}, {ID: "p/d.md"}},
		Edges: []Edge{{Source: "p/a.md", Target: "p/b.md"}, {Source: "p/b.md", Target: "p/c.md"}},
		Total: 4,
	}
	for _, tc := range []struct {
		name string
		o    Options
		g    Graph
		top  int
		want string
	}{
		{"a project", Options{Project: "p"}, g, 10, "The links of project p: 4 notes, 2 links. Most linked: [[p/b]] 2, [[p/a]] 1, [[p/c]] 1."},
		{"the top", Options{Project: "p"}, g, 1, "The links of project p: 4 notes, 2 links. Most linked: [[p/b]] 2."},
		{"the filters", Options{Tag: "t", Focus: "p/b.md", MinDegree: 1}, g, 0, "The links of every project (tag t, within 2 of [[p/b]], at least 1 links each): 4 notes, 2 links."},
		{"cut by its limit", Options{Project: "p", Limit: 4}, Graph{Nodes: g.Nodes, Edges: g.Edges, Total: 9, Truncated: true}, 0, "The links of project p: 4 notes, 2 links, the 4 most linked of 9."},
		{"empty", Options{Project: "q"}, Graph{}, 10, "The links of project q: 0 notes, 0 links."},
	} {
		if got := Text(tc.o, tc.g, tc.top); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}
