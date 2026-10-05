// Package canvas reads Obsidian's .canvas files (IMP-144): JSON Canvas 1.0
// (https://jsoncanvas.org), cards laid out on a plane and the arrows between
// them. gosidian shows a canvas read-only and never writes it, like a base
// (ADR-021): Markdown gives agents a text that reads as a note, and the web
// UI draws the cards where the file puts them.
package canvas

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Canvas is a .canvas file: its cards (nodes) and connections (edges).
type Canvas struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// Node is a card: a text, a file of the vault, a web link, or a group that
// holds the cards inside its rectangle.
type Node struct {
	ID     string  `json:"id"`
	Type   string  `json:"type"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	Color  string  `json:"color,omitempty"`
	// Text is the markdown of a text card.
	Text string `json:"text,omitempty"`
	// File is the vault-relative path of a file card, and Subpath a heading
	// or block of it (#Heading).
	File    string `json:"file,omitempty"`
	Subpath string `json:"subpath,omitempty"`
	// URL is the address of a link card.
	URL string `json:"url,omitempty"`
	// Label is the name of a group.
	Label string `json:"label,omitempty"`
}

// Edge is a connection from one card to another; each end has a side (top,
// right, bottom, left) and a shape (none or arrow: by default none at the
// start, an arrow at the end).
type Edge struct {
	ID       string `json:"id"`
	FromNode string `json:"fromNode"`
	FromSide string `json:"fromSide,omitempty"`
	FromEnd  string `json:"fromEnd,omitempty"`
	ToNode   string `json:"toNode"`
	ToSide   string `json:"toSide,omitempty"`
	ToEnd    string `json:"toEnd,omitempty"`
	Color    string `json:"color,omitempty"`
	Label    string `json:"label,omitempty"`
}

// Node types.
const (
	TypeText  = "text"
	TypeFile  = "file"
	TypeLink  = "link"
	TypeGroup = "group"
)

// MaxNodes bounds the cards of a canvas gosidian reads.
const MaxNodes = 5000

// Parse reads a .canvas file. An empty file is an empty canvas, as Obsidian
// writes a new one; a card without an id, or a connection to a card that is
// not there, is left out.
func Parse(src []byte) (*Canvas, error) {
	c := &Canvas{}
	if strings.TrimSpace(string(src)) == "" {
		return c, nil
	}
	if err := json.Unmarshal(src, c); err != nil {
		return nil, fmt.Errorf("not a JSON canvas: %w", err)
	}
	if len(c.Nodes) > MaxNodes {
		return nil, fmt.Errorf("the canvas has %d cards, over %d", len(c.Nodes), MaxNodes)
	}
	ids := map[string]bool{}
	nodes := c.Nodes[:0]
	for _, n := range c.Nodes {
		if n.ID == "" || ids[n.ID] {
			continue
		}
		ids[n.ID] = true
		n.Color = Color(n.Color)
		nodes = append(nodes, n)
	}
	c.Nodes = nodes
	edges := c.Edges[:0]
	for _, e := range c.Edges {
		if !ids[e.FromNode] || !ids[e.ToNode] {
			continue
		}
		e.Color = Color(e.Color)
		edges = append(edges, e)
	}
	c.Edges = edges
	return c, nil
}

var hexColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{3}(?:[0-9a-fA-F]{3})?$`)

// Color is a card's or connection's color as JSON Canvas writes it — a
// preset from "1" (red) to "6" (purple) or a hex color — or "" for anything
// else, so the value can go into a style as it is.
func Color(c string) string {
	c = strings.TrimSpace(c)
	if len(c) == 1 && c >= "1" && c <= "6" || hexColorRe.MatchString(c) {
		return c
	}
	return ""
}

// Parents maps each card to the group that holds it: the smallest group
// whose rectangle contains the card's, as Obsidian moves a group's cards
// with it. A card in no group has no entry.
func (c *Canvas) Parents() map[string]string {
	var groups []Node
	for _, n := range c.Nodes {
		if n.Type == TypeGroup {
			groups = append(groups, n)
		}
	}
	out := map[string]string{}
	for _, n := range c.Nodes {
		best := -1
		for i, g := range groups {
			if g.ID == n.ID || !contains(g, n) {
				continue
			}
			if best < 0 || g.Width*g.Height < groups[best].Width*groups[best].Height {
				best = i
			}
		}
		if best >= 0 {
			out[n.ID] = groups[best].ID
		}
	}
	return out
}

func contains(g, n Node) bool {
	return n.X >= g.X && n.Y >= g.Y && n.X+n.Width <= g.X+g.Width && n.Y+n.Height <= g.Y+g.Height &&
		!(n.Type == TypeGroup && n.Width == g.Width && n.Height == g.Height && n.X == g.X && n.Y == g.Y)
}

// readingOrder sorts cards top to bottom, then left to right.
func readingOrder(nodes []Node) {
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].Y != nodes[j].Y {
			return nodes[i].Y < nodes[j].Y
		}
		return nodes[i].X < nodes[j].X
	})
}

// Linker writes the link to a file card: a wikilink to the note when it is
// one the reader may see, else the path as text. Nil writes every file as a
// wikilink to its path.
type Linker func(file string) string

