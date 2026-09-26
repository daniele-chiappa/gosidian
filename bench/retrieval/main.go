// Command retrieval runs level R of the gosidian benchmark (IMP-096): it
// indexes the benchmark vault with the server's own code (vault scan +
// index.SearchWith, the path memory_search takes) and reports where the gold
// notes of every question rank. No model and no network: the numbers are
// deterministic for a given vault, question set and query set.
//
//	go run ./bench/retrieval            # from the repository root
//	go run ./bench/retrieval -v         # plus the rank of every question
//	go run ./bench/retrieval -json out.json
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/vault"
)

// question is one line of questions.jsonl. RGold lists the notes that count
// as a hit at level R; questions without it (negatives, list questions)
// belong to level A only.
type question struct {
	ID       string   `json:"id"`
	Category string   `json:"category"`
	Question string   `json:"question"`
	Answer   string   `json:"answer"`
	Check    []string `json:"check"`
	Evidence []string `json:"evidence"`
	RGold    []string `json:"r_gold"`
	Accept   []string `json:"accept"` // level A: regexps an answer must match (bench/agents)
}

// query is one line of queries.jsonl, written by an agent that saw only the
// question text (the leakage control of the benchmark).
type query struct {
	ID    string   `json:"id"`
	Query string   `json:"query"`
	AnyOf []string `json:"any_of"`
}

// mode is one way of searching the same question.
type mode struct {
	Name string
	Opts func(q query, k int) index.SearchOptions
}

var modes = []mode{
	{"bm25", func(q query, k int) index.SearchOptions { return index.SearchOptions{Limit: k, TextOnly: true} }},
	{"ranked", func(q query, k int) index.SearchOptions { return index.SearchOptions{Limit: k} }},
	{"ranked+any_of", func(q query, k int) index.SearchOptions {
		return index.SearchOptions{Limit: k, Variants: q.AnyOf}
	}},
}

// categories fixes the report order.
var categories = []string{"lexical", "typed", "multi-hop", "temporal", "paraphrase", "cross-lingual"}

// metrics aggregates the ranks of a set of questions.
type metrics struct {
	N    int     `json:"n"`
	R1   float64 `json:"r_at_1"`
	R5   float64 `json:"r_at_5"`
	MRR  float64 `json:"mrr"`
	Miss int     `json:"miss"`
}

type modeResult struct {
	Overall    metrics            `json:"overall"`
	ByCategory map[string]metrics `json:"by_category"`
	Ranks      map[string]int     `json:"ranks"` // question id → 1-based rank of the first gold note, 0 = not in top k
}

type report struct {
	Date      string                `json:"date"`
	Notes     int                   `json:"notes"`
	Questions int                   `json:"questions"`
	K         int                   `json:"k"`
	Modes     map[string]modeResult `json:"modes"`
}

func main() {
	vaultDir := flag.String("vault", "bench/vault", "benchmark vault")
	questionsPath := flag.String("questions", "bench/questions.jsonl", "questions with gold notes")
	queriesPath := flag.String("queries", "bench/queries.jsonl", "search queries per question")
	k := flag.Int("k", 10, "hits considered per search")
	jsonOut := flag.String("json", "", "also write the report as JSON to this file")
	verbose := flag.Bool("v", false, "print the rank of every question")
	flag.Parse()

	rep, qs, err := run(*vaultDir, *questionsPath, *queriesPath, *k)
	if err != nil {
		fmt.Fprintln(os.Stderr, "retrieval:", err)
		os.Exit(1)
	}
	printReport(os.Stdout, rep, qs, *verbose)
	if *jsonOut != "" {
		buf, _ := json.MarshalIndent(rep, "", "  ")
		if err := os.WriteFile(*jsonOut, append(buf, '\n'), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "retrieval:", err)
			os.Exit(1)
		}
	}
}

// run indexes a copy of the vault and evaluates every mode.
func run(vaultDir, questionsPath, queriesPath string, k int) (report, []question, error) {
	qs, err := readJSONL[question](questionsPath)
	if err != nil {
		return report{}, nil, err
	}
	queries, err := readJSONL[query](queriesPath)
	if err != nil {
		return report{}, nil, err
	}
	byID := map[string]query{}
	for _, q := range queries {
		byID[q.ID] = q
	}

	tmp, err := os.MkdirTemp("", "gosidian-bench-")
	if err != nil {
		return report{}, nil, err
	}
	defer os.RemoveAll(tmp)
	notes, err := copyVault(vaultDir, filepath.Join(tmp, "vault"))
	if err != nil {
		return report{}, nil, err
	}
	idx, err := index.Open(filepath.Join(tmp, "index.db"))
	if err != nil {
		return report{}, nil, err
	}
	defer idx.Close()
	if err := vault.New(filepath.Join(tmp, "vault")).ScanInto(idx); err != nil {
		return report{}, nil, err
	}

	rep := report{Date: time.Now().UTC().Format("2006-01-02"), Notes: notes, K: k, Modes: map[string]modeResult{}}
	for _, m := range modes {
		res := modeResult{ByCategory: map[string]metrics{}, Ranks: map[string]int{}}
		for _, q := range qs {
			if len(q.RGold) == 0 {
				continue
			}
			qq, ok := byID[q.ID]
			if !ok {
				return report{}, nil, fmt.Errorf("no query for question %s", q.ID)
			}
			hits, err := idx.SearchWith(qq.Query, m.Opts(qq, k))
			if err != nil {
				return report{}, nil, fmt.Errorf("%s %s: %w", m.Name, q.ID, err)
			}
			res.Ranks[q.ID] = firstGold(hits, q.RGold)
		}
		res.Overall = aggregate(qs, res.Ranks, "")
		for _, c := range categories {
			res.ByCategory[c] = aggregate(qs, res.Ranks, c)
		}
		rep.Modes[m.Name] = res
	}
	rep.Questions = len(rep.Modes[modes[0].Name].Ranks)
	return rep, qs, nil
}

