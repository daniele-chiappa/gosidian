package views

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/dbschema"
	"github.com/gosidian/gosidian/internal/index"
)

// relIndex: a bug, and plans that reach it from a field, from the body,
// or not at all (IMP-127 iteration 2).
func relIndex(t *testing.T) *index.Index {
	t.Helper()
	idx, err := index.Open(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	for p, body := range map[string]string{
		"p/docs/bugs/BUG-1.md": "---\ntitle: BUG-1\nid: BUG-1\nstatus: open\n---\n",
		"p/plans/fix.md":       "---\ntitle: Fix\nstatus: done\nrelated: [\"[[BUG-1]]\"]\n---\n",
		"p/plans/mention.md":   "---\ntitle: Mention\nstatus: draft\n---\n\nSee [[p/docs/bugs/BUG-1]].\n",
		"p/plans/other.md":     "---\ntitle: Other\nstatus: draft\n---\n",
	} {
		if err := idx.Upsert(index.NoteDoc{Path: p, Title: strings.TrimSuffix(filepath.Base(p), ".md"), Body: body, ModTime: 1, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
	}
	return idx
}

func bugContext(idx *index.Index) Context {
	return Context{This: ThisFields("p/docs/bugs/BUG-1.md", map[string]any{"id": "BUG-1"}), Resolve: idx.Resolve}
}

// `this` alone is the note holding the view: `related contains this` keeps
// the notes whose related field links to it, `links contains this` all the
// notes that link to it; a [[wikilink]] value names a note the same way.
func TestRun_Relations(t *testing.T) {
	idx := relIndex(t)
	c := bugContext(idx)
	for spec, want := range map[string]string{
		"from: p/plans\nwhere: [related contains this]\nsort: path":                     "fix",
		"from: p/plans\nwhere: [links contains this]\nsort: path":                       "fix,mention",
		"from: p/plans\nwhere: [{field: links, op: contains, value: this}]\nsort: path": "fix,mention",
		"from: p/plans\nwhere: [related != this]\nsort: path":                           "mention,other",
		"from: p/plans\nwhere:\n  - related = [[p/docs/bugs/BUG-1]]\nsort: path":        "fix",
		"from: p/plans\nwhere: [\"links contains [[BUG-1|the bug]]\"]\nsort: path":      "fix,mention",
	} {
		if got := ids(run(t, idx, spec, c)); got != want {
			t.Errorf("%q: %s, want %s", spec, got, want)
		}
	}
	r := run(t, idx, "from: p/plans\nwhere: [links contains this]", c)
	if !reflect.DeepEqual(r.Spec.Columns, []string{"title"}) {
		t.Errorf("links is a pseudo-field, not a column: %v", r.Spec.Columns)
	}
	if call := r.QueryCall(); !strings.Contains(call, `{"field":"links","op":"contains","value":"[[p/docs/bugs/BUG-1]]"}`) {
		t.Errorf("QueryCall = %s", call)
	}
	for spec, msg := range map[string]string{
		"from: p/plans\nwhere:\n  - related contains [[p/none]]": "[[p/none]] names no note",
		"from: p/plans\nwhere: [links > this]":                   "a condition on links takes eq, ne, in or contains",
	} {
		if _, err := Parse(spec, c); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%q: err = %v, want %q", spec, err, msg)
		}
	}
	if _, err := Parse("from: p/plans\nwhere: [related contains this]", Context{}); err == nil || !strings.Contains(err.Error(), "not in a saved note") {
		t.Errorf("this without a note: %v", err)
	}
}

// A new row made from a view that filters on a relation to this note
// starts with the link to it, so it shows in the view.
func TestData_DefaultsRelation(t *testing.T) {
	idx := relIndex(t)
	s, err := dbschema.Parse("p/plans.md", "type: database\nsource: p/plans\nfields:\n  related: {type: relation}\n  status: {type: select, options: [draft, done]}\n")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := Parse("from: p/plans\nwhere: [related contains this, links contains this, status = draft]", bugContext(idx))
	if err != nil {
		t.Fatal(err)
	}
	d := (&Result{Spec: spec, Schema: s}).Data(Context{})
	if want := map[string]any{"related": "[[p/docs/bugs/BUG-1]]", "status": "draft"}; !reflect.DeepEqual(d.Defaults, want) {
		t.Errorf("defaults = %#v, want %#v", d.Defaults, want)
	}
}

// Row views: computed for the row, rendered for an agent between markers
// after the body, a broken one as a warning; a copy pasted into the note
// is dropped.
func TestRowViews(t *testing.T) {
	idx := relIndex(t)
	s, err := dbschema.Parse("p/docs/bugs.md", "type: database\nsource: p/docs/bugs\nfields:\n  id: {type: text}\n  status: {type: select, options: [open, done]}\n"+
		"row_views:\n  - title: Plans that fix it\n    from: p/plans\n    where: [related contains this]\n    columns: [title, status]\n"+
		"  - title: Everything that links here\n    from: p/plans\n    where: [links contains this]\n    as: list\n"+
		"  - title: Broken\n    from: p/plans\n    where: [status ~ x]\n")
	if err != nil {
		t.Fatal(err)
	}
	rs := ComputeRowViews(s.RowViews, bugContext(idx), idx.Query)
	if len(rs) != 3 || rs[0].Err != nil || rs[0].Result.Total != 1 || rs[1].Result.Total != 2 || rs[2].Err == nil {
		t.Fatalf("results = %+v", rs)
	}
	md := RowViewsMarkdown(s.Path, rs)
	for _, want := range []string{"<!-- gosidian:row-views", "from the row_views of p/docs/bugs.md", "**Plans that fix it**",
		"| [[p/plans/fix\\|Fix]] | done |", "**Everything that links here**", "- [[p/plans/mention\\|Mention]]", "> ⚠️ view:", rowViewsClose} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
	body := []byte("# BUG-1\n\nText.\n" + md)
	if got := string(StripRowViews(body)); strings.Contains(got, "row-views") || !strings.HasPrefix(got, "# BUG-1\n\nText.\n") {
		t.Errorf("StripRowViews = %q", got)
	}
	data := RowViewsData(rs, Context{})
	if len(data) != 3 || data[0].Title != "Plans that fix it" || len(data[0].View.Rows) != 1 || data[1].View.As != "list" || data[2].View.Error == "" {
		t.Errorf("data = %+v", data)
	}
}
