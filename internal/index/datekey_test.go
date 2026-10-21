package index

import (
	"slices"
	"testing"

	"github.com/gosidian/gosidian/internal/clock/clocktest"
)

// timeNotes indexes one note per value of created, named by its key.
func timeNotes(t *testing.T, created map[string]string) *Index {
	t.Helper()
	idx := openTest(t)
	for name, v := range created {
		upsert(t, idx, "p/"+name+".md", name, "---\ntitle: "+name+"\ncreated: "+v+"\n---\n")
	}
	return idx
}

func createdOrder(t *testing.T, idx *Index, desc bool, where ...FieldCond) []string {
	t.Helper()
	keys := []SortKey{{Field: "created", Desc: desc}}
	opts := QueryOptions{Folders: []string{"p"}, Where: where, Fields: []string{"created"}, Limit: 50}
	opts.SetSortKeys(keys)
	hits, _, err := idx.Query(opts)
	if err != nil {
		t.Fatal(err)
	}
	// SortHits orders as the query does.
	again := slices.Clone(hits)
	slices.Reverse(again)
	SortHits(again, keys)
	if !slices.Equal(hitPaths(again), hitPaths(hits)) {
		t.Errorf("SortHits %v, query %v", hitPaths(again), hitPaths(hits))
	}
	var out []string
	for _, h := range hits {
		out = append(out, h.Title)
	}
	return out
}

// Old notes say Z, new ones the server's offset (IMP-208): a time compares
// and sorts by its instant, a time or a day without an offset is in the
// notes' zone, and a bare day in a condition still compares with the day a
// value writes.
func TestQuery_TimesByInstant(t *testing.T) {
	clocktest.Rome(t)
	idx := timeNotes(t, map[string]string{
		"a": "2026-10-20T22:04:05+02:00", // 20:04:05Z: before b, though it reads after
		"b": "2026-10-20T20:30:00Z",
		"c": "2026-10-21T00:30:00+02:00", // 22:30Z on the 20th; the 21st in Rome
		"d": "2026-10-20 23:00",          // Rome: 21:00Z
		"e": "2026-10-21",                // Rome's midnight: 22:00Z on the 20th
	})
	if got := createdOrder(t, idx, false); !slices.Equal(got, []string{"a", "b", "d", "e", "c"}) {
		t.Errorf("ascending = %v", got)
	}
	if got := createdOrder(t, idx, true); !slices.Equal(got, []string{"c", "e", "d", "b", "a"}) {
		t.Errorf("descending = %v", got)
	}
	for _, c := range []struct {
		cond FieldCond
		want []string
	}{
		{cond("created", OpGte, "2026-10-20T20:15:00Z"), []string{"b", "d", "e", "c"}},
		{cond("created", OpLt, "2026-10-20T22:30:00+02:00"), []string{"a"}},
		{cond("created", OpEq, "2026-10-20T20:04:05Z"), []string{"a"}},
		{cond("created", OpIn, "2026-10-20T21:30:00+01:00", "2026-10-20T21:00:00Z"), []string{"b", "d"}},
		{cond("created", OpGt, "2026-10-20T23:00"), []string{"e", "c"}},
		// A day: the one written, as before.
		{cond("created", OpEq, "2026-10-21"), []string{"e", "c"}},
		{cond("created", OpLt, "2026-10-21"), []string{"a", "b", "d"}},
	} {
		if got := createdOrder(t, idx, false, c.cond); !slices.Equal(got, c.want) {
			t.Errorf("%s %s %v = %v, want %v", c.cond.Field, c.cond.Op, c.cond.Values, got, c.want)
		}
	}
}

// In UTC, with the values the vault held before IMP-208 (Z, no offset, a
// day), the order and the conditions are what the text gave.
func TestQuery_TimesInUTCAsBefore(t *testing.T) {
	clocktest.Zone(t, "UTC")
	idx := timeNotes(t, map[string]string{
		"a": "2026-10-20",
		"b": "2026-10-20T09:00",
		"c": "2026-10-20T09:00:00Z", // b's instant: the text orders the tie
		"d": "2026-10-20T10:00:00Z",
		"e": "2026-10-21",
	})
	if got := createdOrder(t, idx, false); !slices.Equal(got, []string{"a", "b", "c", "d", "e"}) {
		t.Errorf("ascending = %v", got)
	}
	if got := createdOrder(t, idx, true); !slices.Equal(got, []string{"e", "d", "c", "b", "a"}) {
		t.Errorf("descending = %v", got)
	}
	if got := createdOrder(t, idx, false, cond("created", OpGte, "2026-10-20T09:00")); !slices.Equal(got, []string{"b", "c", "d", "e"}) {
		t.Errorf("gte 09:00 = %v", got)
	}
	if got := createdOrder(t, idx, false, cond("created", OpEq, "2026-10-20")); !slices.Equal(got, []string{"a", "b", "c", "d"}) {
		t.Errorf("eq the day = %v", got)
	}
}

// A value the index types as a date but no time reads (month 13) sorts by
// its text, as before.
func TestDateKey_UnreadableKeepsText(t *testing.T) {
	if got := dateKey("2026-13-45"); got != "2026-13-45" {
		t.Errorf("dateKey = %q", got)
	}
	idx := timeNotes(t, map[string]string{"a": "2026-13-45", "b": "2026-10-20T10:00:00Z"})
	if got := createdOrder(t, idx, false); !slices.Equal(got, []string{"b", "a"}) {
		t.Errorf("ascending = %v", got)
	}
}
