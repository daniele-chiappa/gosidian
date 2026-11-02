package index

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestFold(t *testing.T) {
	for in, want := range map[string]string{
		"cache": "cache", "Cache": "cache", "PORTÉE": "portee", "Portée": "portee", "À": "a", "Straße": "straße",
	} {
		if got := fold(in); got != want {
			t.Errorf("fold(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTokenize(t *testing.T) {
	text := "L'eau — à portée: INC-204, x_y 3.5"
	var got []string
	for _, s := range tokenize(text) {
		got = append(got, text[s.start:s.end])
	}
	if want := []string{"L", "eau", "à", "portée", "INC", "204", "x", "y", "3", "5"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("tokens = %q, want %q", got, want)
	}
}

// words writes n filler words with the given words at their places.
func words(n int, at map[int]string) string {
	ws := make([]string, n)
	for k := range ws {
		ws[k] = fmt.Sprintf("w%d", k)
		if w, ok := at[k]; ok {
			ws[k] = w
		}
	}
	return strings.Join(ws, " ")
}

func TestCutSnippet(t *testing.T) {
	term := func(tokens ...string) snippetTerm { return snippetTerm{tokens: tokens} }
	for _, c := range []struct {
		name  string
		body  string
		terms []snippetTerm
		want  string
	}{
		{"centered on the match", words(100, map[int]string{50: "cache"}), []snippetTerm{term("cache")},
			"…w39 w40 w41 w42 w43 w44 w45 w46 w47 w48 w49 <mark>cache</mark> w51 w52 w53 w54 w55 w56 w57 w58 w59 w60 w61 w62…"},
		{"two terms beat repeats of one", words(100, map[int]string{10: "alpha", 12: "alpha", 14: "alpha", 70: "alpha", 72: "beta"}),
			[]snippetTerm{term("alpha"), term("beta")},
			"…w60 w61 w62 w63 w64 w65 w66 w67 w68 w69 <mark>alpha</mark> w71 <mark>beta</mark> w73 w74 w75 w76 w77 w78 w79 w80 w81 w82 w83…"},
		{"a prefix, a phrase and a diacritic", "Avvio — la stampa di INC-204 a portée: fine.",
			[]snippetTerm{term("stam"), term("inc", "2"), term("portee")},
			"Avvio — la <mark>stampa</mark> di <mark>INC-204</mark> a <mark>portée</mark>: fine"},
		{"an inflection", "the job retries twice", []snippetTerm{{tokens: []string{"retry"}, forms: []string{"retries"}}},
			"the job <mark>retries</mark> twice"},
		{"at the start", words(40, map[int]string{0: "cache"}), []snippetTerm{term("cache")},
			"<mark>cache</mark> w1 w2 w3 w4 w5 w6 w7 w8 w9 w10 w11 w12 w13 w14 w15 w16 w17 w18 w19 w20 w21 w22 w23…"},
		{"at the end", words(40, map[int]string{39: "cache"}), []snippetTerm{term("cache")},
			"…w16 w17 w18 w19 w20 w21 w22 w23 w24 w25 w26 w27 w28 w29 w30 w31 w32 w33 w34 w35 w36 w37 w38 <mark>cache</mark>"},
		{"no match shows the start", words(40, nil), []snippetTerm{term("cache")},
			"w0 w1 w2 w3 w4 w5 w6 w7 w8 w9 w10 w11 w12 w13 w14 w15 w16 w17 w18 w19 w20 w21 w22 w23…"},
		{"an empty body", "— —", []snippetTerm{term("cache")}, ""},
	} {
		if got := cutSnippet(c.body, c.terms); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

// The snippets of the long notes of vocabCorpus cut here instead of by
// FTS5, pinned in testdata/search_vocab_huge.golden.
func TestSearchWith_HugeNoteSnippets(t *testing.T) {
	fixed := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return fixed }
	t.Cleanup(func() { now = time.Now })
	defer func(n int64) { hugeNote = n }(hugeNote)
	hugeNote = 16 << 10
	idx := openTest(t)
	vocab := vocabCorpus(t, idx, fixed, 300)
	matchGolden(t, "search_vocab_huge.golden", searchHits(t, idx, vocabCases(vocab)))
}

// A huge note's snippet takes time in proportion to the note, not to the
// square of its matches: a body of 200,000 tokens, each a match.
func TestCutSnippet_Linear(t *testing.T) {
	body := strings.Repeat("di ", 200_000)
	start := time.Now()
	got := cutSnippet(body, []snippetTerm{{tokens: []string{"d"}}})
	if !strings.HasPrefix(got, "<mark>di</mark> <mark>di</mark>") {
		t.Errorf("snippet = %.60q", got)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("cutSnippet took %v on 200,000 matches", d)
	}
}
