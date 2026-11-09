package audit

import (
	"os"
	"path/filepath"
	"strings"
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

// The admin's audit shows the newest entries first, in pages, with how
// many match in all (IMP-224, F-30).
func TestLog_Page(t *testing.T) {
	l := seedFilterLog(t)
	said := func(rows []Entry) string {
		var s []string
		for _, r := range rows {
			s = append(s, string(r.Action)+" "+r.Path)
		}
		return strings.Join(s, ", ")
	}
	for _, c := range []struct {
		name        string
		opts        TailOpts
		offset      int
		newestFirst bool
		want        string
		total       int
	}{
		{"the newest first", TailOpts{Limit: 2}, 0, true, "create projA/q.md, delete projB/z.md", 5},
		{"the next page", TailOpts{Limit: 2}, 2, true, "append projB/z.md, update projA/y.md", 5},
		{"the last page, shorter", TailOpts{Limit: 2}, 4, true, "create projA/x.md", 5},
		{"past the end", TailOpts{Limit: 2}, 5, true, "", 5},
		{"the oldest first", TailOpts{Limit: 2}, 1, false, "update projA/y.md, append projB/z.md", 5},
		{"oldest, past the end", TailOpts{Limit: 2}, 9, false, "", 5},
		{"a filter counts what it keeps", TailOpts{Actor: "alice", Limit: 10}, 0, true, "delete projB/z.md, create projA/x.md", 2},
		{"a negative offset is none", TailOpts{Limit: 1}, -3, true, "create projA/q.md", 5},
	} {
		rows, total, err := l.Page(c.opts, c.offset, c.newestFirst)
		if err != nil {
			t.Fatal(err)
		}
		if got := said(rows); got != c.want || total != c.total {
			t.Errorf("%s: %q of %d, want %q of %d", c.name, got, total, c.want, c.total)
		}
	}
	var none *Log
	if rows, total, err := none.Page(TailOpts{}, 0, true); rows != nil || total != 0 || err != nil {
		t.Errorf("nil log: %v %d %v", rows, total, err)
	}
}

// OnWrite hooks see every entry written, after it is on disk; Each reads
// the log back, oldest first, skipping a malformed line.
func TestLog_OnWriteAndEach(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var seen []string
	l.OnWrite(func(e Entry) {
		if data, err := os.ReadFile(path); err != nil || !strings.Contains(string(data), e.Path) {
			t.Errorf("hook ran before the line was written: %v", err)
		}
		seen = append(seen, string(e.Action)+" "+e.Path)
	})
	_ = l.Write(Entry{Source: SourceMCP, Action: ActionCreate, Path: "p/a.md"})
	if err := os.WriteFile(path, append(must(os.ReadFile(path)), []byte("not json\n")...), 0o640); err != nil {
		t.Fatal(err)
	}
	_ = l.Write(Entry{Source: SourceHTTP, Action: ActionUpdate, Path: "p/a.md"})
	if strings.Join(seen, ",") != "create p/a.md,update p/a.md" {
		t.Errorf("hooks saw %v", seen)
	}
	var read []string
	if err := l.Each(func(e Entry) { read = append(read, string(e.Action)) }); err != nil {
		t.Fatal(err)
	}
	if strings.Join(read, ",") != "create,update" {
		t.Errorf("Each read %v", read)
	}
	missing, _ := Open(filepath.Join(t.TempDir(), "none", "audit.jsonl"))
	if err := missing.Each(func(Entry) { t.Error("an empty log has no entry") }); err != nil {
		t.Errorf("a missing log is empty: %v", err)
	}
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}
