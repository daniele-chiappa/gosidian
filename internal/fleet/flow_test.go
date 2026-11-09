package fleet

import (
	"strings"
	"testing"
	"time"
)

func TestParseBoardStale(t *testing.T) {
	ok := "columns:\n  - {name: Pronto, status: submitted}\n  - {name: In corso, status: working}\n"
	for src, want := range map[string]int{ok: DefaultStale, ok + "stale: 0\n": 0, ok + "stale: 7\n": 7, ok + "stale: 365\n": 365} {
		b, problems := ParseBoard(src, "fitness/board.md")
		if len(problems) > 0 || b.Stale != want {
			t.Errorf("%q: stale %d, %v; want %d", src, b.Stale, problems, want)
		}
	}
}

func TestDates(t *testing.T) {
	now := time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC)
	at := now.Format(time.RFC3339)
	cases := []struct {
		name   string
		card   Task
		status string
		set    map[string]any
		unset  []string
	}{
		{"first time in working", Task{Status: Submitted}, Working, map[string]any{KeyStarted: at}, nil},
		{"a card at work from before has no start", Task{Status: Review}, Working, map[string]any{}, nil},
		{"working again keeps started", Task{Status: Review, Started: "2026-11-01T09:00:00Z"}, Working, map[string]any{}, nil},
		{"to review writes nothing", Task{Status: Working, Started: "2026-11-01T09:00:00Z"}, Review, map[string]any{}, nil},
		{"closed", Task{Status: Review, Started: "2026-11-01T09:00:00Z"}, Completed, map[string]any{KeyFinished: at}, nil},
		{"closed straight from submitted", Task{Status: Submitted}, Canceled, map[string]any{KeyFinished: at}, nil},
		{"from closed to closed keeps finished", Task{Status: Failed, Finished: "2026-11-02T09:00:00Z"}, Canceled, map[string]any{}, nil},
		{"closed again after a lost finished", Task{Status: Failed}, Completed, map[string]any{KeyFinished: at}, nil},
		{"opened again", Task{Status: Completed, Started: "2026-11-01T09:00:00Z", Finished: "2026-11-02T09:00:00Z"}, Working, map[string]any{}, []string{KeyFinished}},
		{"a finished by hand on an open card", Task{Status: Working, Finished: "2026-11-02"}, Review, map[string]any{}, []string{KeyFinished}},
	}
	for _, c := range cases {
		set, unset := Dates(c.card, c.status, now)
		if len(set) != len(c.set) || strings.Join(unset, ",") != strings.Join(c.unset, ",") {
			t.Errorf("%s: set %v unset %v, want %v %v", c.name, set, unset, c.set, c.unset)
			continue
		}
		for k, v := range c.set {
			if set[k] != v {
				t.Errorf("%s: %s = %v, want %v", c.name, k, set[k], v)
			}
		}
	}
}

func TestCycleOf(t *testing.T) {
	if c := CycleOf([]float64{1, 2}); c.N != 2 || c.P50 != nil || c.P85 != nil {
		t.Fatalf("two cards say a cycle time: %+v", c)
	}
	c := CycleOf([]float64{4, 1, 2, 3, 10})
	if c.N != 5 || *c.P50 != 3 || *c.P85 != 6.4 {
		t.Fatalf("cycle: n %d p50 %v p85 %v", c.N, *c.P50, *c.P85)
	}
	if c := CycleOf([]float64{1.04, 1.04, 1.04}); *c.P50 != 1 || *c.P85 != 1 {
		t.Fatalf("one decimal: %v %v", *c.P50, *c.P85)
	}
}

func TestWhen(t *testing.T) {
	if at, ok := When("2026-11-09T10:00:00+01:00"); !ok || !at.Equal(time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("RFC 3339: %v %v", at, ok)
	}
	if at, ok := When("2026-11-09"); !ok || at.Hour() != 0 || at.Location() != time.Local {
		t.Fatalf("a day: %v %v", at, ok)
	}
	for _, s := range []string{"", "ieri", "09/11/2026"} {
		if _, ok := When(s); ok {
			t.Errorf("%q reads", s)
		}
	}
}

