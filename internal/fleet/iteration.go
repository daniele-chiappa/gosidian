package fleet

// An iteration of a board (IMP-216 phase 2) is a note type: iteration with
// its board, a goal, a first and a last day. Its cards link it; the board
// shows the one in progress. When its last day is over the server writes
// its review in the note, takes the iteration away from the cards it did
// not finish, which stay in their columns, and waits for the
// retrospective, which a person or an agent writes.

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/parser"
)

// IterationType is the type of an iteration's note.
const IterationType = "iteration"

// The frontmatter keys of an iteration the server writes, and the states
// of its retrospective.
const (
	KeyReviewed  = "reviewed"
	KeyRetro     = "retro"
	RetroPending = "pending"
	RetroDone    = "done"
)

// Iteration is an iteration as its note's frontmatter says it.
type Iteration struct {
	Path  string `json:"path"`
	Title string `json:"title,omitempty"`
	Board string `json:"board"`
	Goal  string `json:"goal,omitempty"`
	// Start and End are its first and last day (2006-01-02).
	Start    string `json:"start"`
	End      string `json:"end"`
	Reviewed string `json:"reviewed,omitempty"`
	Retro    string `json:"retro,omitempty"`
}

// ReadIteration reads the iteration of the note at p from its content.
func ReadIteration(p string, content []byte) Iteration {
	project, _, _ := strings.Cut(p, "/")
	fm := parser.ParseFrontmatterFields(parser.FrontmatterRawForPath(p, content))
	str := func(k string) string {
		s, _ := fm[k].(string)
		return strings.TrimSpace(s)
	}
	return Iteration{Path: p, Title: cmp.Or(str("title"), strings.TrimSuffix(path.Base(p), ".md")), Board: LinkPath(str("board"), project),
		Goal: str("goal"), Start: str("start"), End: str("end"), Reviewed: str(KeyReviewed), Retro: str(KeyRetro)}
}

// IsIteration says whether a note's raw frontmatter is an iteration's.
func IsIteration(raw string) bool {
	return strings.TrimSpace(fmt.Sprint(parser.ParseFrontmatterFields(raw)["type"])) == IterationType
}

// Span is the time of it in loc: from its first day's midnight to the
// midnight after its last. ok is false when a day does not read or the
// last is before the first.
func (it Iteration) Span(loc *time.Location) (from, to time.Time, ok bool) {
	from, err1 := time.ParseInLocation(time.DateOnly, it.Start, loc)
	last, err2 := time.ParseInLocation(time.DateOnly, it.End, loc)
	if err1 != nil || err2 != nil || last.Before(from) {
		return time.Time{}, time.Time{}, false
	}
	return from, last.AddDate(0, 0, 1), true
}

// Problems are what keeps it from working, for the lint.
func (it Iteration) Problems() []string {
	var out []string
	if it.Board == "" {
		out = append(out, "an iteration has its board ([[p/board]])")
	}
	_, err1 := time.Parse(time.DateOnly, it.Start)
	_, err2 := time.Parse(time.DateOnly, it.End)
	switch {
	case err1 != nil || err2 != nil:
		out = append(out, "an iteration has its start and end, days written 2006-01-02")
	case it.End < it.Start:
		out = append(out, fmt.Sprintf("its end %s is before its start %s", it.End, it.Start))
	}
	return out
}

// Due says whether its last day is over at now and its review is not
// written.
func (it Iteration) Due(now time.Time) bool {
	_, to, ok := it.Span(now.Location())
	return ok && it.Reviewed == "" && !now.Before(to)
}

// Current is the iteration of board in progress at now among its, the
// first to start when they overlap (the lint reports it).
func Current(its []Iteration, board string, now time.Time) (Iteration, bool) {
	var found []Iteration
	for _, it := range its {
		from, to, ok := it.Span(now.Location())
		if ok && it.Board == board && it.Reviewed == "" && !now.Before(from) && now.Before(to) {
			found = append(found, it)
		}
	}
	if len(found) == 0 {
		return Iteration{}, false
	}
	slices.SortFunc(found, func(a, b Iteration) int { return strings.Compare(a.Start+a.Path, b.Start+b.Path) })
	return found[0], true
}

// Overlapping are the pairs of iterations of the same board whose days
// meet.
func Overlapping(its []Iteration) [][2]Iteration {
	var out [][2]Iteration
	for i, a := range its {
		af, at, ok := a.Span(time.UTC)
		if !ok {
			continue
		}
		for _, b := range its[i+1:] {
			bf, bt, ok := b.Span(time.UTC)
			if ok && a.Board == b.Board && af.Before(bt) && bf.Before(at) {
				out = append(out, [2]Iteration{a, b})
			}
		}
	}
	return out
}

// IterationInfo is an iteration as its board shows it: the days left,
// counting today, and its cards done of all.
type IterationInfo struct {
	Iteration
	DaysLeft int `json:"days_left"`
	Cards    struct {
		Done  int `json:"done"`
		Total int `json:"total"`
	} `json:"cards"`
}

