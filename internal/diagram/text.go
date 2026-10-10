package diagram

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// maxText caps a diagram said in words.
const maxText = 1500

// Text says a diagram in words: what an agent reads beside the YAML, and
// the drawing's text alternative. Server-made text, in English.
func Text(d *Diagram) string {
	if d == nil {
		return ""
	}
	var b strings.Builder
	switch d.Kind {
	case Flow:
		labels := map[string]string{}
		for _, n := range d.Nodes {
			labels[n.ID] = n.Label
		}
		for _, g := range d.Groups {
			labels[g.ID] = g.Label
		}
		fmt.Fprintf(&b, "Flow (%s), %s, %s", d.Direction, count(len(d.Nodes), "node"), count(len(d.Edges), "edge"))
		if len(d.Groups) > 0 {
			fmt.Fprintf(&b, ", %s", count(len(d.Groups), "group"))
		}
		b.WriteString(":")
		for i, e := range d.Edges {
			if i > 0 {
				b.WriteString(";")
			}
			fmt.Fprintf(&b, " %s → %s", or(labels[e.From], e.From), or(labels[e.To], e.To))
			if e.Label != "" {
				fmt.Fprintf(&b, " (%s)", e.Label)
			}
		}
		// The nodes no edge reaches, so none goes unsaid.
		linked := map[string]bool{}
		for _, e := range d.Edges {
			linked[e.From], linked[e.To] = true, true
		}
		var alone []string
		for _, n := range d.Nodes {
			if !linked[n.ID] {
				alone = append(alone, n.Label)
			}
		}
		if len(alone) > 0 {
			fmt.Fprintf(&b, "; unconnected: %s", strings.Join(alone, ", "))
		}
	case Tree, Mindmap, Fishbone:
		name := map[Kind]string{Tree: "Tree", Mindmap: "Mind map", Fishbone: "Fishbone"}[d.Kind]
		if d.Root == nil {
			return name
		}
		if d.Kind == Fishbone {
			fmt.Fprintf(&b, "Fishbone of the effect %q:", d.Root.Label)
		} else {
			fmt.Fprintf(&b, "%s of %q:", name, d.Root.Label)
		}
		for i, c := range d.Root.Children {
			if i > 0 {
				b.WriteString(";")
			}
			b.WriteString(" ")
			writeBranch(&b, c)
		}
	case Venn:
		names := make([]string, 0, len(d.Sets))
		labels := map[string]string{}
		for _, s := range d.Sets {
			names, labels[s.ID] = append(names, s.Label), s.Label
		}
		fmt.Fprintf(&b, "Venn of %s: %s", count(len(d.Sets), "set"), strings.Join(names, ", "))
		for _, a := range d.Areas {
			parts := make([]string, 0, len(a.Sets))
			for _, s := range a.Sets {
				parts = append(parts, or(labels[s], s))
			}
			fmt.Fprintf(&b, "; %s", strings.Join(parts, " ∩ "))
			if a.Label != "" {
				fmt.Fprintf(&b, ": %s", a.Label)
			}
		}
	case Matrix:
		m := d.Matrix
		if m == nil {
			return "Matrix"
		}
		if len(m.Rows) > 0 {
			fmt.Fprintf(&b, "Matrix of %s by %s (%s × %s)", count(len(m.Rows), "row"), count(len(m.Columns), "column"),
				strings.Join(m.Rows, ", "), strings.Join(m.Columns, ", "))
			for i, row := range m.Cells {
				if i >= len(m.Rows) {
					break
				}
				var cells []string
				for j, c := range row {
					if c != "" && j < len(m.Columns) {
						cells = append(cells, m.Columns[j]+": "+c)
					}
				}
				if len(cells) > 0 {
					fmt.Fprintf(&b, "; %s — %s", m.Rows[i], strings.Join(cells, ", "))
				}
			}
			break
		}
		x, y := "x", "y"
		if m.X != nil && m.X.Label != "" {
			x = m.X.Label
		}
		if m.Y != nil && m.Y.Label != "" {
			y = m.Y.Label
		}
		fmt.Fprintf(&b, "Quadrant matrix, %s across, %s up", x, y)
		if len(m.Quadrants) == 4 {
			fmt.Fprintf(&b, "; quadrants: top left %s, top right %s, bottom left %s, bottom right %s", m.Quadrants[0], m.Quadrants[1], m.Quadrants[2], m.Quadrants[3])
		}
		for _, it := range m.Items {
			fmt.Fprintf(&b, "; %s at (%s, %s)", it.Label, num(it.X), num(it.Y))
		}
	case Gantt:
		n := 0
		for _, s := range d.Sections {
			n += len(s.Tasks)
		}
		labels := map[string]string{}
		for _, s := range d.Sections {
			for _, t := range s.Tasks {
				if t.ID != "" {
					labels[t.ID] = t.Label
				}
			}
		}
		fmt.Fprintf(&b, "Gantt, %s", count(n, "task"))
		for _, s := range d.Sections {
			if s.Label != "" {
				fmt.Fprintf(&b, "; %s:", s.Label)
			}
			for i, t := range s.Tasks {
				if i > 0 || s.Label == "" {
					b.WriteString(";")
				}
				fmt.Fprintf(&b, " %s", t.Label)
				switch {
				case t.Start != "":
					fmt.Fprintf(&b, " from %s", t.Start)
				case t.After != "":
					fmt.Fprintf(&b, " after %s", or(labels[t.After], t.After))
				}
				switch {
				case t.End != "":
					fmt.Fprintf(&b, " to %s", t.End)
				case t.Days > 0:
					fmt.Fprintf(&b, " for %s", count(t.Days, "day"))
				}
				if t.Status != "" {
					fmt.Fprintf(&b, " [%s]", t.Status)
				}
			}
		}
	}
	text := strings.TrimSpace(b.String())
	if d.Title != "" {
		text = d.Title + " — " + text
	}
	if len(text) > maxText {
		cut := text[:maxText]
		for !utf8.ValidString(cut) {
			cut = cut[:len(cut)-1] // not inside a rune
		}
		text = cut + "…"
	}
	return text
}

func writeBranch(b *strings.Builder, br *Branch) {
	b.WriteString(br.Label)
	if len(br.Children) == 0 {
		return
	}
	b.WriteString(" (")
	for i, c := range br.Children {
		if i > 0 {
			b.WriteString(", ")
		}
		writeBranch(b, c)
	}
	b.WriteString(")")
}

func count(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return strconv.Itoa(n) + " " + what + "s"
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func num(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
