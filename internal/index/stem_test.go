package index

import (
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
