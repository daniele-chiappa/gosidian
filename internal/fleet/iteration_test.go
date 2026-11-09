package fleet

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestReadIteration(t *testing.T) {
	src := "---\ntitle: Iterazione 1\ntype: iteration\nboard: \"[[fitness/board]]\"\ngoal: Il primo mese\nstart: 2026-11-09\nend: 2026-11-22\n---\n\n# Iterazione 1\n"
	if !IsIteration("type: iteration\n") || IsIteration("type: task\n") {
		t.Fatal("IsIteration")
	}
	it := ReadIteration("fitness/iterations/1.md", []byte(src))
	want := Iteration{Path: "fitness/iterations/1.md", Title: "Iterazione 1", Board: "fitness/board.md", Goal: "Il primo mese", Start: "2026-11-09", End: "2026-11-22"}
	if it != want {
		t.Fatalf("read %+v, want %+v", it, want)
	}
	if p := it.Problems(); len(p) != 0 {
		t.Fatalf("problems: %v", p)
	}
	for name, bad := range map[string]Iteration{
		"no board":   {Start: "2026-11-09", End: "2026-11-22"},
		"no end":     {Board: "fitness/board.md", Start: "2026-11-09"},
		"a bad day":  {Board: "fitness/board.md", Start: "2026-11-09", End: "22/11/2026"},
		"end before": {Board: "fitness/board.md", Start: "2026-11-09", End: "2026-11-08"},
	} {
		if len(bad.Problems()) == 0 {
			t.Errorf("%s: no problem", name)
		}
	}
}

func TestIterationSpanDueCurrent(t *testing.T) {
	rome, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		t.Skip("no zoneinfo:", err)
	}
	it := Iteration{Path: "fitness/iterations/1.md", Board: "fitness/board.md", Start: "2026-11-09", End: "2026-11-22"}
	from, to, ok := it.Span(rome)
	if !ok || from != time.Date(2026, 11, 9, 0, 0, 0, 0, rome) || to != time.Date(2026, 11, 23, 0, 0, 0, 0, rome) {
		t.Fatalf("span: %v %v %v", from, to, ok)
	}
	last := time.Date(2026, 11, 22, 23, 59, 0, 0, rome)
	if it.Due(last) || !it.Due(time.Date(2026, 11, 23, 0, 0, 0, 0, rome)) {
		t.Fatal("due on its last day, or not after it")
	}
	reviewed := it
	reviewed.Reviewed = "2026-11-23T00:05:00+01:00"
	if reviewed.Due(time.Date(2026, 11, 24, 0, 0, 0, 0, rome)) {
		t.Fatal("a reviewed iteration is due again")
	}
	next := Iteration{Path: "fitness/iterations/2.md", Board: "fitness/board.md", Start: "2026-11-23", End: "2026-12-06"}
	other := Iteration{Path: "altro/iterations/1.md", Board: "altro/board.md", Start: "2026-11-01", End: "2026-11-30"}
	its := []Iteration{next, other, it}
	if c, ok := Current(its, "fitness/board.md", last); !ok || c.Path != it.Path {
		t.Fatalf("current on the 22nd: %+v %v", c, ok)
	}
	if c, ok := Current(its, "fitness/board.md", time.Date(2026, 11, 23, 8, 0, 0, 0, rome)); !ok || c.Path != next.Path {
		t.Fatalf("current on the 23rd: %+v %v", c, ok)
	}
	if _, ok := Current(its, "fitness/board.md", time.Date(2026, 11, 8, 8, 0, 0, 0, rome)); ok {
		t.Fatal("an iteration before its start")
	}
	if len(Overlapping(its)) != 0 {
		t.Fatalf("back to back overlap: %v", Overlapping(its))
	}
	clash := Iteration{Path: "fitness/iterations/x.md", Board: "fitness/board.md", Start: "2026-11-22", End: "2026-11-24"}
	if o := Overlapping(append(its, clash)); len(o) != 2 {
		t.Fatalf("overlaps: %v", o)
	}
}

func TestIterationInfoAndUnfinished(t *testing.T) {
	b := board(t)
	it := Iteration{Path: "fitness/iterations/1.md", Board: b.Path, Start: "2026-11-09", End: "2026-11-22"}
	cards := []Task{
		{Path: "fitness/tasks/a.md", Board: b.Path, Status: Completed, Iteration: it.Path},
		{Path: "fitness/tasks/b.md", Board: b.Path, Status: Working, Iteration: it.Path},
		{Path: "fitness/tasks/c.md", Board: b.Path, Status: Canceled, Iteration: it.Path},
		{Path: "fitness/tasks/d.md", Board: b.Path, Status: Backlog, Iteration: it.Path},
		{Path: "fitness/tasks/e.md", Board: b.Path, Status: Working},
	}
	info := b.Info(it, cards, time.Date(2026, 11, 20, 15, 0, 0, 0, time.UTC))
	if info.Cards.Total != 4 || info.Cards.Done != 1 || info.DaysLeft != 3 {
		t.Fatalf("info: %+v", info)
	}
	if info := b.Info(it, cards, time.Date(2026, 11, 22, 23, 0, 0, 0, time.UTC)); info.DaysLeft != 1 {
		t.Fatalf("the last day: %d left", info.DaysLeft)
	}
	var left []string
	for _, c := range it.Unfinished(cards) {
		left = append(left, c.Path)
	}
	if strings.Join(left, ",") != "fitness/tasks/b.md,fitness/tasks/d.md" {
		t.Fatalf("unfinished: %v", left)
	}
}