func TestWeekStarts(t *testing.T) {
	// Sunday 15 November 2026: its week starts on Monday the 9th.
	now := time.Date(2026, 11, 15, 23, 0, 0, 0, time.UTC)
	starts := weekStarts(now, 4)
	var days []string
	for _, s := range starts {
		days = append(days, s.Format(time.DateOnly))
	}
	if strings.Join(days, " ") != "2026-10-19 2026-10-26 2026-11-02 2026-11-09 2026-11-16" {
		t.Fatalf("weeks: %v", days)
	}
	// A Monday is the first day of its own week.
	if s := weekStarts(time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC), 1); s[0].Day() != 9 || s[1].Day() != 16 {
		t.Fatalf("a Monday: %v", s)
	}
	if i := weekOf(starts, time.Date(2026, 11, 8, 23, 59, 0, 0, time.UTC)); i != 2 {
		t.Fatalf("Sunday the 8th in week %d", i)
	}
	if i := weekOf(starts, time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)); i != -1 {
		t.Fatalf("before the first week: %d", i)
	}
}

func TestFlow(t *testing.T) {
	b := board(t)
	now := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	day := func(d, h int) string { return time.Date(2026, 11, d, h, 0, 0, 0, time.UTC).Format(time.RFC3339) }
	done := func(p, started, finished string) Task {
		return Task{Path: p, Board: b.Path, Status: Completed, Started: started, Finished: finished}
	}
	cards := []Task{
		done("fitness/tasks/a.md", day(2, 12), day(3, 12)),  // 1 day, week of 2 Nov
		done("fitness/tasks/b.md", day(2, 12), day(4, 12)),  // 2 days
		done("fitness/tasks/c.md", day(9, 12), day(13, 12)), // 4 days, this week
		done("fitness/tasks/d.md", "", day(14, 12)),         // never started: counted closed, no cycle
		// Closed 40 days ago: outside the cycle window and the weeks.
		{Path: "fitness/tasks/e.md", Board: b.Path, Status: Completed, Started: "2026-10-01T12:00:00Z", Finished: "2026-10-06T12:00:00Z"},
		// Another board's.
		{Path: "fitness/tasks/f.md", Board: "fitness/other.md", Status: Completed, Started: day(9, 12), Finished: day(10, 12)},
		// Stale: in Sviluppo for 5 days, in Verifica for 4, and one in Pronto, which does not count.
		{Path: "fitness/tasks/g.md", Title: "G", Board: b.Path, Status: Working, Column: "Sviluppo", ColumnSince: day(10, 11)},
		{Path: "fitness/tasks/h.md", Title: "H", Board: b.Path, Status: Review, ColumnSince: day(11, 11)},
		{Path: "fitness/tasks/i.md", Title: "I", Board: b.Path, Status: Submitted, ColumnSince: day(1, 11)},
		{Path: "fitness/tasks/j.md", Title: "J", Board: b.Path, Status: Working, ColumnSince: day(13, 11)},
	}
	f := b.Flow(cards, now)
	if f.Cycle.N != 3 || *f.Cycle.P50 != 2 || *f.Cycle.P85 != 3.4 {
		t.Fatalf("cycle: %+v %v %v", f.Cycle, *f.Cycle.P50, *f.Cycle.P85)
	}
	if got := f.Closed; len(got) != 4 || got[0] != 0 || got[1] != 0 || got[2] != 2 || got[3] != 2 {
		t.Fatalf("closed per week: %v", got)
	}
	if len(f.Stale) != 2 || f.Stale[0].Path != "fitness/tasks/g.md" || f.Stale[0].Days != 5 || f.Stale[0].Column != "Sviluppo" ||
		f.Stale[1].Path != "fitness/tasks/h.md" || f.Stale[1].Days != 4 || f.Stale[1].Column != "Verifica" {
		t.Fatalf("stale: %+v", f.Stale)
	}
	b.Stale = 0
	if f := b.Flow(cards, now); f.Stale != nil {
		t.Fatalf("stale 0 still shows %+v", f.Stale)
	}
}
