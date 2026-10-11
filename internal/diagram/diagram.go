// Package diagram reads the diagrams of the notes (IMP-169, ADR-036): a
// ```diagram block of YAML, versioned (diagram: 1), whose kind is a flow, a
// tree, a mind map, a fishbone, a Venn, a matrix or a Gantt. The YAML is the
// source of truth: the editors, the drawings and the conversions (Mermaid,
// JSON Canvas, SVG) read and write it, and nothing lives in the geometry
// alone. This package checks a diagram, lists its links and says it in
// words, for the lint, the write notices, the index and the agents.
package diagram

import (
	"strconv"
	"strings"

	"github.com/gosidian/gosidian/internal/parser"
)

const (
	// Lang is the info string of a diagram's fence.
	Lang = "diagram"
	// NoteType is the type of a note that is one diagram.
	NoteType = "diagram"
	// Version is the format this server reads.
	Version = 1
	// MaxBytes caps a diagram's YAML.
	MaxBytes = 256 << 10
	// MaxItems caps its nodes, edges, branches, sets, cells and tasks.
	MaxItems = 2000
	// maxDepth caps the nesting of a tree.
	maxDepth = 20
)

// Kind is what a diagram draws.
type Kind string

const (
	Flow     Kind = "flow"
	Tree     Kind = "tree"
	Mindmap  Kind = "mindmap"
	Fishbone Kind = "fishbone"
	Venn     Kind = "venn"
	Matrix   Kind = "matrix"
	Gantt    Kind = "gantt"
)

// Kinds lists the kinds, in the order of the docs.
var Kinds = []Kind{Flow, Tree, Mindmap, Fishbone, Venn, Matrix, Gantt}

func (k Kind) hierarchy() bool { return k == Tree || k == Mindmap || k == Fishbone }

// Diagram is a diagram as read, its short forms made full.
type Diagram struct {
	Version     int
	Kind        Kind
	Title       string
	Description string
	// Direction: TB, LR, BT, RL (flow, tree).
	Direction string

	Nodes  []Node  // flow
	Edges  []Edge  // flow
	Groups []Group // flow

	Root *Branch // tree, mindmap, fishbone

	Sets  []Set  // venn
	Areas []Area // venn

	Matrix *MatrixSpec // matrix

	Sections []Section // gantt

	// Layout holds positions by id, written by an editor.
	Layout map[string]Point
}

// Node is a box of a flow.
type Node struct {
	ID, Label, Shape, Link, Class string
	Line                          int
}

// Edge is an arrow of a flow.
type Edge struct {
	From, To, Label, Style string
	Line                   int
}

// Group is a box around nodes of a flow.
type Group struct {
	ID, Label string
	Nodes     []string
	Line      int
}

// Branch is a node of a tree, a mind map or a fishbone.
type Branch struct {
	ID, Label, Link string
	Children        []*Branch
	Line            int
}

// Set is a circle of a Venn.
type Set struct {
	ID, Label, Link string
	Line            int
}

// Area is the part shared by some sets.
type Area struct {
	Sets  []string
	Label string
	Size  float64
	Line  int
}

// MatrixSpec is a 2×2 of quadrants, or a grid of cells.
type MatrixSpec struct {
	X, Y      *Axis
	Quadrants []string
	Items     []Item
	Rows      []string
	Columns   []string
	Cells     [][]string
}

// Axis is an axis of a quadrant matrix.
type Axis struct{ Label, Low, High string }

// Item is a point in a quadrant matrix, x and y in [0, 1].
type Item struct {
	Label, Link string
	X, Y        float64
	Line        int
}

// Section groups the tasks of a Gantt.
type Section struct {
	Label string
	Tasks []Task
}

// Task is a bar of a Gantt.
type Task struct {
	ID, Label, Link   string
	Start, End, After string
	Days              int
	Status            string
	Line              int
}

// Point is a position (and size) an editor stored.
type Point struct{ X, Y, W, H float64 }

// Problem is something wrong with a diagram; Line is 1-based in the block,
// 0 when it is the whole block.
type Problem struct {
	Line int    `json:"line,omitempty"`
	Msg  string `json:"message"`
}

func (p Problem) String() string {
	if p.Line > 0 {
		return "line " + strconv.Itoa(p.Line) + ": " + p.Msg
	}
	return p.Msg
}

// Block is a ```diagram block of a note: Line is the 1-based line of its
// fence in the note, Src its YAML.
type Block struct {
	Line int
	Src  string
}

// Find returns the diagram blocks of a note's body.
func Find(body []byte) []Block {
	fenced := parser.FindFenced(body, Lang)
	out := make([]Block, 0, len(fenced))
	for _, b := range fenced {
		out = append(out, Block{Line: strings.Count(string(body[:b.Start]), "\n") + 1, Src: b.Spec})
	}
	return out
}

// Links returns the targets of a diagram's [[links]], in order.
func Links(d *Diagram) []parser.WikiLinkRef {
	if d == nil {
		return nil
	}
	var out []parser.WikiLinkRef
	add := func(link string) {
		if strings.HasPrefix(link, "[[") {
			out = append(out, parser.WikiLinks(link)...)
		}
	}
	for _, n := range d.Nodes {
		add(n.Link)
	}
	var walk func(b *Branch)
	walk = func(b *Branch) {
		if b == nil {
			return
		}
		add(b.Link)
		for _, c := range b.Children {
			walk(c)
		}
	}
	walk(d.Root)
	for _, s := range d.Sets {
		add(s.Link)
	}
	if d.Matrix != nil {
		for _, it := range d.Matrix.Items {
			add(it.Link)
		}
	}
	for _, s := range d.Sections {
		for _, t := range s.Tasks {
			add(t.Link)
		}
	}
	return out
}

// LinksIn returns the [[links]] of every diagram in a note's body, as far
// as each one reads: the index counts them as the note's links.
func LinksIn(body []byte) []parser.WikiLinkRef {
	var out []parser.WikiLinkRef
	for _, b := range Find(body) {
		d, _ := Parse(b.Src)
		out = append(out, Links(d)...)
	}
	return out
}

// Summary is a diagram of a note as an agent reads it.
type Summary struct {
	Kind     Kind      `json:"kind,omitempty"`
	Title    string    `json:"title,omitempty"`
	Line     int       `json:"line"`
	Text     string    `json:"text,omitempty"`
	Problems []Problem `json:"problems,omitempty"`
	// Twin is the state of the diagram's Mermaid twin, when it has one.
	Twin TwinState `json:"twin,omitempty"`
}

// Summaries says the diagrams of a note's body; nil without any.
func Summaries(body []byte) []Summary {
	blocks := Find(body)
	if len(blocks) == 0 {
		return nil
	}
	twins := map[int]TwinState{}
	for _, t := range Twins(body) {
		if t.DiagramLine > 0 {
			twins[t.DiagramLine] = t.State
		}
	}
	out := make([]Summary, 0, len(blocks))
	for _, b := range blocks {
		d, problems := Parse(b.Src)
		s := Summary{Line: b.Line, Problems: problems, Twin: twins[b.Line]}
		if d != nil {
			s.Kind, s.Title, s.Text = d.Kind, d.Title, Text(d)
		}
		out = append(out, s)
	}
	return out
}
