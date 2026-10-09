package views

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/dbschema"
	"github.com/gosidian/gosidian/internal/index"
)

// rollupIndex: improvements, and plans that implement them through a
// relation, some still open, with an estimate and a due date.
func rollupIndex(t *testing.T) *index.Index {
	t.Helper()
	idx, err := index.Open(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	for p, body := range map[string]string{
		"p/docs/improvements/IMP-1.md": "---\ntitle: IMP-1\nid: IMP-1\nstatus: open\n---\n",
		"p/docs/improvements/IMP-2.md": "---\ntitle: IMP-2\nid: IMP-2\nstatus: open\n---\n",
		"p/docs/improvements/IMP-3.md": "---\ntitle: IMP-3\nid: IMP-3\nstatus: done\n---\n",
		"p/plans/a.md":                 "---\ntitle: A\ntype: plan\nstatus: in-progress\nestimate: 3\ndue: 2026-10-20\nimplements_imp: [\"[[p/docs/improvements/IMP-1]]\", \"[[p/docs/improvements/IMP-2]]\"]\n---\n",
		"p/plans/b.md":                 "---\ntitle: B\ntype: plan\nstatus: done\nestimate: 5\ndue: 2026-10-10\nimplements_imp: [\"[[p/docs/improvements/IMP-1]]\"]\n---\n",
		"p/plans/c.md":                 "---\ntitle: C\ntype: plan\nstatus: draft\nestimate: 1.5\nimplements_imp: [\"[[p/docs/improvements/IMP-1]]\"]\n---\n",
		"p/plans/README.md":            "---\ntitle: Plans\n---\n\n[[p/docs/improvements/IMP-1]]\n",
	} {
		if err := idx.Upsert(index.NoteDoc{Path: p, Title: strings.TrimSuffix(filepath.Base(p), ".md"), Body: body, ModTime: 1, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
	}
	return idx
}

const impSchema = `type: database
source: p/docs/improvements
fields:
  id: {type: text}
  status: {type: select, options: [open, done]}
  plans:
    type: rollup
    from: p/plans
    where: [implements_imp contains this]
  open_plans:
    type: rollup
    from: p/plans
    where:
      - implements_imp contains this
      - status in [draft, in-progress]
  effort:
    type: rollup
    from: p/plans
    where: [implements_imp contains this]
    calc: sum
    of: estimate
  first_due:
    type: rollup
    from: p/plans
    where: [implements_imp contains this]
    calc: min
    of: due
  everything:
    type: rollup
    from: p/plans
    where: [links contains this]
`

const plansSchema = `type: database
source: p/plans
rows: {type: plan}
fields:
  status: {type: select, options: [draft, in-progress, done]}
  implements_imp: {type: relation}
  imps_done:
    type: rollup
    from: p/docs/improvements
    where: [path in this.implements_imp, status = done]
  imps:
    type: rollup
    from: p/docs/improvements
    where: [path in this.implements_imp]
`

func rollupContext(t *testing.T, idx *index.Index) Context {
	t.Helper()
	schemas := map[string]*dbschema.Schema{}
	for p, fm := range map[string]string{"p/docs/improvements.md": impSchema, "p/plans.md": plansSchema} {
		s, err := dbschema.Parse(p, fm)
		if err != nil {
			t.Fatal(err)
		}
		schemas[s.Source] = s
	}
	return Context{Schema: func(f string) *dbschema.Schema { return schemas[f] }, Resolve: idx.Resolve}
}

func cellsOf(r *Result, col string) string {
	var out []string
	for _, h := range r.Hits {
		out = append(out, strings.TrimSuffix(filepath.Base(h.Path), ".md")+"="+strings.Join(h.Fields[col], ","))
	}
	return strings.Join(out, " ")
}

// A rollup column counts the notes that point at each row from a relation,
// sums or reduces one of their fields; the rows a database declares (rows:)
// are the only ones it counts, so the index of the plans folder that links
// to IMP-1 from its body does not.
func TestRollups_Columns(t *testing.T) {
	idx := rollupIndex(t)
	c := rollupContext(t, idx)
	r, err := compute("from: p/docs/improvements\nsort: id asc\ncolumns: [title, plans, open_plans, effort, first_due, everything]", c, idx.Query)
	if err != nil {
		t.Fatal(err)
	}
	for col, want := range map[string]string{
		"plans":      "IMP-1=3 IMP-2=1 IMP-3=0",
		"open_plans": "IMP-1=2 IMP-2=1 IMP-3=0",
		"effort":     "IMP-1=9.5 IMP-2=3 IMP-3=0",
		"first_due":  "IMP-1=2026-10-10 IMP-2=2026-10-20 IMP-3=",
		"everything": "IMP-1=3 IMP-2=1 IMP-3=0",
	} {
		if got := cellsOf(r, col); got != want {
			t.Errorf("%s: %s, want %s", col, got, want)
		}
	}
	if md := r.Markdown(); !strings.Contains(md, "| plans | open_plans |") || !strings.Contains(md, "| 3 | 2 | 9.5 | 2026-10-10 | 3 |") {
		t.Errorf("markdown:\n%s", md)
	}
	if d := r.Data(c); d.Columns[1].Type != "rollup" || d.Rows[0].Fields["plans"] != "3" {
		t.Errorf("data: %+v %+v", d.Columns[1], d.Rows[0].Fields)
	}
}

// A rollup over the notes the row's own relation points at: path in
// this.<relation>. A row without the relation counts 0.
func TestRollups_OutgoingRelation(t *testing.T) {
	idx := rollupIndex(t)
	c := rollupContext(t, idx)
	r, err := compute("from: p/plans\nsort: path\ncolumns: [title, imps, imps_done]", c, idx.Query)
	if err != nil {
		t.Fatal(err)
	}
	if got := cellsOf(r, "imps"); got != "a=2 b=1 c=1" {
		t.Errorf("imps: %s", got)
	}
	if got := cellsOf(r, "imps_done"); got != "a=0 b=0 c=0" {
		t.Errorf("imps_done: %s", got)
	}
}

// A relation naming a note that is gone counts nothing for that link, and
// the view still computes, every row with it (BUG-114, S4-1).
func TestRollups_DanglingRelation(t *testing.T) {
	idx := rollupIndex(t)
	body := "---\ntitle: D\ntype: plan\nstatus: draft\nimplements_imp: [\"[[p/docs/improvements/IMP-1]]\", \"[[p/docs/improvements/IMP-999]]\"]\n---\n"
	if err := idx.Upsert(index.NoteDoc{Path: "p/plans/d.md", Title: "d", Body: body, ModTime: 1, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	c := rollupContext(t, idx)
	r, err := compute("from: p/plans\nsort: path\ncolumns: [title, imps]", c, idx.Query)
	if err != nil {
		t.Fatalf("one dangling link failed the view: %v", err)
	}
	if got := cellsOf(r, "imps"); got != "a=2 b=1 c=1 d=1" {
		t.Errorf("imps: %s", got)
	}
}

// The schema of a rollup's folder is looked up once for the whole table,
// not for every row and rollup (BUG-114, S4-8).
func TestRollups_OneSchemaLookupPerFolder(t *testing.T) {
	idx := rollupIndex(t)
	c := rollupContext(t, idx)
	lookups := map[string]int{}
	inner := c.Schema
	c.Schema = func(f string) *dbschema.Schema {
		lookups[f]++
		return inner(f)
	}
	if _, err := compute("from: p/docs/improvements\nsort: id asc\ncolumns: [title, plans, open_plans, effort, first_due]", c, idx.Query); err != nil {
		t.Fatal(err)
	}
	if n := lookups["p/plans"]; n > 1 {
		t.Errorf("the rollups looked up p/plans %d times for 3 rows and 4 rollups", n)
	}
}

// An inline =count(…) counts the rows of a database only, as a view does:
// p/plans/README.md is in the folder but no plan (BUG-114, S4-10).
func TestCountValue_DatabaseRows(t *testing.T) {
	idx := rollupIndex(t)
	c := rollupContext(t, idx)
	out, _ := ExpandValues([]byte("Plans: `=count(p/plans)`."), true, c, idx.Query)
	if !strings.Contains(string(out), "Plans: 3 (") {
		t.Errorf("count = %s, want the 3 plans without the README", out)
	}
}

// A whole number in a map condition is a value like any other: YAML reads
// value: 5 as an int, and it was refused (BUG-114, S4-6).
func TestView_IntegerInAMapCondition(t *testing.T) {
	idx := rollupIndex(t)
	for spec, want := range map[string]string{
		"from: p/plans\nwhere:\n  - {field: estimate, op: eq, value: 5}":   "b",
		"from: p/plans\nwhere:\n  - {field: estimate, op: eq, value: 1.5}": "c",
	} {
		s, err := Parse(spec, Context{})
		if err != nil {
			t.Fatalf("%q: %v", spec, err)
		}
		r, err := Run(s, idx.Query)
		if err != nil {
			t.Fatal(err)
		}
		if got := cellsOf(r, "title"); got != want+"=" {
			t.Errorf("%q = %s, want %s", spec, got, want)
		}
	}
}

// Sorted by a rollup, every row is computed and sorted, then cut at the
// limit; a condition or a group_by on a rollup is refused.
func TestRollups_SortAndRefusals(t *testing.T) {
	idx := rollupIndex(t)
	c := rollupContext(t, idx)
	r, err := compute("from: p/docs/improvements\nsort: plans desc\nlimit: 2\ncolumns: [title, plans]", c, idx.Query)
	if err != nil {
		t.Fatal(err)
	}
	if got := cellsOf(r, "plans"); got != "IMP-1=3 IMP-2=1" || r.Total != 3 {
		t.Errorf("sorted: %s (total %d)", got, r.Total)
	}
	r, err = compute("from: p/docs/improvements\nsort: first_due asc\ncolumns: [title, first_due]", c, idx.Query)
	if err != nil {
		t.Fatal(err)
	}
	if got := cellsOf(r, "first_due"); got != "IMP-1=2026-10-10 IMP-2=2026-10-20 IMP-3=" {
		t.Errorf("sorted by date, empty last: %s", got)
	}
	// A rollup after a field: the field orders, by the options of its
	// select (open, done), and the rollup breaks its ties, though the view
	// does not show the field (IMP-143).
	r, err = compute("from: p/docs/improvements\nsort: [status asc, plans asc]\ncolumns: [title, plans]", c, idx.Query)
	if err != nil {
		t.Fatal(err)
	}
	if got := cellsOf(r, "plans"); got != "IMP-2=1 IMP-1=3 IMP-3=0" {
		t.Errorf("status, then plans: %s", got)
	}
	r, err = compute("from: p/docs/improvements\nsort: open_plans desc, effort asc, id\nlimit: 2\ncolumns: [title, open_plans]", c, idx.Query)
	if err != nil {
		t.Fatal(err)
	}
	if got := cellsOf(r, "open_plans"); got != "IMP-1=2 IMP-2=1" || r.Total != 3 {
		t.Errorf("open_plans, then effort: %s (total %d)", got, r.Total)
	}
	for spec, msg := range map[string]string{
		"from: p/docs/improvements\nwhere: [plans > 1]":              "is a rollup",
		"from: p/docs/improvements\nas: count\ngroup_by: open_plans": "is a rollup",
	} {
		if _, err := compute(spec, c, idx.Query); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%q: err = %v, want %q", spec, err, msg)
		}
	}
}

func TestAggregate(t *testing.T) {
	for _, tc := range []struct {
		calc   string
		values []string
		want   string
	}{
		{"sum", []string{"1", "2.5", "x"}, "3.5"},
		{"sum", nil, "0"},
		{"min", []string{"10", "9"}, "9"},
		{"max", []string{"10", "9"}, "10"},
		{"max", []string{"2026-01-02", "2026-10-01"}, "2026-10-01"},
		{"min", nil, ""},
	} {
		if got := aggregate(tc.calc, tc.values); got != tc.want {
			t.Errorf("%s %v = %q, want %q", tc.calc, tc.values, got, tc.want)
		}
	}
}