func TestReview(t *testing.T) {
	b := board(t)
	it := Iteration{Path: "fitness/iterations/1.md", Board: b.Path, Goal: "Il primo mese", Start: "2026-11-09", End: "2026-11-22"}
	day := func(d int) string { return time.Date(2026, 11, d, 12, 0, 0, 0, time.Local).Format(time.RFC3339) }
	cards := []Task{
		{Path: "fitness/tasks/a.md", Title: "A", Board: b.Path, Status: Completed, Iteration: it.Path, Started: day(9), Finished: day(10)},
		{Path: "fitness/tasks/b.md", Title: "B", Board: b.Path, Status: Completed, Iteration: it.Path, Started: day(9), Finished: day(12)},
		{Path: "fitness/tasks/c.md", Title: "C", Board: b.Path, Status: Completed, Iteration: it.Path, Started: day(10), Finished: day(18)},
		{Path: "fitness/tasks/d.md", Title: "D", Board: b.Path, Status: Working, Column: "Sviluppo", Iteration: it.Path, ColumnSince: day(15)},
		{Path: "fitness/tasks/e.md", Title: "E", Board: b.Path, Status: Failed, Iteration: it.Path},
		{Path: "fitness/tasks/f.md", Title: "F", Board: b.Path, Status: Completed, Started: day(9), Finished: day(10)},
	}
	words := func(key string, args map[string]string) string {
		s := strings.TrimPrefix(key, "board.review.")
		for k, v := range args {
			s += fmt.Sprintf(" %s=%s", k, v)
		}
		return s
	}
	decimal := func(f float64) string { return strings.Replace(fmt.Sprintf("%.1f", f), ".", ",", 1) }
	got := it.Review(b, cards, time.Date(2026, 11, 23, 0, 5, 0, 0, time.Local), words, decimal)
	for _, want := range []string{
		"## title\n\nclosed_on date=2026-11-23 goal: Il primo mese.\n",
		"**done** (3)\n\n- [[fitness/tasks/a|A]]\n- [[fitness/tasks/b|B]]\n- [[fitness/tasks/c|C]]\n",
		"**deferred** (1)\n\n- [[fitness/tasks/d|D]] · Sviluppo\n",
		"**dropped** (1)\n\n- [[fitness/tasks/e|E]]\n",
		"- **closed_week**: 2 · 1\n",
		"- **stale**: 1\n",
		"## retro\n\n- went_well\n- change\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the review has no %q:\n%s", want, got)
		}
	}
	// 1, 3 and 8 days: p50 3, p85 6.5, in the instance's decimals.
	if !strings.Contains(got, "cycle_value") || !strings.Contains(got, "p50=3,0") || !strings.Contains(got, "p85=6,5") || !strings.Contains(got, "n=3") {
		t.Errorf("the cycle time:\n%s", got)
	}
	if strings.Contains(got, "tasks/f") {
		t.Errorf("a card of no iteration in the review:\n%s", got)
	}
	// None done: the lists say so, the cycle has too few cards.
	got = it.Review(b, nil, time.Date(2026, 11, 23, 0, 5, 0, 0, time.Local), words, decimal)
	if !strings.Contains(got, "**done** (0)\n\n- none\n") || !strings.Contains(got, "cycle_few n=0") || strings.Contains(got, "dropped") || strings.Contains(got, "closed_week") {
		t.Errorf("an empty review:\n%s", got)
	}
}

func TestWaitingRetro(t *testing.T) {
	its := []Iteration{
		{Path: "p/iterations/1.md", Board: "p/board.md", End: "2026-11-08", Reviewed: "2026-11-09T00:05:00Z", Retro: RetroPending},
		{Path: "p/iterations/2.md", Board: "p/board.md", End: "2026-11-22", Reviewed: "2026-11-23T00:05:00Z", Retro: RetroPending},
		{Path: "p/iterations/3.md", Board: "p/board.md", End: "2026-12-06", Reviewed: "2026-12-07T00:05:00Z", Retro: RetroDone},
		{Path: "p/iterations/4.md", Board: "p/board.md", End: "2026-12-20"},
		{Path: "p/iterations/x.md", Board: "p/other.md", End: "2026-11-22", Reviewed: "2026-11-23T00:05:00Z", Retro: RetroPending},
	}
	got := WaitingRetro(its, "p/board.md")
	if len(got) != 2 || got[0].Path != "p/iterations/2.md" || got[1].Path != "p/iterations/1.md" {
		t.Fatalf("pending: %+v", got)
	}
}
