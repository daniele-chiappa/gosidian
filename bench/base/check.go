package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// reference is bench/base/reference.json: the base's numbers as the
// roadmap's M1.2 decided them, each with the room it has before it counts
// as worse, and its objective when it has one. It moves only with a commit
// that says why (base reference): all of it, which Set, Why and From say,
// or some numbers, which a move says.
type reference struct {
	Schema  int      `json:"schema"`
	Set     string   `json:"set"`
	Why     string   `json:"why"`
	From    []string `json:"from"`
	MaxLoad float64  `json:"max_load"` // above it, the times of a measure may be off
	Checks  []check  `json:"checks"`
	Moves   []move   `json:"moves,omitempty"` // oldest first
}

// move is some numbers moved after the whole reference was set.
type move struct {
	Set     string   `json:"set"`
	Why     string   `json:"why"`
	From    []string `json:"from"`
	Metrics []string `json:"metrics"`
}

// check is one number: its path in a results file (a list reads as its
// median), its reference, and the room past it the wrong way: pct of the
// reference, at least min. Without either, any move the wrong way counts:
// the numbers with no noise, the search's results and the bytes.
type check struct {
	Name         string   `json:"name"`
	Metric       string   `json:"metric"`
	Ref          float64  `json:"ref"`
	HigherBetter bool     `json:"higher_better,omitempty"`
	Pct          float64  `json:"pct,omitempty"`
	Min          float64  `json:"min,omitempty"`
	Target       *float64 `json:"target,omitempty"`
}

const eps = 1e-9

func (c check) room() float64 { return math.Max(c.Ref*c.Pct/100, c.Min) }

// limit is the value past which the number is worse.
func (c check) limit() float64 {
	if c.HigherBetter {
		return c.Ref - c.room()
	}
	return c.Ref + c.room()
}

// verdict is worse past the limit, better past the reference by the same
// room the other way (by anything, for a number with no room), else ok.
func (c check) verdict(v float64) string {
	gain := v - c.Ref
	if !c.HigherBetter {
		gain = -gain
	}
	switch {
	case gain < -c.room()-eps:
		return "worse"
	case gain > c.room()+eps:
		return "better"
	}
	return "ok"
}

func (c check) reached(v float64) bool {
	if c.Target == nil {
		return false
	}
	if c.HigherBetter {
		return v >= *c.Target-eps
	}
	return v <= *c.Target+eps
}

// lookup reads a number of a results file by its dotted path; a list of
// numbers (one per run) reads as its median.
func lookup(doc any, path string) (float64, bool) {
	for _, k := range strings.Split(path, ".") {
		m, ok := doc.(map[string]any)
		if !ok {
			return 0, false
		}
		if doc, ok = m[k]; !ok {
			return 0, false
		}
	}
	switch v := doc.(type) {
	case float64:
		return v, true
	case []any:
		fs := make([]float64, 0, len(v))
		for _, x := range v {
			f, ok := x.(float64)
			if !ok {
				return 0, false
			}
			fs = append(fs, f)
		}
		if len(fs) == 0 {
			return 0, false
		}
		return median(fs), true
	}
	return 0, false
}

func readReference(path string) (*reference, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	ref := &reference{}
	if err := json.Unmarshal(data, ref); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return ref, nil
}

// readResults reads a results file both ways: as a document, for the
// checks' paths, and as a result, for the machine and the errors.
func readResults(path string) (any, *result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var doc any
	r := &result{}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := json.Unmarshal(data, r); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	return doc, r, nil
}

// checkCmd compares one results file with the reference and fails when a
// number is past its limit or missing, or the bench met errors: the guard
// of the nightly run and of a tag (the roadmap's M1.4).
func checkCmd(args []string) error {
	fs := flag.NewFlagSet("check", flag.ExitOnError)
	refPath := fs.String("ref", "bench/base/reference.json", "the reference")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("check: one results file")
	}
	ref, err := readReference(*refPath)
	if err != nil {
		return err
	}
	doc, res, err := readResults(fs.Arg(0))
	if err != nil {
		return err
	}
	if bad := printCheck(os.Stdout, ref, doc, res); bad > 0 {
		return fmt.Errorf("check: %d of %d numbers past their limits or missing", bad, len(ref.Checks))
	}
	return nil
}