// Markdown is the canvas as a note to read: what it holds, its groups with
// their cards in reading order, the cards outside any group, then the
// connections. Text cards keep their markdown, quoted; file cards are
// links; a card is named in a connection by its label.
func Markdown(rel string, c *Canvas, link Linker) []byte {
	if link == nil {
		link = func(file string) string { return "[[" + strings.TrimSuffix(file, ".md") + "]]" }
	}
	var b strings.Builder
	title := rel
	if i := strings.LastIndex(title, "/"); i >= 0 {
		title = title[i+1:]
	}
	title = strings.TrimSuffix(title, ".canvas")
	fmt.Fprintf(&b, "# %s\n\n", title)

	counts := map[string]int{}
	for _, n := range c.Nodes {
		counts[n.Type]++
	}
	var parts []string
	for _, k := range []struct{ typ, one, many string }{
		{TypeText, "text card", "text cards"}, {TypeFile, "file card", "file cards"},
		{TypeLink, "link card", "link cards"}, {TypeGroup, "group", "groups"},
	} {
		switch counts[k.typ] {
		case 0:
		case 1:
			parts = append(parts, "1 "+k.one)
		default:
			parts = append(parts, fmt.Sprintf("%d %s", counts[k.typ], k.many))
		}
	}
	parts = append(parts, plural(len(c.Edges), "connection", "connections"))
	fmt.Fprintf(&b, "An Obsidian canvas, read-only in gosidian: %s.\n", strings.Join(parts, ", "))
	if len(c.Nodes) == 0 {
		return []byte(b.String())
	}

	byID := map[string]Node{}
	for _, n := range c.Nodes {
		byID[n.ID] = n
	}
	parents := c.Parents()
	children := map[string][]Node{}
	var top []Node
	for _, n := range c.Nodes {
		if p, ok := parents[n.ID]; ok {
			children[p] = append(children[p], n)
		} else {
			top = append(top, n)
		}
	}
	name := func(n Node) string { return cardName(n, link) }

	var writeCard func(n Node, depth int)
	writeCard = func(n Node, depth int) {
		indent := strings.Repeat("  ", depth)
		switch n.Type {
		case TypeGroup:
			label := n.Label
			if label == "" {
				label = "(untitled)"
			}
			fmt.Fprintf(&b, "%s- **Group** %s\n", indent, label)
			kids := children[n.ID]
			readingOrder(kids)
			for _, k := range kids {
				writeCard(k, depth+1)
			}
		case TypeText:
			text := strings.TrimSpace(n.Text)
			if text == "" {
				fmt.Fprintf(&b, "%s- **Text** (empty)\n", indent)
				return
			}
			fmt.Fprintf(&b, "%s- **Text**\n", indent)
			for _, line := range strings.Split(text, "\n") {
				fmt.Fprintf(&b, "%s  > %s\n", indent, strings.TrimRight(line, " \t\r"))
			}
		case TypeFile:
			fmt.Fprintf(&b, "%s- **File** %s\n", indent, name(n))
		case TypeLink:
			fmt.Fprintf(&b, "%s- **Link** <%s>\n", indent, n.URL)
		default:
			fmt.Fprintf(&b, "%s- **%s** %s\n", indent, n.Type, n.ID)
		}
	}
	readingOrder(top)
	b.WriteString("\n## Cards\n\n")
	for _, n := range top {
		writeCard(n, 0)
	}

	if len(c.Edges) > 0 {
		b.WriteString("\n## Connections\n\n")
		for _, e := range c.Edges {
			arrow := "→"
			from, to := e.FromEnd == "arrow", e.ToEnd != "none"
			switch {
			case from && to:
				arrow = "↔"
			case from:
				arrow = "←"
			case !to:
				arrow = "—"
			}
			line := fmt.Sprintf("- %s %s %s", name(byID[e.FromNode]), arrow, name(byID[e.ToNode]))
			if l := strings.TrimSpace(e.Label); l != "" {
				line += ": " + strings.ReplaceAll(l, "\n", " ")
			}
			b.WriteString(line + "\n")
		}
	}
	return []byte(b.String())
}

// cardName is how a connection names a card: a group by its label, a text
// by its first line, a file by its link, a web link by its address.
func cardName(n Node, link Linker) string {
	switch n.Type {
	case TypeGroup:
		if n.Label != "" {
			return "group «" + n.Label + "»"
		}
		return "group " + n.ID
	case TypeText:
		first := strings.TrimSpace(n.Text)
		if i := strings.IndexByte(first, '\n'); i >= 0 {
			first = strings.TrimSpace(first[:i])
		}
		first = strings.TrimLeft(first, "# ")
		if r := []rune(first); len(r) > 60 {
			first = string(r[:59]) + "…"
		}
		if first == "" {
			return "text " + n.ID
		}
		return "«" + first + "»"
	case TypeFile:
		s := link(n.File)
		if n.Subpath != "" {
			s += " " + n.Subpath
		}
		return s
	case TypeLink:
		return "<" + n.URL + ">"
	}
	return n.ID
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
