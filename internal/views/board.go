package views

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

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

// boardRead are the fields of a card its board reads without showing: its
// rank, and what its flow needs (IMP-216 phase 2).
var boardRead = []string{fleet.KeyRank, fleet.KeyStatus, fleet.KeyColumnSince, fleet.KeyStarted, fleet.KeyFinished, fleet.KeyIteration}

// BoardState is what a board shows above its columns (IMP-216 phase 2):
// its flow, the days a card waits before it is stale, its iteration in
// progress and the iterations whose retrospective waits.
type BoardState struct {
	Flow      fleet.Flow           `json:"flow"`
	StaleDays int                  `json:"stale_days"`
	Iteration *fleet.IterationInfo `json:"iteration,omitempty"`
	Retro     []fleet.Iteration    `json:"retro,omitempty"`
}

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
	s.Lanes, s.Read = b.Columns, boardRead
	r, err := computeSpec(s, c, q)
	if err != nil {
		return nil, err
	}
	now := c.Today
	if now.IsZero() {
		now = time.Now()
	}
	cards := make([]fleet.Task, 0, len(r.Hits))
	for _, h := range r.Hits {
		cards = append(cards, cardOf(h, note, project))
	}
	r.Board = &BoardState{Flow: b.Flow(cards, now), StaleDays: b.Stale}
	if its, err := iterationsOf(q, project); err == nil {
		if it, ok := fleet.Current(its, note, now); ok {
			info := b.Info(it, cards, now)
			r.Board.Iteration = &info
		}
		r.Board.Retro = fleet.WaitingRetro(its, note)
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

// field is the first value of h's field k, trimmed.
func field(h index.QueryHit, k string) string {
	if vs := h.Fields[k]; len(vs) > 0 {
		return strings.TrimSpace(vs[0])
	}
	return ""
}

// cardOf is the card of board, of project, that h is, as far as its flow
// needs.
func cardOf(h index.QueryHit, board, project string) fleet.Task {
	return fleet.Task{Path: h.Path, Title: rowTitle(h), Board: board, Status: field(h, fleet.KeyStatus), Column: field(h, fleet.KeyColumn),
		ColumnSince: field(h, fleet.KeyColumnSince), Started: field(h, fleet.KeyStarted), Finished: field(h, fleet.KeyFinished),
		Iteration: fleet.LinkPath(field(h, fleet.KeyIteration), project)}
}

// iterationsOf are the iterations of project the reader sees.
func iterationsOf(q QueryFunc, project string) ([]fleet.Iteration, error) {
	hits, _, err := q(index.QueryOptions{Projects: []string{project},
		Where:  []index.FieldCond{{Field: "type", Op: index.OpEq, Values: []string{fleet.IterationType}}},
		Fields: []string{"board", "goal", "start", "end", fleet.KeyReviewed, fleet.KeyRetro}, Sort: "path", Limit: index.QueryMaxLimit})
	if err != nil {
		return nil, err
	}
	out := make([]fleet.Iteration, 0, len(hits))
	for _, h := range hits {
		out = append(out, fleet.Iteration{Path: h.Path, Title: rowTitle(h), Board: fleet.LinkPath(field(h, "board"), project), Goal: field(h, "goal"),
			Start: field(h, "start"), End: field(h, "end"), Reviewed: field(h, fleet.KeyReviewed), Retro: field(h, fleet.KeyRetro)})
	}
	return out, nil
}

// markdown is the board's state as the lines an agent reads above its
// columns.
func (st *BoardState) markdown() string {
	var b strings.Builder
	f := st.Flow
	cycle := fmt.Sprintf("too few cards (%d)", f.Cycle.N)
	if f.Cycle.P50 != nil {
		cycle = fmt.Sprintf("%s days median, %s at the 85th percentile (%d cards)", num(*f.Cycle.P50), num(*f.Cycle.P85), f.Cycle.N)
	}
	weeks := make([]string, len(f.Closed))
	for i, n := range f.Closed {
		weeks[i] = strconv.Itoa(n)
	}
	fmt.Fprintf(&b, "_Flow: cycle time %s; closed per week %s; stale %d", cycle, strings.Join(weeks, " · "), len(f.Stale))
	if st.StaleDays > 0 {
		fmt.Fprintf(&b, " (over %d days)", st.StaleDays)
	}
	b.WriteString("._\n")
	if it := st.Iteration; it != nil {
		fmt.Fprintf(&b, "_Iteration %s", noteLink(it.Path, it.Title))
		if it.Goal != "" {
			fmt.Fprintf(&b, ": %s", tableSafe(it.Goal))
		}
		fmt.Fprintf(&b, ", %s to %s, %d days left, %d of %d cards done._\n", it.Start, it.End, it.DaysLeft, it.Cards.Done, it.Cards.Total)
	}
	for _, it := range st.Retro {
		fmt.Fprintf(&b, "_Retrospective to write: %s (retro: done once written)._\n", noteLink(it.Path, it.Title))
	}
	return b.String()
}

// noteLink is a wikilink to the note at p, named title.
func noteLink(p, title string) string {
	return "[[" + strings.TrimSuffix(p, ".md") + `\|` + tableSafe(strings.ReplaceAll(title, "`", "")) + "]]"
}

func num(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