// printCheck writes the table and, under it, one line per number to act
// on: WORSE and MISSING (which it counts), BETTER, and the load. The
// nightly run's message quotes the WORSE and MISSING lines.
func printCheck(w io.Writer, ref *reference, doc any, res *result) int {
	var b, notes strings.Builder
	fmt.Fprintf(&b, "Reference of %s: %s\n", ref.Set, ref.Why)
	for _, m := range ref.Moves {
		fmt.Fprintf(&b, "Moved on %s, %s: %s\n", m.Set, strings.Join(m.Metrics, ", "), m.Why)
	}
	b.WriteString("\n")
	b.WriteString("| Number | Reference | Now | Worse past | Objective | |\n|---|---|---|---|---|---|\n")
	bad := 0
	for _, c := range ref.Checks {
		past := "> " + num(c.limit())
		if c.HigherBetter {
			past = "< " + num(c.limit())
		}
		obj := "—"
		if c.Target != nil {
			obj = map[bool]string{true: "≥ ", false: "≤ "}[c.HigherBetter] + num(*c.Target)
		}
		v, ok := lookup(doc, c.Metric)
		now, verdict := "—", "missing"
		if ok {
			now, verdict = num(v), c.verdict(v)
			if c.reached(v) {
				verdict += ", objective reached"
			}
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", c.Name, num(c.Ref), now, past, obj, verdict)
		switch {
		case !ok:
			bad++
			fmt.Fprintf(&notes, "MISSING %s (%s): not in the results\n", c.Name, c.Metric)
		case strings.HasPrefix(verdict, "worse"):
			bad++
			fmt.Fprintf(&notes, "WORSE %s: %s, the reference %s, the limit %s\n", c.Name, num(v), num(c.Ref), num(c.limit()))
		case strings.HasPrefix(verdict, "better"):
			fmt.Fprintf(&notes, "BETTER %s: %s against %s, past the noise: move the reference (base reference) in a commit that says why\n", c.Name, num(v), num(c.Ref))
		}
	}
	if n := len(res.Errors); n > 0 {
		bad++
		fmt.Fprintf(&notes, "WORSE the bench met %d errors: %s\n", n, res.Errors[0])
	}
	if ref.MaxLoad > 0 && res.Machine.LoadBefore > ref.MaxLoad {
		fmt.Fprintf(&notes, "LOAD %.2f before the measure, above %.0f: its times may be off\n", res.Machine.LoadBefore, ref.MaxLoad)
	}
	if notes.Len() > 0 {
		b.WriteString("\n" + notes.String())
	}
	io.WriteString(w, b.String())
	return bad
}

// referenceCmd moves the reference to the medians of the results given,
// keeping each number's room and objective, and says what changed: all of
// it, or with -only the metrics named, which a move records.
func referenceCmd(args []string) error {
	fs := flag.NewFlagSet("reference", flag.ExitOnError)
	refPath := fs.String("ref", "bench/base/reference.json", "the reference to rewrite")
	why := fs.String("why", "", "why it moves: it goes in the file, and in the commit (required)")
	only := fs.String("only", "", "the metrics to move, comma-separated (default: all)")
	fs.Parse(args)
	if *why == "" || fs.NArg() == 0 {
		return fmt.Errorf("reference: -why and one or more results files")
	}
	ref, err := readReference(*refPath)
	if err != nil {
		return err
	}
	var docs []any
	var from []string
	for _, f := range fs.Args() {
		doc, _, err := readResults(f)
		if err != nil {
			return err
		}
		docs = append(docs, doc)
		from = append(from, filepath.Base(f))
	}
	moving := map[string]bool{}
	for _, m := range strings.Split(*only, ",") {
		if m = strings.TrimSpace(m); m != "" {
			moving[m] = true
		}
	}
	for m := range moving {
		if !slices.ContainsFunc(ref.Checks, func(c check) bool { return c.Metric == m }) {
			return fmt.Errorf("reference: no check of %s", m)
		}
	}
	var moved []string
	for i, c := range ref.Checks {
		if len(moving) > 0 && !moving[c.Metric] {
			continue
		}
		moved = append(moved, c.Metric)
		var vs []float64
		for j, d := range docs {
			v, ok := lookup(d, c.Metric)
			if !ok {
				return fmt.Errorf("reference: %s is not in %s", c.Metric, from[j])
			}
			vs = append(vs, v)
		}
		nv := round(median(vs), 3)
		if nv != c.Ref {
			fmt.Printf("%s: %s → %s\n", c.Name, num(c.Ref), num(nv))
		}
		ref.Checks[i].Ref = nv
	}
	today := time.Now().UTC().Format("2006-01-02")
	if len(moving) > 0 {
		ref.Moves = append(ref.Moves, move{Set: today, Why: *why, From: from, Metrics: moved})
	} else {
		ref.Set, ref.Why, ref.From, ref.Moves = today, *why, from, nil
	}
	data, err := json.MarshalIndent(ref, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(*refPath, append(data, '\n'), 0o644)
}

// num prints a number as the results hold it: whole above 100, else with
// up to three decimals.
func num(v float64) string {
	if math.Abs(v) >= 100 {
		return fmt.Sprintf("%.0f", v)
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.3f", v), "0"), ".")
}
