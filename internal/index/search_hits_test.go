package index

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var updateGolden = flag.Bool("update", false, "rewrite the search goldens in testdata")

// searchCorpus writes a corpus where one word, cache, is in more notes than
// a search's candidate pool, at every place of their text, with its
// inflections, identifiers that share a prefix, titles, backlinks,
// importance, pinned and archived tags and ages: what decides a search's
// hits, their order, their score, their why and their snippet.
func searchCorpus(t testing.TB, idx *Index, fixed time.Time) {
	t.Helper()
	r := rand.New(rand.NewSource(11))
	filler := strings.Fields("the team moved the service behind a new gateway and measured again after the release " +
		"while the dashboard kept the numbers of every region and the logs showed fewer retries than before " +
		"a review of the queue found two workers idle most of the night so the schedule changed")
	extra := []string{"cache", "caching", "cached", "caches", "warmup", "stream", "streams", "streaming", "INC-2", "INC-21", "INC-204", "lantern"}
	for k := 0; k < 160; k++ {
		project := "p"
		if k%5 == 0 {
			project = "q"
		}
		var words []string
		for w := 0; w < 40+r.Intn(80); w++ {
			words = append(words, filler[r.Intn(len(filler))])
		}
		if k < 120 {
			for c := 0; c <= k%3; c++ {
				pos := r.Intn(len(words) + 1)
				words = append(words[:pos], append([]string{"cache"}, words[pos:]...)...)
			}
		}
		for c := 0; c < 2; c++ {
			if e := extra[r.Intn(len(extra))]; r.Intn(3) == 0 {
				pos := r.Intn(len(words) + 1)
				words = append(words[:pos], append([]string{e}, words[pos:]...)...)
			}
		}
		var fm []string
		if k%17 == 0 {
			fm = append(fm, "importance: 5")
		}
		switch k % 23 {
		case 3:
			fm = append(fm, "tags: [pinned]")
		case 7:
			fm = append(fm, "tags: [status:archived]")
		}
		if k%9 == 0 {
			fm = append(fm, fmt.Sprintf("title: Cache report %d", k))
		}
		body := fmt.Sprintf("# Note %d\n\n", k) + strings.Join(words, " ") + ".\n"
		if k%13 == 0 {
			body += "\nSee [[p/note-1]].\n"
		}
		if len(fm) > 0 {
			body = "---\n" + strings.Join(fm, "\n") + "\n---\n\n" + body
		}
		age := time.Duration(r.Intn(400)) * 24 * time.Hour
		if err := idx.UpsertUnresolved(NoteDoc{Path: fmt.Sprintf("%s/note-%d.md", project, k), Title: fmt.Sprintf("note-%d", k),
			Body: body, ModTime: fixed.Add(-age).Unix(), Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
	}
	if err := idx.ResolveAll(); err != nil {
		t.Fatal(err)
	}
}

// What a search returns, every field of every hit, for the shapes of query
// memory_search sends: pinned in testdata/search_hits.golden, so a change to
// how the hits are found shows as a diff (go test -run SearchWith_Hits -update
// rewrites it, for a change of results that is meant).
func TestSearchWith_Hits(t *testing.T) {
	fixed := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return fixed }
	t.Cleanup(func() { now = time.Now })
	idx := openTest(t)
	searchCorpus(t, idx, fixed)
	matchGolden(t, "search_hits.golden", searchHits(t, idx, hitsCases))
}

var hitsCases = []searchCase{
	{"cache", SearchOptions{Limit: 11}},
	{"cache", SearchOptions{Limit: 3}},
	{"cache", SearchOptions{Limit: 60}},
	{"cache warmup", SearchOptions{Limit: 11}},
	{"streams", SearchOptions{Limit: 11}},
	{"INC-2", SearchOptions{Limit: 11}},
	{"Cache — report", SearchOptions{Limit: 11}},
	{"cache", SearchOptions{Limit: 11, Projects: []string{"q"}}},
	{"cache", SearchOptions{Limit: 11, Exclude: []string{"q"}}},
	{"cache", SearchOptions{Limit: 11, Variants: []string{"caching layer", "warm cache"}}},
	{"cache", SearchOptions{Limit: 11, TextOnly: true}},
	{"nothing matches this", SearchOptions{Limit: 11}},
	// Each scope is its own statement: two projects, then none again.
	{"cache", SearchOptions{Limit: 11, Projects: []string{"p", "q"}}},
	{"cache", SearchOptions{Limit: 11}},
}

// longCorpus writes long notes, 70 to 330 KB, next to short ones with the
// same words: the searched words near the start, in the middle or at the end
// of a long body, in a cluster or once, or only in its title. A snippet is
// cut from the whole body, so where the words sit decides it.
func longCorpus(t testing.TB, idx *Index, fixed time.Time) {
	t.Helper()
	r := rand.New(rand.NewSource(13))
	filler := strings.Fields("the team moved the service behind a new gateway and measured again after the release " +
		"while the dashboard kept the numbers of every region and the logs showed fewer retries than before. " +
		"A review of the queue found two workers idle most of the night so the schedule changed.")
	for k := 0; k < 48; k++ {
		long := k%3 == 0
		n := 40 + r.Intn(80)
		if long {
			n = 12000 + r.Intn(43000)
		}
		words := make([]string, n)
		for w := range words {
			words[w] = filler[r.Intn(len(filler))]
		}
		put := func(at int, ws ...string) {
			at = min(at, len(words))
			words = append(words[:at], append(ws, words[at:]...)...)
		}
		// Where cache sits: near the start, in the middle, at the end, or
		// nowhere (k%4 == 3); a cluster of it in some long notes, so that
		// they rank among the first hits despite their length.
		switch k % 4 {
		case 0:
			put(r.Intn(30), "cache")
		case 1:
			put(n/2, "cache")
		case 2:
			put(n-r.Intn(30), "cache")
		}
		if long && k%2 == 0 {
			for c := 0; c < 40; c++ {
				put(n/3+c*7, "cache")
			}
		}
		if k%5 == 1 {
			put(r.Intn(len(words)), "warmup")
		}
		if k%7 == 2 {
			put(r.Intn(len(words)), "lantern", "glow")
		}
		project := "p"
		if k%4 == 1 {
			project = "q"
		}
		name := fmt.Sprintf("note-%d", k)
		if long {
			name = fmt.Sprintf("long-%d", k)
		}
		body := fmt.Sprintf("# %s\n\n", name) + strings.Join(words, " ") + ".\n"
		if k%12 == 6 {
			body = fmt.Sprintf("---\ntitle: Lantern handbook %d\n---\n\n", k) + body
		}
		if err := idx.UpsertUnresolved(NoteDoc{Path: fmt.Sprintf("%s/%s.md", project, name), Title: name,
			Body: body, ModTime: fixed.Add(-time.Duration(k) * 24 * time.Hour).Unix(), Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
	}
	if err := idx.ResolveAll(); err != nil {
		t.Fatal(err)
	}
}

// The same on long notes, pinned in testdata/search_long.golden: their
// snippets, as hits of one query, of a fused search, or as the candidates a
// low limit leaves out.
func TestSearchWith_LongHits(t *testing.T) {
	fixed := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return fixed }
	t.Cleanup(func() { now = time.Now })
	idx := openTest(t)
	longCorpus(t, idx, fixed)
	matchGolden(t, "search_long.golden", searchHits(t, idx, longCases))
}

// A snippet cut later is the one the pool would have cut: every one
// waiting gives the hits of none waiting, for the short notes and the long
// ones, with FTS5 cutting every snippet (no huge note cut by cutSnippet).
func TestSearchWith_SnippetsLater(t *testing.T) {
	fixed := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return fixed }
	t.Cleanup(func() { now = time.Now })
	defer func(n, h int64) { longNote, hugeNote = n, h }(longNote, hugeNote)
	hugeNote = 1 << 40
	for _, c := range []struct {
		name   string
		corpus func(testing.TB, *Index, time.Time)
		cases  []searchCase
	}{
		{"short notes", searchCorpus, hitsCases},
		{"long notes", longCorpus, longCases},
	} {
		run := func(cut int64) string {
			longNote = cut
			idx := openTest(t)
			c.corpus(t, idx, fixed)
			return searchHits(t, idx, c.cases)
		}
		if waiting, cut := run(0), run(1<<40); waiting != cut {
			t.Errorf("%s: the snippets cut later differ from the pool's", c.name)
		}
	}
}

var longCases = []searchCase{
	{"cache", SearchOptions{Limit: 11}},
	{"cache", SearchOptions{Limit: 3}},
	{"cache", SearchOptions{Limit: 40}},
	{"cache warmup", SearchOptions{Limit: 11}},
	{"lantern", SearchOptions{Limit: 11}},
	{"lantern glow", SearchOptions{Limit: 11}},
	{"cache", SearchOptions{Limit: 11, Projects: []string{"q"}}},
	{"cache", SearchOptions{Limit: 11, Variants: []string{"lantern glow", "warmup"}}},
	{"warmup", SearchOptions{Limit: 5, Variants: []string{"cache"}}},
	{"cache", SearchOptions{Limit: 11, TextOnly: true}},
}

// BenchmarkSearch_LongNotes runs the searches of TestSearchWith_LongHits,
// one sub-benchmark each: a search among long documents.
func BenchmarkSearch_LongNotes(b *testing.B) {
	idx, err := Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { idx.Close() })
	longCorpus(b, idx, time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC))
	for k, c := range longCases {
		b.Run(fmt.Sprintf("%d-%s", k, strings.ReplaceAll(c.q, " ", "_")), func(b *testing.B) {
			for b.Loop() {
				if _, err := idx.SearchWith(c.q, c.opts); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

type searchCase struct {
	q    string
	opts SearchOptions
}

// searchHits runs the cases and writes every field of every hit.
func searchHits(t *testing.T, idx *Index, cases []searchCase) string {
	t.Helper()
	var b strings.Builder
	for _, c := range cases {
		hits, err := idx.SearchWith(c.q, c.opts)
		if err != nil {
			t.Fatalf("%q: %v", c.q, err)
		}
		fmt.Fprintf(&b, "## %q limit=%d projects=%v exclude=%v variants=%q textonly=%v: %d hits\n",
			c.q, c.opts.Limit, c.opts.Projects, c.opts.Exclude, c.opts.Variants, c.opts.TextOnly, len(hits))
		for _, h := range hits {
			fmt.Fprintf(&b, "%s | %s | %v | %s\n    %s\n", h.Path, h.Title, h.Score, strings.Join(h.Why, "; "), h.Snippet)
		}
	}
	return b.String()
}

// matchGolden compares got with testdata/name, after rewriting it with -update.
func matchGolden(t *testing.T, name, got string) {
	t.Helper()
	golden := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		g, w := strings.Split(got, "\n"), strings.Split(string(want), "\n")
		for k := 0; k < len(g) || k < len(w); k++ {
			var gl, wl string
			if k < len(g) {
				gl = g[k]
			}
			if k < len(w) {
				wl = w[k]
			}
			if gl != wl {
				t.Fatalf("hits differ from %s at line %d\n got: %s\nwant: %s", golden, k+1, gl, wl)
			}
		}
	}
}