// firstGold returns the 1-based position of the first hit that is a gold
// note, or 0 when none is in the list.
func firstGold(hits []index.SearchHit, gold []string) int {
	for i, h := range hits {
		for _, g := range gold {
			if h.Path == g {
				return i + 1
			}
		}
	}
	return 0
}

// aggregate computes the metrics over the ranked questions of one category
// ("" = all).
func aggregate(qs []question, ranks map[string]int, category string) metrics {
	var m metrics
	for _, q := range qs {
		r, ok := ranks[q.ID]
		if !ok || (category != "" && q.Category != category) {
			continue
		}
		m.N++
		switch {
		case r == 0:
			m.Miss++
		case r == 1:
			m.R1++
			m.R5++
			m.MRR++
		case r <= 5:
			m.R5++
			m.MRR += 1 / float64(r)
		default:
			m.MRR += 1 / float64(r)
		}
	}
	if m.N > 0 {
		m.R1 /= float64(m.N)
		m.R5 /= float64(m.N)
		m.MRR /= float64(m.N)
	}
	return m
}

func printReport(w io.Writer, rep report, qs []question, verbose bool) {
	fmt.Fprintf(w, "Level R — %d questions, %d notes, top %d, %s\n\n", rep.Questions, rep.Notes, rep.K, rep.Date)
	fmt.Fprintln(w, "| mode | R@1 | R@5 | MRR | misses |")
	fmt.Fprintln(w, "|---|---|---|---|---|")
	for _, m := range modes {
		o := rep.Modes[m.Name].Overall
		fmt.Fprintf(w, "| %s | %.2f | %.2f | %.2f | %d |\n", m.Name, o.R1, o.R5, o.MRR, o.Miss)
	}
	fmt.Fprintln(w, "\nR@5 by category:")
	fmt.Fprintln(w)
	header := "| category | n |"
	sep := "|---|---|"
	for _, m := range modes {
		header += " " + m.Name + " |"
		sep += "---|"
	}
	fmt.Fprintln(w, header)
	fmt.Fprintln(w, sep)
	for _, c := range categories {
		row := fmt.Sprintf("| %s | %d |", c, rep.Modes[modes[0].Name].ByCategory[c].N)
		for _, m := range modes {
			row += fmt.Sprintf(" %.2f |", rep.Modes[m.Name].ByCategory[c].R5)
		}
		fmt.Fprintln(w, row)
	}
	if !verbose {
		return
	}
	fmt.Fprintln(w, "\nRank of the first gold note (0 = not in top k):")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| id | category | "+strings.Join(modeNames(), " | ")+" |")
	fmt.Fprintln(w, "|---|---|"+strings.Repeat("---|", len(modes)))
	ids := make([]string, 0, len(qs))
	cat := map[string]string{}
	for _, q := range qs {
		if _, ok := rep.Modes[modes[0].Name].Ranks[q.ID]; ok {
			ids = append(ids, q.ID)
			cat[q.ID] = q.Category
		}
	}
	sort.SliceStable(ids, func(a, b int) bool { return categoryIndex(cat[ids[a]]) < categoryIndex(cat[ids[b]]) })
	for _, id := range ids {
		row := fmt.Sprintf("| %s | %s |", id, cat[id])
		for _, m := range modes {
			row += fmt.Sprintf(" %d |", rep.Modes[m.Name].Ranks[id])
		}
		fmt.Fprintln(w, row)
	}
}

func modeNames() []string {
	out := make([]string, len(modes))
	for i, m := range modes {
		out[i] = m.Name
	}
	return out
}

func categoryIndex(c string) int {
	for i, x := range categories {
		if x == c {
			return i
		}
	}
	return len(categories)
}

// copyVault copies the markdown notes of src into dst with one fixed
// modification time, so the recency factor is the same for every note
// whatever the checkout time. Returns the number of notes.
func copyVault(src, dst string) (int, error) {
	fixed := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	n := 0
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !strings.HasSuffix(p, ".md") {
			return nil
		}
		buf, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, buf, 0o644); err != nil {
			return err
		}
		n++
		return os.Chtimes(target, fixed, fixed)
	})
	return n, err
}

func readJSONL[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []T
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for line := 1; sc.Scan(); line++ {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var v T
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		out = append(out, v)
	}
	return out, sc.Err()
}
