package fleet

// The flow of a board (IMP-216 phase 2): how long its cards take from the
// first column of working to done, how many close a week, and which wait
// too long in a column. The server computes it from the cards at each
// read: a board has few, and nothing is kept.

import (
	"math"
	"slices"
	"time"
)

// DefaultStale is the days a card waits in a column of working,
// input-required or review before its board shows it as stale.
const DefaultStale = 3

const (
	// cycleWindow: the cards completed in it make the cycle time, and
	// minCycle are the fewest that say anything.
	cycleWindow = 28 * 24 * time.Hour
	minCycle    = 3
	// flowWeeks are the weeks the closed cards are counted in.
	flowWeeks = 4
)

// Flow is a board's flow at a time.
type Flow struct {
	Cycle Cycle `json:"cycle_days"`
	// Closed are the cards completed in each of the last flowWeeks weeks
	// (Monday to Sunday, in the zone of the time), the oldest first.
	Closed []int       `json:"closed_per_week"`
	Stale  []StaleCard `json:"stale,omitempty"`
}

// Cycle is the cycle time in days of N cards: the median and the 85th
// percentile, with one decimal; nil with fewer than minCycle.
type Cycle struct {
	P50 *float64 `json:"p50,omitempty"`
	P85 *float64 `json:"p85,omitempty"`
	N   int      `json:"n"`
}

// StaleCard is a card that waits in its column longer than its board's
// Stale: Days whole days.
type StaleCard struct {
	Path   string `json:"path"`
	Title  string `json:"title"`
	Column string `json:"column"`
	Days   int    `json:"days"`
}

// Flow computes the flow of b's cards among cards at now.
func (b Board) Flow(cards []Task, now time.Time) Flow {
	f := Flow{Closed: make([]int, flowWeeks), Stale: b.StaleCards(cards, now)}
	weeks := weekStarts(now, flowWeeks)
	var days []float64
	for _, t := range cards {
		if t.Board != b.Path || t.Status != Completed {
			continue
		}
		end, ok := When(t.Finished)
		if !ok {
			continue
		}
		if i := weekOf(weeks, end); i >= 0 {
			f.Closed[i]++
		}
		if d, ok := cycleDays(t); ok && now.Sub(end) <= cycleWindow {
			days = append(days, d)
		}
	}
	f.Cycle = CycleOf(days)
	return f
}

// StaleCards are b's cards among cards that wait in a column of working,
// input-required or review longer than b's Stale days at now, the longest
// first; none when Stale is 0.
func (b Board) StaleCards(cards []Task, now time.Time) []StaleCard {
	if b.Stale <= 0 {
		return nil
	}
	limit := time.Duration(b.Stale) * 24 * time.Hour
	var out []StaleCard
	for _, t := range cards {
		if t.Board != b.Path || (t.Status != Working && t.Status != InputRequired && t.Status != Review) {
			continue
		}
		since, ok := When(t.ColumnSince)
		if d := now.Sub(since); ok && d > limit {
			out = append(out, StaleCard{Path: t.Path, Title: t.Title, Column: b.ColumnOf(t), Days: int(d.Hours() / 24)})
		}
	}
	slices.SortStableFunc(out, func(a, c StaleCard) int { return c.Days - a.Days })
	return out
}

// Dates are what a move to status writes on card c besides its column:
// started the first time it enters working, finished when it closes,
// taken away when it opens again. A card already at work without started
// (one of before phase 2) gets none: its cycle time is not known.
func Dates(c Task, status string, now time.Time) (map[string]any, []string) {
	set := map[string]any{}
	var unset []string
	at := now.Format(time.RFC3339)
	if status == Working && c.Started == "" && c.Status != Working && c.Status != InputRequired && c.Status != Review {
		set[KeyStarted] = at
	}
	switch {
	case Ended(status) && (c.Finished == "" || !Ended(c.Status)):
		set[KeyFinished] = at
	case !Ended(status) && c.Finished != "":
		unset = append(unset, KeyFinished)
	}
	return set, unset
}

// CycleOf is the cycle time of the cards that took days.
func CycleOf(days []float64) Cycle {
	c := Cycle{N: len(days)}
	if len(days) < minCycle {
		return c
	}
	slices.Sort(days)
	p50, p85 := percentile(days, 0.5), percentile(days, 0.85)
	c.P50, c.P85 = &p50, &p85
	return c
}

// percentile of the sorted values, linearly between the two nearest, with
// one decimal.
func percentile(sorted []float64, p float64) float64 {
	pos := p * float64(len(sorted)-1)
	lo := int(pos)
	v := sorted[lo]
	if lo+1 < len(sorted) {
		v += (pos - float64(lo)) * (sorted[lo+1] - sorted[lo])
	}
	return math.Round(v*10) / 10
}

// cycleDays is how long card t took, from started to finished, in days.
func cycleDays(t Task) (float64, bool) {
	start, ok1 := When(t.Started)
	end, ok2 := When(t.Finished)
	if !ok1 || !ok2 || end.Before(start) {
		return 0, false
	}
	return end.Sub(start).Hours() / 24, true
}

// When reads a time the server writes on a card (RFC 3339), or a day
// written by hand (2006-01-02, at its midnight in the local zone).
func When(s string) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	if t, err := time.ParseInLocation(time.DateOnly, s, time.Local); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// weekStarts are the Mondays at midnight of the last n weeks to now's, in
// now's zone, the oldest first, and the Monday after.
func weekStarts(now time.Time, n int) []time.Time {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	monday := day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
	out := make([]time.Time, n+1)
	for i := range out {
		out[i] = monday.AddDate(0, 0, 7*(i-n+1))
	}
	return out
}

// weekOf is the week of starts (weekStarts) that holds t, or -1.
func weekOf(starts []time.Time, t time.Time) int {
	for i := 0; i+1 < len(starts); i++ {
		if !t.Before(starts[i]) && t.Before(starts[i+1]) {
			return i
		}
	}
	return -1
}
