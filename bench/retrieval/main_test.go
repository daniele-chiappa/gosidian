package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The benchmark data must stay consistent as the vault evolves: every gold
// note exists, every answer check is present in the evidence, every level-R
// question has a query.
func TestBenchmarkDataIsConsistent(t *testing.T) {
	qs, err := readJSONL[question]("../questions.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	queries, err := readJSONL[query]("../queries.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	hasQuery := map[string]bool{}
	for _, q := range queries {
		if strings.TrimSpace(q.Query) == "" {
			t.Errorf("query %s is empty", q.ID)
		}
		hasQuery[q.ID] = true
	}
	known := map[string]bool{"negative": true}
	for _, c := range categories {
		known[c] = true
	}
	seen := map[string]bool{}
	for _, q := range qs {
		if seen[q.ID] {
			t.Errorf("duplicate question id %s", q.ID)
		}
		seen[q.ID] = true
		if !known[q.Category] {
			t.Errorf("%s: unknown category %q", q.ID, q.Category)
		}
		if q.Category == "negative" && len(q.RGold) > 0 {
			t.Errorf("%s: a negative question has no retrieval gold", q.ID)
		}
		if len(q.Accept) == 0 {
			t.Errorf("%s: no accept pattern for level A", q.ID)
		}
		for _, a := range q.Accept {
			if _, err := regexp.Compile("(?i)" + a); err != nil {
				t.Errorf("%s: accept %q: %v", q.ID, a, err)
			}
		}
		if len(q.RGold) > 0 && !hasQuery[q.ID] {
			t.Errorf("%s: level-R question without a query", q.ID)
		}
		var evidence strings.Builder
		for _, p := range append(append([]string{}, q.Evidence...), q.RGold...) {
			buf, err := os.ReadFile(filepath.Join("../vault", p))
			if err != nil {
				t.Errorf("%s: gold note %s: %v", q.ID, p, err)
				continue
			}
			if contains(q.Evidence, p) {
				evidence.Write(buf)
			}
		}
		for _, c := range q.Check {
			if !strings.Contains(strings.ToLower(evidence.String()), strings.ToLower(c)) {
				t.Errorf("%s: check %q not found in the evidence notes", q.ID, c)
			}
		}
	}
}

// The runner evaluates every level-R question in every mode.
func TestRunEvaluatesEveryQuestion(t *testing.T) {
	rep, qs, err := run("../vault", "../questions.jsonl", "../queries.jsonl", 10)
	if err != nil {
		t.Fatal(err)
	}
	want := 0
	for _, q := range qs {
		if len(q.RGold) > 0 {
			want++
		}
	}
	if rep.Questions != want || rep.Notes == 0 {
		t.Fatalf("report covers %d questions over %d notes, want %d questions", rep.Questions, rep.Notes, want)
	}
	for _, m := range modes {
		if got := len(rep.Modes[m.Name].Ranks); got != want {
			t.Errorf("mode %s ranked %d questions, want %d", m.Name, got, want)
		}
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
