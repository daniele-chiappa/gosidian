package index

import (
	"fmt"
	"strings"
	"testing"
)

func TestStemmer_PorterStems(t *testing.T) {
	s, err := newStemmer()
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	words := []string{"retries", "retry", "retrieval", "deployment", "certificate", "Caresses", "relational", "credenziali", "BUG-017", "ff_payments"}
	want := []string{"retri", "retri", "retriev", "deploy", "certif", "caress", "relat", "credenziali", "", ""}
	got := s.stems(words)
	for k := range words {
		if got[k] != want[k] {
			t.Errorf("stem(%q) = %q, want %q", words[k], got[k], want[k])
		}
	}
}

// The stemmer keeps nothing between calls: the second call's stems are its
// own words', whatever the first one held, and a word repeated, accented or
// alone stems as it does in a list.
func TestStemmer_CallsDoNotMix(t *testing.T) {
	s, err := newStemmer()
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if got := s.stems([]string{"deployments", "retries", "relational"}); strings.Join(got, ",") != "deploy,retri,relat" {
		t.Fatalf("first call = %q", got)
	}
	for _, c := range []struct {
		words []string
		want  string
	}{
		{[]string{"caresses", "caresses", "Résumés"}, "caress,caress,resum"},
		{[]string{"tested"}, "test"},
		{nil, ""},
		{[]string{"a b", ""}, ","},
	} {
		if got := s.stems(c.words); strings.Join(got, ",") != c.want {
			t.Errorf("stems(%q) = %q, want %s", c.words, got, c.want)
		}
	}
	if got := s.stem("streams"); got != "stream" {
		t.Errorf("stem(streams) = %q", got)
	}
	// Remembered or not, the same stems: the words of the first call again,
	// mixed with new ones, and past the cap, which forgets them all.
	if got := s.stems([]string{"retries", "testing", "deployments", "BUG-017"}); strings.Join(got, ",") != "retri,test,deploy," {
		t.Errorf("remembered and new = %q", got)
	}
	many := make([]string, maxKnownStems+10)
	for k := range many {
		many[k] = fmt.Sprintf("tested%d", k)
	}
	if got := s.stems(many); got[0] != "tested0" || got[len(got)-1] != many[len(many)-1] {
		t.Errorf("past the cap: %q … %q", got[0], got[len(got)-1])
	}
	if len(s.known) > maxKnownStems+10 {
		t.Errorf("the stemmer remembers %d stems", len(s.known))
	}
	if got := s.stems([]string{"deployments", "tested"}); strings.Join(got, ",") != "deploy,test" {
		t.Errorf("after the cap = %q", got)
	}
}

func TestMatchExpr_AddsOnlySameStemInflections(t *testing.T) {
	idx := openTest(t)
	upsert(t, idx, "p/a.md", "", "Paylane retries webhooks; one retried twice. Retrieval of notes. Tests were tested with testdata.")
	for q, want := range map[string]string{
		"retry":       `("retry"* OR "retried" OR "retries")`,
		"testing":     `("testing"* OR "tested" OR "tests")`,
		"running":     `"running"*`, // stem "run" too short
		"credenziali": `"credenziali"*`,
		"BUG-017":     `"BUG-017"*`,
		`say "hi"`:    `"say"* AND "hi"*`,
	} {
		if got := idx.matchExpr(q); got != want {
			t.Errorf("matchExpr(%q) = %s, want %s", q, got, want)
		}
	}
}

// Stems only add inflections: every note the plain word found is still
// found, and words that merely share letters stay out.
func TestSearch_StemsAddInflections(t *testing.T) {
	idx := openTest(t)
	upsert(t, idx, "p/webhooks.md", "", "Paylane retries webhook deliveries every 5 minutes.")
	upsert(t, idx, "p/retrieval.md", "", "Retrieval of webhook notes.")
	upsert(t, idx, "p/deploy.md", "", "The deployment runbook.")
	upsert(t, idx, "p/deployed.md", "", "We deployed on Monday.")
	upsert(t, idx, "p/runtime.md", "", "Runtime configuration.")

	for q, want := range map[string][]string{
		"retry webhook": {"p/webhooks.md"},
		"deploy":        {"p/deploy.md", "p/deployed.md"},
		"running":       nil,
	} {
		hits, err := idx.Search(q, 10)
		if err != nil {
			t.Fatal(err)
		}
		got := paths(hits)
		if strings.Join(got, ",") != strings.Join(want, ",") && !(len(want) == 2 && len(got) == 2) {
			t.Errorf("search %q = %v, want %v", q, got, want)
		}
	}
}

// A word of punctuation alone in the query is no term: the " — " of a
// title, "&" or a stray "-" emptied the whole search (BUG-114, S4-3).
func TestSearch_PunctuationWords(t *testing.T) {
	idx := openTest(t)
	upsert(t, idx, "p/imp.md", "IMP-152 — Code review", "The review of the code.")
	upsert(t, idx, "p/ab.md", "A & B", "Two letters.")
	for q, want := range map[string]string{
		"IMP-152 — Code review": "p/imp.md",
		"retry -":               "",
		"A & B":                 "p/ab.md",
		"— &":                   "",
	} {
		hits, err := idx.Search(q, 10)
		if err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
		if got := strings.Join(paths(hits), ","); got != want {
			t.Errorf("search %q = %q, want %q", q, got, want)
		}
	}
}
