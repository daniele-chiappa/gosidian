package diagram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The examples of docs/diagrams/examples are the format's contract (ADR-036):
// every valid one reads without problems, every invalid one has the problem
// its first line names. plancia-diagrams runs the same files.
func TestExamples(t *testing.T) {
	root := filepath.Join("..", "..", "docs", "diagrams", "examples")
	valid, _ := filepath.Glob(filepath.Join(root, "valid", "*.yaml"))
	invalid, _ := filepath.Glob(filepath.Join(root, "invalid", "*.yaml"))
	if len(valid) < 10 || len(invalid) < 25 {
		t.Fatalf("examples: %d valid, %d invalid", len(valid), len(invalid))
	}
	kinds := map[Kind]bool{}
	for _, f := range valid {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		d, problems := Parse(string(src))
		if d == nil || len(problems) > 0 {
			t.Errorf("%s: %v", filepath.Base(f), problems)
			continue
		}
		kinds[d.Kind] = true
		if Text(d) == "" {
			t.Errorf("%s: no text", filepath.Base(f))
		}
	}
	for _, k := range Kinds {
		if !kinds[k] {
			t.Errorf("no valid example of kind %s", k)
		}
	}
	for _, f := range invalid {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		first, _, _ := strings.Cut(string(src), "\n")
		want, ok := strings.CutPrefix(first, "# error: ")
		if !ok {
			t.Errorf("%s: the first line is not # error: …", filepath.Base(f))
			continue
		}
		_, problems := Parse(string(src))
		found := false
		for _, p := range problems {
			if strings.Contains(p.Msg, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: want %q, got %v", filepath.Base(f), want, problems)
		}
	}
}

func TestParse_ShortFormsBecomeFull(t *testing.T) {
	d, problems := Parse(`kind: flow
nodes: [a, {id: b, label: B}, c-1]
edges:
  - "a -> b"
  - "b->c-1: go: now"
  - {from: c-1, to: a, style: dotted}`)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	if d.Direction != "TB" || d.Nodes[0].Label != "a" || d.Nodes[0].Shape != "box" {
		t.Errorf("defaults: %+v", d.Nodes[0])
	}
	want := []Edge{{From: "a", To: "b", Style: "solid"}, {From: "b", To: "c-1", Label: "go: now", Style: "solid"}, {From: "c-1", To: "a", Style: "dotted"}}
	for i, e := range d.Edges {
		e.Line = 0
		if e != want[i] {
			t.Errorf("edge %d = %+v, want %+v", i, e, want[i])
		}
	}

	m, problems := Parse(`kind: mindmap
root: {label: Centre, id: c}
children:
  - Leaf
  - Branch: [x, {y: [z]}]
  - {label: Full, children: [w]}`)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	if got := Text(m); got != `Mind map of "Centre": Leaf; Branch (x, y (z)); Full (w)` {
		t.Errorf("text = %q", got)
	}
}

func TestParse_ProblemsHaveLines(t *testing.T) {
	_, problems := Parse("kind: flow\nnodes:\n  - a\n  - {id: a}\nedges:\n  - \"a -> zz\"\n")
	lines := map[int]bool{}
	for _, p := range problems {
		lines[p.Line] = true
	}
	if !lines[4] || !lines[6] {
		t.Errorf("problems = %v", problems)
	}
}

func TestParse_Caps(t *testing.T) {
	if _, problems := Parse(strings.Repeat("x", MaxBytes+1)); len(problems) != 1 || !strings.Contains(problems[0].Msg, "bytes") {
		t.Errorf("size: %v", problems)
	}
	var b strings.Builder
	b.WriteString("kind: flow\nnodes:\n")
	for i := 0; i <= MaxItems; i++ {
		b.WriteString("  - n" + strings.Repeat("a", i%30) + "\n")
	}
	if _, problems := Parse(b.String()); len(problems) == 0 || !strings.Contains(problems[0].Msg, "more than") {
		t.Errorf("items: %v", problems[:min(3, len(problems))])
	}
}

func TestFindLinksAndSummaries(t *testing.T) {
	body := []byte("---\ntitle: x\n---\n\n# Note\n\n```diagram\nkind: flow\nnodes:\n  - {id: a, link: \"[[team/one]]\"}\n  - {id: b, link: \"https://example.com\"}\nedges: [\"a -> b\"]\n```\n\n````md\n```diagram\nkind: venn\n```\n````\n\n```diagram\nkind: gantt\ntasks:\n  - {label: T, start: 2026-10-10, days: 1, link: \"[[team/two#Plan|plan]]\"}\n```\n\n```diagram\nkind: nope\n```\n")
	blocks := Find(body)
	if len(blocks) != 3 || blocks[0].Line != 7 || blocks[1].Line != 21 {
		t.Fatalf("blocks = %+v", blocks)
	}
	links := LinksIn(body)
	if len(links) != 2 || links[0].Target != "team/one" || links[1].Target != "team/two#Plan" {
		t.Errorf("links = %+v", links)
	}
	sums := Summaries(body)
	if len(sums) != 3 || sums[0].Kind != Flow || sums[0].Text != "Flow (TB), 2 nodes, 1 edge: a → b" || sums[2].Kind != "" || len(sums[2].Problems) != 1 {
		t.Errorf("summaries = %+v", sums)
	}
	if Summaries([]byte("no diagrams")) != nil {
		t.Error("a note without diagrams has summaries")
	}
}

func TestText_Kinds(t *testing.T) {
	for src, want := range map[string]string{
		"kind: venn\nsets: [A, B]\nareas:\n  - {sets: [A, B], label: both}":                                                   "Venn of 2 sets: A, B; A ∩ B: both",
		"kind: matrix\nrows: [r1, r2]\ncolumns: [c1]\ncells:\n  - [x]\n  - []":                                                "Matrix of 2 rows by 1 column (r1, r2 × c1); r1 — c1: x",
		"kind: gantt\ntasks:\n  - {id: a, label: A, start: 2026-10-10, days: 2}\n  - {label: B, after: a, status: milestone}": "Gantt, 2 tasks; A from 2026-10-10 for 2 days; B after A [milestone]",
		"kind: fishbone\ntitle: T\nroot: Late\nchildren:\n  - Methods: [m1]":                                                  `T — Fishbone of the effect "Late": Methods (m1)`,
	} {
		d, problems := Parse(src)
		if len(problems) > 0 {
			t.Errorf("%q: %v", src, problems)
			continue
		}
		if got := Text(d); got != want {
			t.Errorf("text = %q, want %q", got, want)
		}
	}
}