// Info is it as board b shows it at now, with its cards among cards.
func (b Board) Info(it Iteration, cards []Task, now time.Time) IterationInfo {
	info := IterationInfo{Iteration: it}
	if _, to, ok := it.Span(now.Location()); ok {
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		info.DaysLeft = max(0, int(to.Sub(today).Hours()/24+0.5))
	}
	for _, t := range cards {
		if t.Board == b.Path && t.Iteration == it.Path {
			info.Cards.Total++
			if t.Status == Completed {
				info.Cards.Done++
			}
		}
	}
	return info
}

// WaitingRetro are the iterations of board among its whose retrospective
// waits, the last to end first.
func WaitingRetro(its []Iteration, board string) []Iteration {
	var out []Iteration
	for _, it := range its {
		if it.Board == board && it.Reviewed != "" && it.Retro == RetroPending {
			out = append(out, it)
		}
	}
	slices.SortFunc(out, func(a, b Iteration) int { return strings.Compare(b.End+b.Path, a.End+a.Path) })
	return out
}

// Words gives the words of a review in the instance's language by their
// key in the catalogues (board.review.*), with {name} filled in.
type Words func(key string, args map[string]string) string

// Review is its review, closed at now with b's cards among cards:
// the cards done, those left (which keep their column), those failed or
// canceled, the cycle time and the cards closed in each of its weeks, the
// stale ones; then the questions of its retrospective. decimal writes a
// number with one decimal in the instance's language.
func (it Iteration) Review(b Board, cards []Task, now time.Time, w Words, decimal func(float64) string) string {
	var done, left, dropped []Task
	var days []float64
	from, _, _ := it.Span(now.Location())
	var closed []int
	for _, t := range cards {
		if t.Board != b.Path || t.Iteration != it.Path {
			continue
		}
		switch t.Status {
		case Completed:
			done = append(done, t)
			if d, ok := cycleDays(t); ok {
				days = append(days, d)
			}
			if end, ok := When(t.Finished); ok && !end.Before(from) {
				week := int(end.Sub(from).Hours() / (24 * 7))
				for len(closed) <= week {
					closed = append(closed, 0)
				}
				closed[week]++
			}
		case Failed, Canceled:
			dropped = append(dropped, t)
		default:
			left = append(left, t)
		}
	}
	var s strings.Builder
	fmt.Fprintf(&s, "## %s\n\n%s", w("board.review.title", nil), w("board.review.closed_on", map[string]string{"date": now.Format(time.DateOnly)}))
	if it.Goal != "" {
		fmt.Fprintf(&s, " %s: %s.", w("board.review.goal", nil), it.Goal)
	}
	s.WriteString("\n")
	list := func(key string, ts []Task, column bool) {
		fmt.Fprintf(&s, "\n**%s** (%d)\n\n", w(key, nil), len(ts))
		if len(ts) == 0 {
			fmt.Fprintf(&s, "- %s\n", w("board.review.none", nil))
		}
		for _, t := range ts {
			fmt.Fprintf(&s, "- [[%s|%s]]", strings.TrimSuffix(t.Path, ".md"), t.Title)
			if column && b.ColumnOf(t) != "" {
				fmt.Fprintf(&s, " · %s", b.ColumnOf(t))
			}
			s.WriteString("\n")
		}
	}
	list("board.review.done", done, false)
	list("board.review.deferred", left, true)
	if len(dropped) > 0 {
		list("board.review.dropped", dropped, false)
	}
	c := CycleOf(days)
	value := w("board.review.cycle_few", map[string]string{"n": fmt.Sprint(c.N)})
	if c.P50 != nil {
		value = w("board.review.cycle_value", map[string]string{"p50": decimal(*c.P50), "p85": decimal(*c.P85), "n": fmt.Sprint(c.N)})
	}
	fmt.Fprintf(&s, "\n- **%s**: %s\n", w("board.review.cycle", nil), value)
	if len(closed) > 0 {
		weeks := make([]string, len(closed))
		for i, n := range closed {
			weeks[i] = fmt.Sprint(n)
		}
		fmt.Fprintf(&s, "- **%s**: %s\n", w("board.review.closed_week", nil), strings.Join(weeks, " · "))
	}
	fmt.Fprintf(&s, "- **%s**: %d\n", w("board.review.stale", nil), len(b.StaleCards(left, now)))
	fmt.Fprintf(&s, "\n## %s\n\n- %s\n- %s\n", w("board.review.retro", nil), w("board.review.went_well", nil), w("board.review.change", nil))
	return s.String()
}

// Unfinished are the cards of it among cards that are not closed: the
// review takes the iteration away from them.
func (it Iteration) Unfinished(cards []Task) []Task {
	var out []Task
	for _, t := range cards {
		if t.Iteration == it.Path && !Ended(t.Status) {
			out = append(out, t)
		}
	}
	return out
}
