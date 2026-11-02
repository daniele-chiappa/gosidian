package index

import (
	"fmt"
	"math"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// vocabCorpus writes notes in a language of many words, two to four
// syllables each, with the short words of a sentence (di, con, a) a quarter
// of the text. As in a vault of real documents, a short word of a query is
// the prefix of thousands of indexed words, and a prefix query merges the
// lists of every one of them. It returns the vocabulary, most frequent
// first, for the queries.
func vocabCorpus(t testing.TB, idx *Index, fixed time.Time, notes int) []string {
	t.Helper()
	return writeVocab(t, idx, fixed, notes, func(k int, r *rand.Rand) int {
		n := 150 + r.Intn(450)
		if k%10 == 0 {
			n = 3000 + r.Intn(3000)
		}
		return n
	})
}

// hugeCorpus writes vocabCorpus's language in a few notes of one to two
// megabytes among short ones: a short word of a query matches all through
// them, and FTS5 scores every match of a note against every other to cut
// its snippet.
func hugeCorpus(t testing.TB, idx *Index, fixed time.Time) []string {
	t.Helper()
	return writeVocab(t, idx, fixed, 40, func(k int, r *rand.Rand) int {
		n := 150 + r.Intn(450)
		if k%10 == 0 {
			n = 180000 + r.Intn(180000)
		}
		return n
	})
}

// writeVocab writes notes of length(k) words in the language of
// vocabCorpus and returns its vocabulary, most frequent first.
func writeVocab(t testing.TB, idx *Index, fixed time.Time, notes int, length func(k int, r *rand.Rand) int) []string {
	t.Helper()
	r := rand.New(rand.NewSource(17))
	syllables := strings.Fields("a con di per co ri mo sta ta to ne la ge zio men ca pa so ver ti in re de se ma no li pre pro tra fi vi du bi lu gu ro po fa va")
	short := []string{"a", "di", "con", "per", "il", "la", "che"}
	vocab := make([]string, 30000)
	for k := range vocab {
		var w strings.Builder
		for s := 0; s < 2+r.Intn(3); s++ {
			w.WriteString(syllables[r.Intn(len(syllables))])
		}
		vocab[k] = w.String()
	}
	for k := 0; k < notes; k++ {
		words := make([]string, length(k, r))
		for w := range words {
			if r.Intn(4) == 0 {
				words[w] = short[r.Intn(len(short))]
			} else {
				// A few words are everywhere, most are rare.
				words[w] = vocab[int(float64(len(vocab))*math.Pow(r.Float64(), 3))]
			}
		}
		title := strings.Join(words[:3], " ")
		body := fmt.Sprintf("# %s\n\n%s.\n", title, strings.Join(words, " "))
		if err := idx.UpsertUnresolved(NoteDoc{Path: fmt.Sprintf("p/doc-%d.md", k), Title: title,
			Body: body, ModTime: fixed.Add(-time.Duration(k) * time.Hour).Unix(), Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
	}
	if err := idx.ResolveAll(); err != nil {
		t.Fatal(err)
	}
	return vocab
}

// vocabCases are searches as an agent writes them: three to five words, a
// short one among them, alone or with phrasings.
func vocabCases(vocab []string) []searchCase {
	return []searchCase{
		{"di " + vocab[3] + " " + vocab[40], SearchOptions{Limit: 11}},
		{vocab[5] + " con " + vocab[90] + " " + vocab[7], SearchOptions{Limit: 11}},
		{"a " + vocab[2] + " per " + vocab[30], SearchOptions{Limit: 11}},
		{vocab[12] + " " + vocab[150] + " di " + vocab[8], SearchOptions{Limit: 3}},
		{"con " + vocab[1], SearchOptions{Limit: 11, Variants: []string{"di " + vocab[1], vocab[1] + " a " + vocab[25]}}},
		{vocab[60] + " " + vocab[4], SearchOptions{Limit: 11}},
	}
}

// The searches of vocabCases, pinned in testdata/search_vocab.golden: what
// a query with short words finds among many words that start with them.
func TestSearchWith_ShortWordHits(t *testing.T) {
	fixed := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return fixed }
	t.Cleanup(func() { now = time.Now })
	idx := openTest(t)
	vocab := vocabCorpus(t, idx, fixed, 300)
	matchGolden(t, "search_vocab.golden", searchHits(t, idx, vocabCases(vocab)))
}

// BenchmarkSearch_ShortWords runs the searches of vocabCases on a larger
// vocabCorpus, one sub-benchmark each.
func BenchmarkSearch_ShortWords(b *testing.B) {
	idx, err := Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { idx.Close() })
	vocab := vocabCorpus(b, idx, time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC), 2000)
	for k, c := range vocabCases(vocab) {
		b.Run(fmt.Sprintf("%d-%d-words", k, len(strings.Fields(c.q))), func(b *testing.B) {
			for b.Loop() {
				if _, err := idx.SearchWith(c.q, c.opts); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkSearch_HugeNotes searches hugeCorpus: the hits include notes of
// one to two megabytes, whose snippets are most of a search.
func BenchmarkSearch_HugeNotes(b *testing.B) {
	idx, err := Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { idx.Close() })
	vocab := hugeCorpus(b, idx, time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC))
	for k, c := range []searchCase{
		{"di " + vocab[3], SearchOptions{Limit: 11}},
		{vocab[5] + " a " + vocab[1], SearchOptions{Limit: 11}},
	} {
		b.Run(fmt.Sprintf("%d-%d-words", k, len(strings.Fields(c.q))), func(b *testing.B) {
			for b.Loop() {
				if _, err := idx.SearchWith(c.q, c.opts); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
