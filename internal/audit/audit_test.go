package audit

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLog_NilSafe(t *testing.T) {
	var l *Log
	if err := l.Write(Entry{Action: ActionCreate, Path: "x.md"}); err != nil {
		t.Errorf("nil write should be no-op: %v", err)
	}
	if rows, err := l.Tail(10); err != nil || rows != nil {
		t.Errorf("nil tail should return nil: %v %v", err, rows)
	}
}

func TestLog_AppendAndTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a.md", "b.md", "c.md", "d.md"} {
		if err := l.Write(Entry{Source: SourceHTTP, Action: ActionCreate, Path: p, Size: 10}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := l.Tail(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Path != "c.md" || rows[1].Path != "d.md" {
		t.Errorf("tail wrong: %+v", rows)
	}

	// Tail with n larger than total returns everything
	all, _ := l.Tail(100)
	if len(all) != 4 {
		t.Errorf("expected 4 entries, got %d", len(all))
	}
	if all[0].Source != SourceHTTP || all[0].Action != ActionCreate {
		t.Errorf("first row metadata wrong: %+v", all[0])
	}
}

func TestLog_PersistsAcrossOpens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	l1, _ := Open(path)
	_ = l1.Write(Entry{Action: ActionDelete, Path: "x.md"})

	l2, _ := Open(path)
	rows, _ := l2.Tail(10)
	if len(rows) != 1 || rows[0].Action != ActionDelete {
		t.Errorf("rows = %+v", rows)
	}
}

// --- TailFiltered tests ---

// seedFilterLog populates a fresh audit log with a deterministic mix of
// entries spanning multiple sources, actors, actions, and paths.
func seedFilterLog(t *testing.T) *Log {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-2 * time.Hour)
	entries := []Entry{
		{TS: base.Add(5 * time.Minute), Source: SourceHTTP, Actor: "alice", Action: ActionCreate, Path: "projA/x.md", Size: 10},
		{TS: base.Add(10 * time.Minute), Source: SourceMCP, Actor: "bob", Action: ActionUpdate, Path: "projA/y.md", Size: 20},
		{TS: base.Add(15 * time.Minute), Source: SourceMCP, Actor: "bob", Action: ActionAppend, Path: "projB/z.md", Size: 30},
		{TS: base.Add(20 * time.Minute), Source: SourceHTTP, Actor: "alice", Action: ActionDelete, Path: "projB/z.md"},
		{TS: base.Add(25 * time.Minute), Source: SourceMCP, Actor: "bob@abc123", Action: ActionCreate, Path: "projA/q.md", Size: 40},
	}
	for _, e := range entries {
		if err := l.Write(e); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

func TestTailFiltered_NoFiltersReturnsAll(t *testing.T) {
	l := seedFilterLog(t)
	rows, err := l.TailFiltered(TailOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Errorf("expected 5, got %d", len(rows))
	}
}

func TestTailFiltered_ByActor(t *testing.T) {
	l := seedFilterLog(t)
	rows, _ := l.TailFiltered(TailOpts{Actor: "alice"})
	if len(rows) != 2 {
		t.Errorf("expected 2 alice rows, got %d: %+v", len(rows), rows)
	}
	for _, r := range rows {
		if r.Actor != "alice" {
			t.Errorf("unexpected actor %q", r.Actor)
		}
	}
}

func TestTailFiltered_ByAction(t *testing.T) {
	l := seedFilterLog(t)
	rows, _ := l.TailFiltered(TailOpts{Action: ActionCreate})
	if len(rows) != 2 {
		t.Errorf("expected 2 create rows, got %d", len(rows))
	}
}

func TestTailFiltered_BySource(t *testing.T) {
	l := seedFilterLog(t)
	rows, _ := l.TailFiltered(TailOpts{Source: SourceMCP})
	if len(rows) != 3 {
		t.Errorf("expected 3 MCP rows, got %d", len(rows))
	}
}

func TestTailFiltered_ByPathPrefix(t *testing.T) {
	l := seedFilterLog(t)
	rows, _ := l.TailFiltered(TailOpts{PathPrefix: "projB/"})
	if len(rows) != 2 {
		t.Errorf("expected 2 projB rows, got %d", len(rows))
	}
}

func TestTailFiltered_BySince(t *testing.T) {
	l := seedFilterLog(t)
	// Seed uses base = now-2h with entries at base+{5,10,15,20,25}min, so
	// timestamps span roughly now-115min to now-95min. A "since=120min ago"
	// cutoff keeps all 5 entries; a tighter window keeps fewer. We verify
	// both directions to ensure the comparator is strict-after.
	all, _ := l.TailFiltered(TailOpts{Since: time.Now().Add(-120 * time.Minute)})
	if len(all) == 0 {
		t.Fatalf("expected some rows within 120min window, got 0")
	}
	// Negative assertion: every returned row must be strictly after the cutoff.
	cutoff := time.Now().Add(-120 * time.Minute)
	for _, r := range all {
		if !r.TS.After(cutoff) {
			t.Errorf("entry %+v passed filter but TS not after cutoff %v", r, cutoff)
		}
	}
	// Tight window: none of the seed entries is within the last minute.
	recent, _ := l.TailFiltered(TailOpts{Since: time.Now().Add(-time.Minute)})
	if len(recent) != 0 {
		t.Errorf("expected 0 rows within last minute, got %d", len(recent))
	}
}

func TestTailFiltered_LimitClamp(t *testing.T) {
	l := seedFilterLog(t)
	rows, _ := l.TailFiltered(TailOpts{Limit: 2})
	if len(rows) != 2 {
		t.Errorf("expected limit=2 to return 2 rows, got %d", len(rows))
	}
	// With limit=2 we should keep the newest 2 by file order, which are the
	// last two we wrote.
	if rows[0].Path != "projB/z.md" || rows[1].Path != "projA/q.md" {
		t.Errorf("wrong tail window: %+v", rows)
	}
}

func TestTailFiltered_CombinedFilters(t *testing.T) {
	l := seedFilterLog(t)
	rows, _ := l.TailFiltered(TailOpts{Actor: "bob", Action: ActionUpdate})
	if len(rows) != 1 || rows[0].Path != "projA/y.md" {
		t.Errorf("combined filter failed: %+v", rows)
	}
}
