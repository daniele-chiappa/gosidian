package index

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// partialCorpus: deploy is in a third of the notes, friday in three of 63,
// and one note has both.
func partialCorpus(t *testing.T) *Index {
	t.Helper()
	idx := openTest(t)
	for k := 0; k < 40; k++ {
		upsert(t, idx, fmt.Sprintf("p/w%02d.md", k), "", fmt.Sprintf("# Note %d\n\nSomething about the weather.", k))
	}
	upsert(t, idx, "p/full.md", "", "# Friday deploy\n\nNo deploy on friday after 15:00.")
	upsert(t, idx, "p/freeze.md", "", "# Freeze\n\nThe friday freeze starts at 15:00.")
	upsert(t, idx, "q/lunch.md", "", "# Lunch\n\nThe team lunch is on friday.")
	for k := 0; k < 20; k++ {
		upsert(t, idx, fmt.Sprintf("p/d%02d.md", k), "", fmt.Sprintf("# Release %d\n\nA deploy of the api.", k))
	}
	return idx
}

func partialWhy(h SearchHit) string {
	for _, w := range h.Why {
		if strings.HasPrefix(w, "partial: ") {
			return w
		}
	}
	return ""
}

// When the notes with every word are fewer than the limit, the notes with
// one of the query's less common words follow them, marked partial; a
// common word alone brings none.
func TestSearchWith_PartialHitsFollowTheFullOnes(t *testing.T) {
	idx := partialCorpus(t)
	hits, err := idx.SearchWith("friday deploy", SearchOptions{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(paths(hits), ","); got != "p/full.md,p/freeze.md,q/lunch.md" && got != "p/full.md,q/lunch.md,p/freeze.md" {
		t.Fatalf("hits = %s, want the full note, then the two with friday only", got)
	}
	if w := partialWhy(hits[0]); w != "" || hits[0].Missing != nil {
		t.Errorf("the full hit says %q, missing %v", w, hits[0].Missing)
	}
	for _, h := range hits[1:] {
		if w := partialWhy(h); w != `partial: without "deploy"` || strings.Join(h.Missing, ",") != "deploy" {
			t.Errorf("%s: why %v, missing %v, want partial without deploy", h.Path, h.Why, h.Missing)
		}
		if h.Score > hits[0].Score {
			t.Errorf("%s: score %v above the full hit's %v", h.Path, h.Score, hits[0].Score)
		}
		if !strings.Contains(h.Snippet, "<mark>friday</mark>") {
			t.Errorf("%s: snippet %q does not mark friday", h.Path, h.Snippet)
		}
	}
	if hits[1].Score < hits[2].Score {
		t.Errorf("partial hits out of order: %v then %v", hits[1].Score, hits[2].Score)
	}

	// The scope holds for the partial hits too.
	for _, opts := range []SearchOptions{{Limit: 5, Projects: []string{"p"}}, {Limit: 5, Exclude: []string{"q"}}} {
		hits, err := idx.SearchWith("friday deploy", opts)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(paths(hits), ","); got != "p/full.md,p/freeze.md" {
			t.Errorf("%+v: hits = %s, want p/full.md,p/freeze.md", opts, got)
		}
	}

	// No full hit: the partial ones alone, the best at 1.
	hits, err = idx.SearchWith("friday retrospective", SearchOptions{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 || hits[0].Score != 1 {
		t.Fatalf("hits = %v, want the three friday notes, the best at 1", paths(hits))
	}
	for _, h := range hits {
		if w := partialWhy(h); w != `partial: without "retrospective"` || strings.Join(h.Missing, ",") != "retrospective" {
			t.Errorf("%s: why %v, missing %v", h.Path, h.Why, h.Missing)
		}
	}
}

// No partial hits: the full ones fill the limit, the query is one word, or
// its words are all common.
func TestSearchWith_NoPartialHits(t *testing.T) {
	idx := partialCorpus(t)
	for _, c := range []struct {
		q     string
		limit int
		want  int
	}{
		{"deploy api", 5, 5},   // the full hits fill the limit
		{"friday", 10, 3},      // one word
		{"deploy api", 30, 20}, // both words common
		{"release deploy", 30, 20},
		{"nothing matches this", 10, 0},
	} {
		hits, err := idx.SearchWith(c.q, SearchOptions{Limit: c.limit})
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != c.want {
			t.Errorf("%q limit %d: %d hits %v, want %d", c.q, c.limit, len(hits), paths(hits), c.want)
		}
		for _, h := range hits {
			if w := partialWhy(h); w != "" {
				t.Errorf("%q: %s says %q", c.q, h.Path, w)
			}
		}
	}
}

// The full hits stay what they were, field by field: the partial ones only
// follow them.
func TestSearchWith_PartialKeepsTheFullHits(t *testing.T) {
	fixed := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return fixed }
	t.Cleanup(func() { now = time.Now })
	idx := openTest(t)
	searchCorpus(t, idx, fixed)
	partial := 0
	for _, c := range []searchCase{
		{"cache warmup", SearchOptions{Limit: 11}},
		{"warmup lantern", SearchOptions{Limit: 11}},
		{"streams lantern gateway", SearchOptions{Limit: 20}},
		{"cache warmup", SearchOptions{Limit: 11, Projects: []string{"p"}}},
		{"cache warmup", SearchOptions{Limit: 11, TextOnly: true}},
		{"cache warmup", SearchOptions{Limit: 11, Variants: []string{"lantern glow"}}},
		{"warmup lantern", SearchOptions{Limit: 20, Variants: []string{"streams gateway", "INC-2 warmup"}}},
	} {
		want := fullOnly(t, idx, c.q, c.opts)
		got, err := idx.SearchWith(c.q, c.opts)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) > len(want) {
			partial++
		}
		for k, w := range want {
			if k >= len(got) {
				break
			}
			g := got[k]
			if g.Path != w.Path || g.Title != w.Title || g.Score != w.Score || g.Snippet != w.Snippet || strings.Join(g.Why, ";") != strings.Join(w.Why, ";") {
				t.Errorf("%q hit %d = %+v, want %+v", c.q, k, g, w)
			}
		}
		last := 1.0
		if len(want) > 0 {
			last = want[len(want)-1].Score
		}
		for _, g := range got[min(len(want), len(got)):] {
			if partialWhy(g) == "" || g.Score > last {
				t.Errorf("%q: %s after the full hits, why %v, score %v (last full %v)", c.q, g.Path, g.Why, g.Score, last)
			}
		}
	}
	if partial == 0 {
		t.Error("no search had partial hits")
	}
}

// fullOnly is what SearchWith returned before the partial hits: the notes
// with every word, fused across the variants.
func fullOnly(t *testing.T, idx *Index, q string, opts SearchOptions) []SearchHit {
	t.Helper()
	queries := distinctQueries(q, opts.Variants)
	var hits []rankedHit
	if len(queries) == 1 {
		full, err := idx.rankedMatch(idx.matchExpr(q), q, opts, opts.Limit, longNote)
		if err != nil {
			t.Fatal(err)
		}
		hits = full
	} else {
		depth := min(max(opts.Limit*3, minFusedDepth), maxPool)
		var lists [][]rankedHit
		for _, query := range queries {
			full, err := idx.rankedMatch(idx.matchExpr(query), query, opts, depth, longNote)
			if err != nil {
				t.Fatal(err)
			}
			lists = append(lists, full)
		}
		hits = fuse(queries, lists, opts.Limit)
	}
	out, err := idx.withSnippets(hits)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// With variants, the fused partial lists follow the fusion of the full ones
// when it falls short of the limit.
func TestSearchWith_VariantsPartialAfterTheFused(t *testing.T) {
	idx := partialCorpus(t)
	hits, err := idx.SearchWith("friday deploy", SearchOptions{Limit: 5, Variants: []string{"friday release"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 || hits[0].Path != "p/full.md" {
		t.Fatalf("hits = %v, want p/full.md, then the two friday notes", paths(hits))
	}
	if w := partialWhy(hits[0]); w != "" {
		t.Errorf("the full hit says %q", w)
	}
	for _, h := range hits[1:] {
		if partialWhy(h) == "" || strings.Join(h.Missing, ",") != "deploy" || !hasWhy(h, "matched: friday deploy | friday release") {
			t.Errorf("%s: why %v, want partial and both phrasings", h.Path, h.Why)
		}
		if h.Score > hits[0].Score {
			t.Errorf("%s: score %v above the full hit's %v", h.Path, h.Score, hits[0].Score)
		}
	}

	// The fused full hits fill the limit: no partial ones.
	hits, err = idx.SearchWith("deploy api", SearchOptions{Limit: 5, Variants: []string{"release deploy"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if w := partialWhy(h); w != "" {
			t.Errorf("%s says %q", h.Path, w)
		}
	}
	if len(hits) != 5 {
		t.Errorf("hits = %v, want 5", paths(hits))
	}
}
