package canvas

import (
	"strings"
	"testing"
)

// roadmap is a canvas as Obsidian writes it: a group with a text and a
// file card, a nested group, a link card outside, connections with and
// without arrows and labels.
const roadmap = `{
	"nodes":[
		{"id":"g1","type":"group","x":-40,"y":-40,"width":900,"height":600,"label":"Fase 1","color":"4"},
		{"id":"g2","type":"group","x":400,"y":0,"width":400,"height":300,"label":"Dettagli"},
		{"id":"t1","type":"text","x":0,"y":0,"width":300,"height":120,"text":"# Scrivere i test\nPrima i casi limite.\n"},
		{"id":"f1","type":"file","x":450,"y":40,"width":300,"height":200,"file":"p/plans/a.md","subpath":"#Context","color":"#ff0000"},
		{"id":"l1","type":"link","x":1000,"y":0,"width":300,"height":100,"url":"https://jsoncanvas.org","color":"red; background:url(x)"},
		{"id":"t2","type":"text","x":0,"y":300,"width":300,"height":100,"text":""},
		{"id":"","type":"text","x":0,"y":0,"width":1,"height":1,"text":"no id"}
	],
	"edges":[
		{"id":"e1","fromNode":"t1","fromSide":"right","toNode":"f1","toSide":"left","label":"poi"},
		{"id":"e2","fromNode":"f1","toNode":"l1","fromEnd":"arrow","toEnd":"arrow"},
		{"id":"e3","fromNode":"l1","toNode":"t1","toEnd":"none"},
		{"id":"e4","fromNode":"t1","toNode":"gone"}
	]
}`

func TestParse(t *testing.T) {
	c, err := Parse([]byte(roadmap))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Nodes) != 6 || len(c.Edges) != 3 {
		t.Fatalf("nodes %d, edges %d: a card without an id and a connection to a missing card are left out", len(c.Nodes), len(c.Edges))
	}
	colors := map[string]string{}
	for _, n := range c.Nodes {
		colors[n.ID] = n.Color
	}
	if colors["g1"] != "4" || colors["f1"] != "#ff0000" || colors["l1"] != "" {
		t.Errorf("colors = %v", colors)
	}
	if c, err := Parse([]byte("  \n")); err != nil || len(c.Nodes) != 0 {
		t.Errorf("empty file: %+v %v", c, err)
	}
	if _, err := Parse([]byte("{nodes")); err == nil || !strings.Contains(err.Error(), "not a JSON canvas") {
		t.Errorf("bad JSON: %v", err)
	}
}

func TestColor(t *testing.T) {
	for in, want := range map[string]string{"1": "1", "6": "6", "7": "", "0": "", "#abc": "#abc", "#A1B2C3": "#A1B2C3", "#abcd": "", "red": "", "#12345g": "", "": ""} {
		if got := Color(in); got != want {
			t.Errorf("Color(%q) = %q, want %q", in, got, want)
		}
	}
}

// A card belongs to the smallest group whose rectangle holds it.
func TestParents(t *testing.T) {
	c, _ := Parse([]byte(roadmap))
	p := c.Parents()
	want := map[string]string{"g2": "g1", "t1": "g1", "f1": "g2", "t2": "g1"}
	if len(p) != len(want) {
		t.Errorf("parents = %v, want %v", p, want)
	}
	for k, v := range want {
		if p[k] != v {
			t.Errorf("parent of %s = %q, want %q", k, p[k], v)
		}
	}
}

func TestMarkdown(t *testing.T) {
	c, _ := Parse([]byte(roadmap))
	got := string(Markdown("p/roadmap.canvas", c, nil))
	want := `# roadmap

An Obsidian canvas, read-only in gosidian: 2 text cards, 1 file card, 1 link card, 2 groups, 3 connections.

## Cards

- **Group** Fase 1
  - **Text**
    > # Scrivere i test
    > Prima i casi limite.
  - **Group** Dettagli
    - **File** [[p/plans/a]] #Context
  - **Text** (empty)
- **Link** <https://jsoncanvas.org>

## Connections

- «Scrivere i test» → [[p/plans/a]] #Context: poi
- [[p/plans/a]] #Context ↔ <https://jsoncanvas.org>
- <https://jsoncanvas.org> — «Scrivere i test»
`
	if got != want {
		t.Errorf("markdown:\n%s\nwant:\n%s", got, want)
	}
	// The linker decides how a file card reads: a note the reader may not
	// see stays its path.
	got = string(Markdown("p/roadmap.canvas", c, func(file string) string { return "`" + file + "`" }))
	if !strings.Contains(got, "- **File** `p/plans/a.md` #Context") {
		t.Errorf("linker:\n%s", got)
	}
	if got := string(Markdown("x.canvas", &Canvas{}, nil)); got != "# x\n\nAn Obsidian canvas, read-only in gosidian: 0 connections.\n" {
		t.Errorf("empty: %q", got)
	}
}
