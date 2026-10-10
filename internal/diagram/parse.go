package diagram

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/gosidian/gosidian/internal/parser"
)

var (
	idRe       = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,39}$`)
	edgeRe     = regexp.MustCompile(`^\s*([^\s:>-][^\s>]*?)\s*->\s*([^\s:]+)\s*(?::\s*(.*))?$`)
	yamlLineRe = regexp.MustCompile(`line (\d+)`)
)

var (
	directions = []string{"TB", "LR", "BT", "RL"}
	shapes     = []string{"box", "pill", "decision", "data", "doc", "db", "circle", "note"}
	classes    = []string{"user", "data", "ext", "planned", "off"}
	styles     = []string{"solid", "dashed", "dotted"}
	statuses   = []string{"done", "active", "critical", "milestone"}
)

// The fields each kind reads, beyond diagram, kind, title, description and
// layout.
var kindFields = map[Kind][]string{
	Flow:     {"direction", "nodes", "edges", "groups"},
	Tree:     {"direction", "root", "children"},
	Mindmap:  {"root", "children"},
	Fishbone: {"root", "children"},
	Venn:     {"sets", "areas"},
	Matrix:   {"x", "y", "quadrants", "items", "rows", "columns", "cells"},
	Gantt:    {"sections", "tasks"},
}

// reader collects the problems of one diagram.
type reader struct {
	problems []Problem
	items    int
	ids      map[string]int // id → line of its first use
}

func (r *reader) bad(n *yaml.Node, format string, a ...any) {
	line := 0
	if n != nil {
		line = n.Line
	}
	r.problems = append(r.problems, Problem{Line: line, Msg: fmt.Sprintf(format, a...)})
}

// count adds n items, and says false once the diagram is too large.
func (r *reader) count(at *yaml.Node, n int) bool {
	r.items += n
	if r.items > MaxItems {
		if r.items-n <= MaxItems {
			r.bad(at, "the diagram has more than %d elements", MaxItems)
		}
		return false
	}
	return true
}

// id checks an id and that it is the only one of its name.
func (r *reader) id(n *yaml.Node, id string) {
	if !idRe.MatchString(id) {
		r.bad(n, "%q is not an id: a letter, then letters, digits, - and _ (at most 40)", id)
		return
	}
	if first, ok := r.ids[id]; ok {
		r.bad(n, "id %q is used twice (first at line %d)", id, first)
		return
	}
	r.ids[id] = n.Line
}

func (r *reader) link(n *yaml.Node, link string) string {
	link = strings.TrimSpace(link)
	switch {
	case link == "":
	case strings.HasPrefix(link, "http://"), strings.HasPrefix(link, "https://"):
	case strings.HasPrefix(link, "[[") && strings.HasSuffix(link, "]]") && len(parser.WikiLinks(link)) == 1:
	default:
		r.bad(n, "link %q is a [[note]] or an http(s) address", link)
	}
	return link
}

// linkOf reads the link field of a mapping.
func (r *reader) linkOf(f map[string]*yaml.Node) string {
	return r.link(f["link"], r.str(f["link"], "link"))
}

func oneOf(v string, allowed []string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

// pairs is a mapping's keys and values, in order.
type pair struct{ key, val *yaml.Node }

func pairs(n *yaml.Node) []pair {
	out := make([]pair, 0, len(n.Content)/2)
	for i := 0; i+1 < len(n.Content); i += 2 {
		out = append(out, pair{n.Content[i], n.Content[i+1]})
	}
	return out
}

// fields reads a mapping whose keys must be in allowed.
func (r *reader) fields(n *yaml.Node, what string, allowed ...string) map[string]*yaml.Node {
	out := map[string]*yaml.Node{}
	if n.Kind != yaml.MappingNode {
		r.bad(n, "%s is a mapping (key: value)", what)
		return out
	}
	for _, p := range pairs(n) {
		k := p.key.Value
		if !oneOf(k, allowed) {
			r.bad(p.key, "%s has no field %q (it has %s)", what, k, strings.Join(allowed, ", "))
			continue
		}
		out[k] = p.val
	}
	return out
}

func (r *reader) str(n *yaml.Node, what string) string {
	if n == nil {
		return ""
	}
	if n.Kind != yaml.ScalarNode {
		r.bad(n, "%s is a text", what)
		return ""
	}
	if n.Tag == "!!null" {
		return ""
	}
	return n.Value
}

func (r *reader) num(n *yaml.Node, what string) (float64, bool) {
	if n == nil {
		return 0, false
	}
	f, err := strconv.ParseFloat(n.Value, 64)
	if n.Kind != yaml.ScalarNode || err != nil {
		r.bad(n, "%s is a number", what)
		return 0, false
	}
	return f, true
}

func (r *reader) seq(n *yaml.Node, what string) []*yaml.Node {
	if n == nil || n.Tag == "!!null" {
		return nil
	}
	if n.Kind != yaml.SequenceNode {
		r.bad(n, "%s is a list", what)
		return nil
	}
	return n.Content
}

func (r *reader) strs(n *yaml.Node, what string) []string {
	var out []string
	for _, e := range r.seq(n, what) {
		out = append(out, r.str(e, what+" item"))
	}
	return out
}

// Parse reads a diagram's YAML. The diagram comes back as far as it reads,
// with what is wrong in it; nil when it does not read at all.
func Parse(src string) (*Diagram, []Problem) {
	if len(src) > MaxBytes {
		return nil, []Problem{{Msg: fmt.Sprintf("the diagram is %d bytes, at most %d", len(src), MaxBytes)}}
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		line := 0
		if m := yamlLineRe.FindStringSubmatch(err.Error()); m != nil {
			line, _ = strconv.Atoi(m[1])
		}
		return nil, []Problem{{Line: line, Msg: "not valid YAML: " + strings.TrimPrefix(err.Error(), "yaml: ")}}
	}
	if len(doc.Content) == 0 {
		return nil, []Problem{{Msg: "the block is empty: a diagram has kind and its content (docs/diagrams.md)"}}
	}
	root := doc.Content[0]
	r := &reader{ids: map[string]int{}}
	if root.Kind != yaml.MappingNode {
		r.bad(root, "a diagram is a mapping: kind, then its content")
		return nil, r.problems
	}
	d := &Diagram{Version: Version}
	top := map[string]*yaml.Node{}
	for _, p := range pairs(root) {
		top[p.key.Value] = p.val
	}
	if v := top["diagram"]; v != nil {
		n, err := strconv.Atoi(v.Value)
		if err != nil || n != Version {
			r.bad(v, "diagram: %s is a format this server does not read (it reads %d)", v.Value, Version)
			return nil, r.problems
		}
	}
	kn := top["kind"]
	d.Kind = Kind(r.str(kn, "kind"))
	allowed, ok := kindFields[d.Kind]
	if !ok {
		names := make([]string, len(Kinds))
		for i, k := range Kinds {
			names[i] = string(k)
		}
		if kn == nil {
			r.bad(root, "kind is missing: one of %s", strings.Join(names, ", "))
		} else {
			r.bad(kn, "kind %q is not one of %s", d.Kind, strings.Join(names, ", "))
		}
		return nil, r.problems
	}
	for _, p := range pairs(root) {
		k := p.key.Value
		if !oneOf(k, []string{"diagram", "kind", "title", "description", "layout"}) && !oneOf(k, allowed) {
			r.bad(p.key, "a %s diagram has no field %q (it has %s)", d.Kind, k, strings.Join(allowed, ", "))
		}
	}
	d.Title = r.str(top["title"], "title")
	d.Description = r.str(top["description"], "description")
	switch d.Kind {
	case Flow:
		r.flow(d, top)
	case Tree, Mindmap, Fishbone:
		r.hierarchy(d, root, top)
	case Venn:
		r.venn(d, root, top)
	case Matrix:
		r.matrix(d, root, top)
	case Gantt:
		r.gantt(d, root, top)
	}
	r.layout(d, top["layout"])
	return d, r.problems
}

func (r *reader) direction(d *Diagram, n *yaml.Node) {
	d.Direction = "TB"
	if n == nil {
		return
	}
	v := strings.ToUpper(r.str(n, "direction"))
	if !oneOf(v, directions) {
		r.bad(n, "direction %q is one of %s", v, strings.Join(directions, ", "))
		return
	}
	d.Direction = v
}

// --- flow ---

func (r *reader) flow(d *Diagram, top map[string]*yaml.Node) {
	r.direction(d, top["direction"])
	nodes := r.seq(top["nodes"], "nodes")
	if len(nodes) == 0 {
		r.bad(top["nodes"], "a flow has nodes: [{id, label}, …]")
	}
	if !r.count(top["nodes"], len(nodes)) {
		return
	}
	for _, n := range nodes {
		node := Node{Line: n.Line, Shape: "box"}
		if n.Kind == yaml.ScalarNode {
			node.ID, node.Label = n.Value, n.Value
		} else {
			f := r.fields(n, "a node", "id", "label", "shape", "link", "class")
			node.ID = r.str(f["id"], "id")
			node.Label = r.str(f["label"], "label")
			if s := r.str(f["shape"], "shape"); s != "" {
				if !oneOf(s, shapes) {
					r.bad(f["shape"], "shape %q is one of %s", s, strings.Join(shapes, ", "))
				}
				node.Shape = s
			}
			if c := r.str(f["class"], "class"); c != "" {
				if !oneOf(c, classes) {
					r.bad(f["class"], "class %q is one of %s", c, strings.Join(classes, ", "))
				}
				node.Class = c
			}
			node.Link = r.linkOf(f)
			if f["id"] == nil {
				r.bad(n, "a node has an id")
				continue
			}
		}
		r.id(n, node.ID)
		if node.Label == "" {
			node.Label = node.ID
		}
		d.Nodes = append(d.Nodes, node)
	}
	groups := r.seq(top["groups"], "groups")
	if !r.count(top["groups"], len(groups)) {
		return
	}
	inGroup := map[string]string{}
	known := func(id string) bool { _, ok := r.ids[id]; return ok }
	for _, n := range groups {
		f := r.fields(n, "a group", "id", "label", "nodes")
		g := Group{ID: r.str(f["id"], "id"), Label: r.str(f["label"], "label"), Line: n.Line}
		if f["id"] == nil {
			r.bad(n, "a group has an id")
			continue
		}
		r.id(n, g.ID)
		for _, m := range r.strs(f["nodes"], "a group's nodes") {
			switch {
			case !known(m):
				r.bad(f["nodes"], "group %s holds %q, which is no node", g.ID, m)
			case inGroup[m] != "":
				r.bad(f["nodes"], "node %s is in groups %s and %s: a node is in one group", m, inGroup[m], g.ID)
			default:
				inGroup[m] = g.ID
			}
			g.Nodes = append(g.Nodes, m)
		}
		d.Groups = append(d.Groups, g)
	}
	edges := r.seq(top["edges"], "edges")
	if !r.count(top["edges"], len(edges)) {
		return
	}
	for _, n := range edges {
		e := Edge{Line: n.Line, Style: "solid"}
		if n.Kind == yaml.ScalarNode {
			m := edgeRe.FindStringSubmatch(n.Value)
			if m == nil {
				r.bad(n, "edge %q is \"from -> to\" or \"from -> to: label\"", n.Value)
				continue
			}
			e.From, e.To, e.Label = m[1], m[2], strings.TrimSpace(m[3])
		} else {
			f := r.fields(n, "an edge", "from", "to", "label", "style")
			e.From, e.To, e.Label = r.str(f["from"], "from"), r.str(f["to"], "to"), r.str(f["label"], "label")
			if s := r.str(f["style"], "style"); s != "" {
				if !oneOf(s, styles) {
					r.bad(f["style"], "style %q is one of %s", s, strings.Join(styles, ", "))
				}
				e.Style = s
			}
		}
		for _, end := range []string{e.From, e.To} {
			if !known(end) {
				r.bad(n, "the edge %s -> %s names %q, which is no node or group", e.From, e.To, end)
			}
		}
		d.Edges = append(d.Edges, e)
	}
}

// --- tree, mind map, fishbone ---

var branchFields = []string{"id", "label", "link", "children"}

func (r *reader) hierarchy(d *Diagram, root *yaml.Node, top map[string]*yaml.Node) {
	if d.Kind == Tree {
		r.direction(d, top["direction"])
	}
	rn := top["root"]
	if rn == nil {
		r.bad(root, "a %s has a root: its %s", d.Kind, map[Kind]string{Tree: "top", Mindmap: "centre", Fishbone: "effect, the problem"}[d.Kind])
		return
	}
	d.Root = r.branch(rn, 0)
	if d.Root == nil {
		return
	}
	if c := top["children"]; c != nil {
		d.Root.Children = append(d.Root.Children, r.children(c, 1)...)
	}
	if d.Kind == Fishbone && len(d.Root.Children) == 0 {
		r.bad(rn, "a fishbone has categories under its root (the 6M: methods, machines, materials…), and causes under them")
	}
}

func (r *reader) branch(n *yaml.Node, depth int) *Branch {
	if depth > maxDepth {
		r.bad(n, "the tree is deeper than %d levels", maxDepth)
		return nil
	}
	if !r.count(n, 1) {
		return nil
	}
	b := &Branch{Line: n.Line}
	switch n.Kind {
	case yaml.ScalarNode:
		b.Label = n.Value
	case yaml.MappingNode:
		ps := pairs(n)
		full := false
		for _, p := range ps {
			if oneOf(p.key.Value, branchFields) {
				full = true
			}
		}
		if !full && len(ps) == 1 {
			// {Label: [children]}
			b.Label = ps[0].key.Value
			b.Children = r.children(ps[0].val, depth+1)
			break
		}
		f := r.fields(n, "a branch", branchFields...)
		b.Label = r.str(f["label"], "label")
		b.Link = r.linkOf(f)
		if f["id"] != nil {
			b.ID = r.str(f["id"], "id")
			r.id(f["id"], b.ID)
		}
		b.Children = r.children(f["children"], depth+1)
	default:
		r.bad(n, "a branch is a text, {label, children} or {Label: [children]}")
		return nil
	}
	if strings.TrimSpace(b.Label) == "" {
		r.bad(n, "a branch has a label")
	}
	return b
}

func (r *reader) children(n *yaml.Node, depth int) []*Branch {
	if n == nil || n.Tag == "!!null" {
		return nil
	}
	if n.Kind == yaml.ScalarNode {
		if b := r.branch(n, depth); b != nil {
			return []*Branch{b}
		}
		return nil
	}
	var out []*Branch
	for _, c := range r.seq(n, "children") {
		if b := r.branch(c, depth); b != nil {
			out = append(out, b)
		}
	}
	return out
}

// --- venn ---

func (r *reader) venn(d *Diagram, root *yaml.Node, top map[string]*yaml.Node) {
	sets := r.seq(top["sets"], "sets")
	if len(sets) < 2 || len(sets) > 5 {
		at := top["sets"]
		if at == nil {
			at = root
		}
		r.bad(at, "a Venn has 2 to 5 sets, it has %d", len(sets))
	}
	if !r.count(top["sets"], len(sets)) {
		return
	}
	for _, n := range sets {
		s := Set{Line: n.Line}
		if n.Kind == yaml.ScalarNode {
			s.ID, s.Label = n.Value, n.Value
		} else {
			f := r.fields(n, "a set", "id", "label", "link")
			s.ID, s.Label = r.str(f["id"], "id"), r.str(f["label"], "label")
			s.Link = r.linkOf(f)
			if f["id"] == nil {
				r.bad(n, "a set has an id")
				continue
			}
		}
		r.id(n, s.ID)
		if s.Label == "" {
			s.Label = s.ID
		}
		d.Sets = append(d.Sets, s)
	}
	isSet := map[string]bool{}
	for _, s := range d.Sets {
		isSet[s.ID] = true
	}
	areas := r.seq(top["areas"], "areas")
	if !r.count(top["areas"], len(areas)) {
		return
	}
	seen := map[string]int{}
	for _, n := range areas {
		f := r.fields(n, "an area", "sets", "label", "size")
		a := Area{Label: r.str(f["label"], "label"), Line: n.Line}
		a.Sets = r.strs(f["sets"], "an area's sets")
		if len(a.Sets) == 0 {
			r.bad(n, "an area names its sets: {sets: [a, b], label}")
		}
		in := map[string]bool{}
		for _, s := range a.Sets {
			switch {
			case !isSet[s]:
				r.bad(f["sets"], "the area names %q, which is no set", s)
			case in[s]:
				r.bad(f["sets"], "the area names %s twice", s)
			}
			in[s] = true
		}
		key := strings.Join(slices.Sorted(slices.Values(a.Sets)), "+")
		if first, ok := seen[key]; ok {
			r.bad(n, "the area of %s is there twice (first at line %d)", strings.Join(a.Sets, ", "), first)
		}
		seen[key] = n.Line
		if f["size"] != nil {
			if v, ok := r.num(f["size"], "size"); ok {
				if v < 0 {
					r.bad(f["size"], "size is not negative")
				}
				a.Size = v
			}
		}
		d.Areas = append(d.Areas, a)
	}
}

// --- matrix ---

func (r *reader) matrix(d *Diagram, root *yaml.Node, top map[string]*yaml.Node) {
	quad := top["x"] != nil || top["y"] != nil || top["quadrants"] != nil || top["items"] != nil
	grid := top["rows"] != nil || top["columns"] != nil || top["cells"] != nil
	m := &MatrixSpec{}
	d.Matrix = m
	switch {
	case quad && grid:
		r.bad(root, "a matrix is quadrants (x, y, quadrants, items) or a grid (rows, columns, cells), not both")
		return
	case !quad && !grid:
		r.bad(root, "a matrix has x, y and quadrants, or rows, columns and cells")
		return
	case grid:
		m.Rows = r.strs(top["rows"], "rows")
		m.Columns = r.strs(top["columns"], "columns")
		if len(m.Rows) == 0 || len(m.Columns) == 0 {
			r.bad(root, "a grid has rows and columns")
		}
		cells := r.seq(top["cells"], "cells")
		if len(cells) > len(m.Rows) {
			r.bad(top["cells"], "cells has %d rows, the grid %d", len(cells), len(m.Rows))
		}
		for _, row := range cells {
			vals := r.strs(row, "a row of cells")
			if !r.count(row, len(vals)) {
				return
			}
			if len(vals) > len(m.Columns) {
				r.bad(row, "the row has %d cells, the grid %d columns", len(vals), len(m.Columns))
			}
			m.Cells = append(m.Cells, vals)
		}
		return
	}
	axis := func(n *yaml.Node, name string) *Axis {
		if n == nil {
			r.bad(root, "a quadrant matrix has %s: {label, low, high}", name)
			return nil
		}
		if n.Kind == yaml.ScalarNode {
			return &Axis{Label: n.Value}
		}
		f := r.fields(n, name, "label", "low", "high")
		return &Axis{Label: r.str(f["label"], "label"), Low: r.str(f["low"], "low"), High: r.str(f["high"], "high")}
	}
	m.X, m.Y = axis(top["x"], "x"), axis(top["y"], "y")
	if q := top["quadrants"]; q != nil {
		m.Quadrants = r.strs(q, "quadrants")
		if len(m.Quadrants) != 4 {
			r.bad(q, "quadrants has 4 labels (top left, top right, bottom left, bottom right), it has %d", len(m.Quadrants))
		}
	}
	items := r.seq(top["items"], "items")
	if !r.count(top["items"], len(items)) {
		return
	}
	for _, n := range items {
		f := r.fields(n, "an item", "label", "x", "y", "link")
		it := Item{Label: r.str(f["label"], "label"), Line: n.Line}
		it.Link = r.linkOf(f)
		for name, dst := range map[string]*float64{"x": &it.X, "y": &it.Y} {
			v, ok := r.num(f[name], name)
			if !ok {
				if f[name] == nil {
					r.bad(n, "an item has %s, from 0 to 1", name)
				}
				continue
			}
			if v < 0 || v > 1 {
				r.bad(f[name], "%s is from 0 to 1", name)
			}
			*dst = v
		}
		if it.Label == "" {
			r.bad(n, "an item has a label")
		}
		m.Items = append(m.Items, it)
	}
}

// --- gantt ---

func (r *reader) gantt(d *Diagram, root *yaml.Node, top map[string]*yaml.Node) {
	if top["sections"] != nil && top["tasks"] != nil {
		r.bad(root, "a Gantt has sections, or tasks without sections, not both")
		return
	}
	var tasksAt []*yaml.Node
	if t := top["tasks"]; t != nil {
		d.Sections = []Section{{}}
		tasksAt = []*yaml.Node{t}
	} else {
		for _, n := range r.seq(top["sections"], "sections") {
			f := r.fields(n, "a section", "label", "tasks")
			d.Sections = append(d.Sections, Section{Label: r.str(f["label"], "label")})
			tasksAt = append(tasksAt, f["tasks"])
		}
	}
	if len(d.Sections) == 0 {
		r.bad(root, "a Gantt has tasks, or sections with tasks")
		return
	}
	for i, tn := range tasksAt {
		tasks := r.seq(tn, "tasks")
		if !r.count(tn, len(tasks)) {
			return
		}
		for _, n := range tasks {
			d.Sections[i].Tasks = append(d.Sections[i].Tasks, r.task(n))
		}
	}
	// after names a task that exists, and no chain comes back.
	byID := map[string]Task{}
	for _, s := range d.Sections {
		for _, t := range s.Tasks {
			if t.ID != "" {
				byID[t.ID] = t
			}
		}
	}
	for _, s := range d.Sections {
		for _, t := range s.Tasks {
			if t.After == "" {
				continue
			}
			if _, ok := byID[t.After]; !ok {
				r.problems = append(r.problems, Problem{Line: t.Line, Msg: fmt.Sprintf("task %q comes after %q, which is no task", t.Label, t.After)})
				continue
			}
			seen := map[string]bool{t.ID: true}
			for cur := t.After; cur != ""; cur = byID[cur].After {
				if seen[cur] {
					r.problems = append(r.problems, Problem{Line: t.Line, Msg: fmt.Sprintf("task %q waits for itself through after", t.Label)})
					break
				}
				seen[cur] = true
			}
		}
	}
}

func (r *reader) task(n *yaml.Node) Task {
	f := r.fields(n, "a task", "id", "label", "link", "start", "end", "after", "days", "status")
	t := Task{Line: n.Line, Label: r.str(f["label"], "label")}
	if t.Label == "" {
		r.bad(n, "a task has a label")
	}
	if f["id"] != nil {
		t.ID = r.str(f["id"], "id")
		r.id(f["id"], t.ID)
	}
	t.Link = r.linkOf(f)
	if s := r.str(f["status"], "status"); s != "" {
		if !oneOf(s, statuses) {
			r.bad(f["status"], "status %q is one of %s", s, strings.Join(statuses, ", "))
		}
		t.Status = s
	}
	t.Start, t.End, t.After = r.str(f["start"], "start"), r.str(f["end"], "end"), r.str(f["after"], "after")
	var start, end time.Time
	var err error
	switch {
	case t.Start != "" && t.After != "":
		r.bad(n, "a task has start or after, not both")
	case t.Start == "" && t.After == "":
		r.bad(n, "a task has start (YYYY-MM-DD) or after (a task's id)")
	case t.Start != "":
		if start, err = time.Parse("2006-01-02", t.Start); err != nil {
			r.bad(f["start"], "start %q is a date, YYYY-MM-DD", t.Start)
		}
	}
	if f["days"] != nil {
		v, ok := r.num(f["days"], "days")
		if ok && (v < 1 || v != float64(int(v))) {
			r.bad(f["days"], "days is a whole number of days, at least 1")
		}
		t.Days = int(v)
	}
	switch {
	case t.End != "" && f["days"] != nil:
		r.bad(n, "a task has end or days, not both")
	case t.End != "":
		if end, err = time.Parse("2006-01-02", t.End); err != nil {
			r.bad(f["end"], "end %q is a date, YYYY-MM-DD", t.End)
		} else if !start.IsZero() && end.Before(start) {
			r.bad(f["end"], "end %s comes before start %s", t.End, t.Start)
		}
	case t.Status != "milestone" && f["days"] == nil:
		r.bad(n, "a task has end (YYYY-MM-DD) or days, unless it is a milestone")
	}
	return t
}

// --- layout ---

func (r *reader) layout(d *Diagram, n *yaml.Node) {
	if n == nil || n.Tag == "!!null" {
		return
	}
	if n.Kind != yaml.MappingNode {
		r.bad(n, "layout is a mapping: id: {x, y}")
		return
	}
	d.Layout = map[string]Point{}
	for _, p := range pairs(n) {
		id := p.key.Value
		if _, ok := r.ids[id]; !ok {
			r.bad(p.key, "layout places %q, which is no id of the diagram", id)
			continue
		}
		f := r.fields(p.val, "a position", "x", "y", "w", "h")
		var pt Point
		for name, dst := range map[string]*float64{"x": &pt.X, "y": &pt.Y, "w": &pt.W, "h": &pt.H} {
			if f[name] != nil {
				if v, ok := r.num(f[name], name); ok {
					*dst = v
				}
			}
		}
		if f["x"] == nil || f["y"] == nil {
			r.bad(p.val, "a position has x and y")
		}
		d.Layout[id] = pt
	}
}
