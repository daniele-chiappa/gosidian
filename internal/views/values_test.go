package views

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/dbschema"
)

// A count view: the total, and with group_by one number per value, in the
// order of the schema's options; for agents a line of text.
func TestRun_Count(t *testing.T) {
	idx := testIndex(t)
	r := run(t, idx, "from: p/docs/improvements\nwhere: [status = open]\nas: count", Context{})
	if r.Total != 2 || r.Markdown() != "**2** notes\n" {
		t.Errorf("count = %d, %q", r.Total, r.Markdown())
	}
	if one := run(t, idx, "from: p/docs/improvements\nwhere: [status = done]\nas: count", Context{}); one.Markdown() != "**1** note\n" {
		t.Errorf("one = %q", one.Markdown())
	}
	s, err := dbschema.Parse("p/docs/improvements.md", "type: database\nsource: p/docs/improvements\nfields:\n  priority: {type: select, options: [low, medium, high]}\n")
	if err != nil {
		t.Fatal(err)
	}
	c := Context{Schema: func(string) *dbschema.Schema { return s }}
	g, err := compute("from: p/docs/improvements\nas: count\ngroup_by: priority", c, idx.Query)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Count{{"low", 2}, {"high", 1}}; !reflect.DeepEqual(g.Counts(), want) {
		t.Errorf("counts = %+v, want %+v", g.Counts(), want)
	}
	if md := g.Markdown(); md != "**3** notes · priority: low 2 · high 1\n" {
		t.Errorf("markdown = %q", md)
	}
	d := g.Data(Context{})
	if d.As != "count" || d.Total != 3 || len(d.Counts) != 2 || d.Group == nil || d.Group.Type != "select" || d.Rows != nil || d.Columns != nil {
		t.Errorf("data = %+v", d)
	}
	// A list field counts a note once per value; the notes without one last.
	l := run(t, idx, "from: p/plans\nas: count\ngroup_by: implements_imp", Context{})
	if want := []Count{{"IMP-001", 1}, {"IMP-002", 1}, {"IMP-003", 1}}; !reflect.DeepEqual(l.Counts(), want) || l.Total != 2 {
		t.Errorf("list counts = %+v (total %d)", l.Counts(), l.Total)
	}
	if _, err := Parse("from: p/plans\nas: table\ngroup_by: status", Context{}); err == nil {
		t.Error("group_by needs a board or a count")
	}
}

// Inline values: found outside code blocks, read as a count view, rendered
// as the number (web UI) or the number with its expression (agents).
func TestExpandValues(t *testing.T) {
	idx := testIndex(t)
	body := "# Hot\n\nOpen: `=count(p/docs/improvements where status = open)`, high: `=count(p/docs/improvements where status in [open, done] and priority = high)`.\n" +
		"All plans: `=count(p/plans)`. Bad: `=count(p/plans where status ~ x)`.\n\n```md\nsample `=count(p/plans)`\n```\n"
	if vs := FindValues([]byte(body)); len(vs) != 4 || vs[0].Expr != "p/docs/improvements where status = open" {
		t.Fatalf("values = %+v", vs)
	}
	out, hash := ExpandValues([]byte(body), true, Context{}, idx.Query)
	got := string(out)
	for _, want := range []string{"Open: 2 (`=count(p/docs/improvements where status = open)`)", "high: 1 (`=count(", "All plans: 2 (`=count(p/plans)`)",
		"Bad: ⚠️ count: condition \"status ~ x\"", "sample `=count(p/plans)`\n```"} {
		if !strings.Contains(got, want) {
			t.Errorf("agent form lacks %q:\n%s", want, got)
		}
	}
	if hash == "" {
		t.Error("values have a hash")
	}
	// Read back as computed, it is the same note: nothing doubles.
	again, hash2 := ExpandValues(out, true, Context{}, idx.Query)
	if string(again) != got || hash2 != hash {
		t.Errorf("a copied result is computed again, not doubled:\n%s", again)
	}
	web, _ := ExpandValues([]byte(body), false, Context{}, idx.Query)
	if w := string(web); !strings.Contains(w, `Open: <span class="gosidian-count" title="=count(p/docs/improvements where status = open)">2</span>`) ||
		!strings.Contains(w, `gosidian-count-error`) {
		t.Errorf("web form:\n%s", w)
	}
	// The syntax shown in prose, in a span of two backticks, stays text.
	prose := "Write `` `=count(p/plans)` `` for a number, or ``=count(p/plans)``."
	if vs := FindValues([]byte(prose)); len(vs) != 0 {
		t.Errorf("prose values = %+v", vs)
	}
	if out, hash := ExpandValues([]byte("no values"), true, Context{}, idx.Query); string(out) != "no values" || hash != "" {
		t.Errorf("no values: %q %q", out, hash)
	}
}

// The agent's form copied back is read as the value alone only where it is
// one: a number that ends a word ("v2") is text, and a code block keeps its
// sample (BUG-114, S4-4).
func TestExpandValues_CopiesAtAWordStart(t *testing.T) {
	idx := testIndex(t)
	body := "Release v2 (`=count(p/plans)`) and 5 (`=count(p/plans)`).\n\n```\n3 (`=count(p/plans)`)\n```\n"
	out, _ := ExpandValues([]byte(body), true, Context{}, idx.Query)
	got := string(out)
	for _, want := range []string{"Release v2 (2 (`=count(p/plans)`))", "and 2 (`=count(p/plans)`).", "```\n3 (`=count(p/plans)`)\n```"} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
}

func TestParseCount(t *testing.T) {
	c := Context{This: ThisFields("p/a.md", map[string]any{"id": "A"})}
	s, err := ParseCount("p/x, p/y/ where title = \"salt and pepper\" and status in [a, b] and owner = this.id", c)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.From, []string{"p/x", "p/y"}) || len(s.Where) != 3 || s.Where[0].Values[0] != "salt and pepper" ||
		!reflect.DeepEqual(s.Where[1].Values, []string{"a", "b"}) || s.Where[2].Values[0] != "A" {
		t.Errorf("spec = %+v", s)
	}
	if _, err := ParseCount(" where status = open", c); err == nil {
		t.Error("a count needs a folder")
	}
}

// The views and values of a note render together, with one hash.
func TestRenderNote_ViewsAndValues(t *testing.T) {
	idx := testIndex(t)
	body := []byte("Open `=count(p/docs/improvements where status = open)`.\n\n```view\nfrom: p/plans\nas: count\n```\n")
	out, hash := RenderNote(body, true, Context{}, idx.Query)
	if s := string(out); !strings.Contains(s, "Open 2 (`=count(") || !strings.Contains(s, "**2** notes") || hash == "" {
		t.Errorf("render:\n%s (hash %q)", s, hash)
	}
	html, data := RenderNoteData(body, Context{}, idx.Query)
	if !strings.Contains(string(html), `class="gosidian-count"`) || len(data) != 1 || data[0].As != "count" {
		t.Errorf("data render: %s %+v", html, data)
	}
}
