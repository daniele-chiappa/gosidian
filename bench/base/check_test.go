package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCheck_VerdictsAndLimits(t *testing.T) {
	two := 2.0
	for _, tc := range []struct {
		name string
		c    check
		v    float64
		want string
	}{
		{"a time within its room", check{Ref: 100, Pct: 20}, 119, "ok"},
		{"a time past it", check{Ref: 100, Pct: 20}, 121, "worse"},
		{"a time faster by more than the room", check{Ref: 100, Pct: 20}, 79, "better"},
		{"the floor of a fast call", check{Ref: 0.4, Pct: 25, Min: 0.1}, 0.49, "ok"},
		{"past the floor", check{Ref: 0.4, Pct: 25, Min: 0.1}, 0.51, "worse"},
		{"a byte more, with no room", check{Ref: 1000}, 1001, "worse"},
		{"a byte less", check{Ref: 1000}, 999, "better"},
		{"the same result", check{Ref: 0.711, HigherBetter: true}, 0.711, "ok"},
		{"a result that falls", check{Ref: 0.711, HigherBetter: true}, 0.689, "worse"},
		{"a result that rises", check{Ref: 0.711, HigherBetter: true}, 0.733, "better"},
		{"one miss less", check{Ref: 3, Target: &two}, 2, "better"},
	} {
		if got := tc.c.verdict(tc.v); got != tc.want {
			t.Errorf("%s: verdict(%v) = %s, want %s", tc.name, tc.v, got, tc.want)
		}
	}
	if l := (check{Ref: 0.4, Pct: 25, Min: 0.1}).limit(); l != 0.5 {
		t.Errorf("limit with a floor = %v, want 0.5", l)
	}
	if !(check{Ref: 3, Target: &two}).reached(2) || (check{Ref: 3, Target: &two}).reached(3) {
		t.Error("an objective of at most 2 misses")
	}
}

func TestLookup_PathsAndMedians(t *testing.T) {
	var doc any
	json.Unmarshal([]byte(`{"start":{"cold_ms":[480,450,470]},"latency":{"memory_get":{"p50_ms":0.43}},"spa":{"bytes":5}}`), &doc)
	for path, want := range map[string]float64{"start.cold_ms": 470, "latency.memory_get.p50_ms": 0.43, "spa.bytes": 5} {
		if v, ok := lookup(doc, path); !ok || v != want {
			t.Errorf("lookup(%s) = %v %v, want %v", path, v, ok, want)
		}
	}
	for _, path := range []string{"start.warm_ms", "latency.memory_get", "spa.bytes.x", "large.cold_ms"} {
		if _, ok := lookup(doc, path); ok {
			t.Errorf("lookup(%s) found a number", path)
		}
	}
}

// The reference's numbers all exist in a results file, and the runs it is
// the median of pass it: a limit tighter than their noise would fail the
// guard on nothing.
func TestReference_TheRunsItCameFromPassIt(t *testing.T) {
	ref, err := readReference("reference.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(ref.From) == 0 || ref.Set == "" || ref.Why == "" || ref.MaxLoad == 0 {
		t.Fatalf("the reference says neither when, why nor from what: %+v", ref)
	}
	moved := map[string]bool{}
	for _, m := range ref.Moves {
		for _, metric := range m.Metrics {
			moved[metric] = true
		}
	}
	// only is ref with just the checks keep keeps.
	only := func(keep func(c check) bool) *reference {
		sub := *ref
		sub.Checks = slices.DeleteFunc(slices.Clone(ref.Checks), func(c check) bool { return !keep(c) })
		return &sub
	}
	pass := func(ref *reference, files []string) {
		for _, f := range files {
			doc, res, err := readResults(filepath.Join("..", "results", f))
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if bad := printCheck(&out, ref, doc, res); bad != 0 {
				t.Errorf("%s: %d numbers past their limits:\n%s", f, bad, out.String())
			}
		}
	}
	pass(only(func(c check) bool { return !moved[c.Metric] }), ref.From)
	for i, m := range ref.Moves {
		if m.Set == "" || m.Why == "" || len(m.From) == 0 || len(m.Metrics) == 0 {
			t.Errorf("a move says neither when, why, from what nor which: %+v", m)
		}
		// A metric a later move set again comes from that move's runs.
		later := map[string]bool{}
		for _, n := range ref.Moves[i+1:] {
			for _, metric := range n.Metrics {
				later[metric] = true
			}
		}
		pass(only(func(c check) bool { return slices.Contains(m.Metrics, c.Metric) && !later[c.Metric] }), m.From)
	}
}

func TestPrintCheck_TheLinesTheNightlyQuotes(t *testing.T) {
	ref := &reference{Set: "2026-10-29", Why: "a test", MaxLoad: 4, Checks: []check{
		{Name: "Start, cold (ms)", Metric: "start.cold_ms", Ref: 450, Pct: 15},
		{Name: "Bootstrap (bytes)", Metric: "surface.bootstrap_bytes", Ref: 25000},
		{Name: "Large vault: start, cold (ms)", Metric: "large.cold_ms", Ref: 5700, Pct: 20},
	}}
	var doc any
	json.Unmarshal([]byte(`{"start":{"cold_ms":[600]},"surface":{"bootstrap_bytes":24000}}`), &doc)
	res := &result{Machine: machine{LoadBefore: 6.5}}
	var out bytes.Buffer
	if bad := printCheck(&out, ref, doc, res); bad != 2 {
		t.Errorf("bad = %d, want 2 (the start worse, the large vault missing)", bad)
	}
	for _, line := range []string{"WORSE Start, cold (ms): 600, the reference 450, the limit 518", "MISSING Large vault: start, cold (ms)", "BETTER Bootstrap (bytes): 24000 against 25000", "LOAD 6.50 before the measure, above 4"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("no %q in:\n%s", line, out.String())
		}
	}
}

// One run of three the host slowed moves no percentile: each is the median
// of the runs' own.
func TestSummarizeRuns_OneSlowRunOfThree(t *testing.T) {
	run := func(base time.Duration) []time.Duration {
		var s []time.Duration
		for k := range 100 {
			s = append(s, base+time.Duration(k)*time.Microsecond)
		}
		return s
	}
	l := summarizeRuns([][]time.Duration{run(4 * time.Millisecond), run(time.Millisecond), run(time.Millisecond)}, 2)
	if l.N != 300 || l.Errors != 2 {
		t.Errorf("N, Errors = %d, %d", l.N, l.Errors)
	}
	if l.P50 != 1.049 || l.P95 != 1.094 {
		t.Errorf("p50, p95 = %v, %v: the slow run counted", l.P50, l.P95)
	}
	if one := summarizeRuns([][]time.Duration{run(time.Millisecond)}, 0); one != summarize(run(time.Millisecond), 0) {
		t.Errorf("one run: %+v, summarize: %+v", one, summarize(run(time.Millisecond), 0))
	}
}
