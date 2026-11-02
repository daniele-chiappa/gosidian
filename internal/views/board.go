package views

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/gosidian/gosidian/internal/fleet"
	"github.com/gosidian/gosidian/internal/index"
)

// The board of a ```board block (IMP-216): the block says the columns of a
// board, and a read shows its cards there, the tasks of the note's project
// whose board is the note, as a view `as: board` would, grouped by their
// column instead of a select's options.

// boardColumns are the fields a card shows: who holds it, the notes it is
// for, who closes it. The rank orders the cards without showing.
var boardColumns = []string{"title", fleet.KeyClaimedBy, "for", "done_by"}

// computeBoard computes the board block src of the note in c.
func computeBoard(src string, c Context, q QueryFunc) (*Result, error) {
	note := ""
	if p := c.This["path"]; len(p) > 0 {
		note = p[0]
	}
	project, _, ok := strings.Cut(note, "/")
	if !ok {
		return nil, fmt.Errorf("board: the block of %q is in no project", note)
	}
	b, problems := fleet.ParseBoard(src, note)
	if len(problems) > 0 {
		return nil, fmt.Errorf("board: %s", problems[0])
	}
	spec := fmt.Sprintf("from: %s/%s\nwhere:\n  - board = \"%s\"\nas: board\ngroup_by: %s\nlimit: %d\ncolumns: [%s]\n",
		project, fleet.Folder, fleet.Link(note), fleet.KeyColumn, index.QueryMaxLimit, strings.Join(boardColumns, ", "))
	s, err := Parse(spec, c)
	if err != nil {
		return nil, err
	}
	s.Lanes, s.Read = b.Columns, []string{fleet.KeyRank}
	r, err := computeSpec(s, c, q)
	if err != nil {
		return nil, err
	}
	// The rows in the board's order, which the web UI keeps in each column.
	groups := r.lanes()
	r.Hits = r.Hits[:0:0]
	for _, g := range groups {
		r.Hits = append(r.Hits, g.Hits...)
	}
	return r, nil
}

// lanes are the groups of a board of cards: its columns in order, empty
// ones too, then the values found that are none of them, alphabetically,
// then the cards without a column (closed ones) when there are some. A
// column takes the cards that name it in any case, each by rank.
func (r *Result) lanes() []Group {
	field := r.Spec.GroupBy
	byLane := map[string][]index.QueryHit{}
	var extra []string
	for _, h := range r.Hits {
		v := ""
		if vs := h.Fields[field]; len(vs) > 0 {
			v = strings.TrimSpace(vs[0])
		}
		if i := slices.IndexFunc(r.Spec.Lanes, func(l fleet.Column) bool { return strings.EqualFold(l.Name, v) }); i >= 0 {
			v = r.Spec.Lanes[i].Name
		} else if _, seen := byLane[v]; !seen && v != "" {
			extra = append(extra, v)
		}
		byLane[v] = append(byLane[v], h)
	}
	slices.Sort(extra)
	var out []Group
	for _, l := range r.Spec.Lanes {
		out = append(out, Group{Value: l.Name, Hits: byRank(byLane[l.Name])})
	}
	for _, v := range extra {
		out = append(out, Group{Value: v, Hits: byRank(byLane[v])})
	}
	if hs := byLane[""]; len(hs) > 0 {
		out = append(out, Group{Value: "", Hits: byRank(hs)})
	}
	return out
}

// byRank orders the cards of a column as fleet.Order does.
func byRank(hs []index.QueryHit) []index.QueryHit {
	slices.SortStableFunc(hs, func(a, b index.QueryHit) int {
		return fleet.Order(fleet.Task{Path: a.Path, Rank: rankOf(a)}, fleet.Task{Path: b.Path, Rank: rankOf(b)})
	})
	return hs
}

func rankOf(h index.QueryHit) float64 {
	if vs := h.Fields[fleet.KeyRank]; len(vs) > 0 {
		if f, err := strconv.ParseFloat(vs[0], 64); err == nil && f > 0 {
			return f
		}
	}
	return 0
}
