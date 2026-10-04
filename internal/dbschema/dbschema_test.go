package dbschema

import (
	"errors"
	"strings"
	"testing"
)

const schemaFM = `title: Improvements backlog
tags: [p, type:index]
type: database
source: p/docs/improvements/
fields:
  id: {type: text, required: true}
  status: {type: select, required: true, options: [open, in-progress, done]}
  priority: {type: select, options: [low, medium, high]}
  created: {type: date}
  closed: {type: date}
  points: {type: number}
  blocked: {type: checkbox}
  labels: {type: multi-select, options: [ui, mcp]}
  link: {type: url}
  plan: {type: relation}
  aliases: {type: list}`

func mustParse(t *testing.T) *Schema {
	t.Helper()
	s, err := Parse("p/docs/improvements.md", schemaFM)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestParse(t *testing.T) {
	s := mustParse(t)
	if s.Source != "p/docs/improvements" {
		t.Errorf("source = %q, trailing slash should be trimmed", s.Source)
	}
	if got := strings.Join(s.FieldNames(), ","); got != "id,status,priority,created,closed,points,blocked,labels,link,plan,aliases" {
		t.Errorf("fields out of declaration order: %s", got)
	}
	if f, _ := s.Field("status"); !f.Required || len(f.Options) != 3 {
		t.Errorf("status = %+v", f)
	}
}

func TestParse_Errors(t *testing.T) {
	cases := map[string]string{
		"not a database":       "title: x\ntype: doc",
		"no source":            "type: database\nfields:\n  id: {type: text}",
		"fields as prose list": "type: database\nsource: p/x\nfields:\n  - \"id: text, obbligatorio\"",
		"unknown type":         "type: database\nsource: p/x\nfields:\n  id: {type: string}",
		"select without opts":  "type: database\nsource: p/x\nfields:\n  s: {type: select}",
		"invalid yaml":         "type: database\nsource: [unclosed",
	}
	for name, fm := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse("p/x.md", fm)
			if err == nil {
				t.Fatal("want an error")
			}
			if name == "not a database" && !errors.Is(err, ErrNotDatabase) {
				t.Errorf("want ErrNotDatabase, got %v", err)
			}
		})
	}
}

func TestCovers(t *testing.T) {
	s := mustParse(t)
	for rel, want := range map[string]bool{
		"p/docs/improvements/IMP-001.md":     true,
		"p/docs/improvements.md":             false, // the database note itself
		"p/docs/improvements/sub/IMP-002.md": false, // rows are direct children
		"p/docs/bugs/BUG-001.md":             false,
	} {
		if got := s.Covers(rel); got != want {
			t.Errorf("Covers(%s) = %v, want %v", rel, got, want)
		}
	}
}

// CheckEdit looks only at the keys an edit touches, with values as JSON
// decodes them.
func TestCheckEdit(t *testing.T) {
	s := mustParse(t)
	const rel = "p/docs/improvements/IMP-129.md"
	valid := map[string]any{
		"status": "done", "closed": "2026-10-03", "points": float64(3), "blocked": true,
		"labels": []any{"ui"}, "link": "https://example.com", "plan": "[[p/plans/x]]",
		"aliases": []any{"a"}, "title": "anything", "id": "IMP-129",
	}
	if probs := s.CheckEdit(rel, valid, []string{"priority"}); len(probs) != 0 {
		t.Fatalf("valid edit has problems: %+v", probs)
	}
	cases := []struct {
		name  string
		set   map[string]any
		unset []string
		field string
		want  string
	}{
		{"invented field", map[string]any{"resolved": "x"}, nil, "resolved", "not in the schema"},
		{"select off-list", map[string]any{"status": "fixed"}, nil, "status", "not one of"},
		{"required emptied", map[string]any{"status": ""}, nil, "status", "cannot be empty"},
		{"required removed", nil, []string{"status"}, "status", "cannot be removed"},
		{"bad date", map[string]any{"closed": "ieri"}, nil, "closed", "ISO date"},
		{"not a bool", map[string]any{"blocked": "yes"}, nil, "blocked", "true or false"},
		{"multi-select off-list", map[string]any{"labels": []any{"web"}}, nil, "labels", "not one of"},
		{"id vs file name", map[string]any{"id": "IMP-130"}, nil, "id", "does not match the file name"},
	}
	for _, tc := range cases {
		probs := s.CheckEdit(rel, tc.set, tc.unset)
		found := false
		for _, p := range probs {
			found = found || (p.Field == tc.field && strings.Contains(p.Message, tc.want))
		}
		if !found {
			t.Errorf("%s: want a problem on %q containing %q, got %+v", tc.name, tc.field, tc.want, probs)
		}
	}
}

func TestValidate(t *testing.T) {
	s := mustParse(t)
	const rel = "p/docs/improvements/IMP-129.md"
	valid := "title: \"IMP-129 — x\"\nid: IMP-129\nstatus: done\npriority: low\ncreated: 2026-10-02\n" +
		"closed: \"2026-10-03\"\npoints: 3\nblocked: false\nlabels: [ui]\nlink: https://example.com/x\n" +
		"plan: \"[[p/plans/x]]\"\naliases: [a, b]\ntags: [p, type:doc]"
	if probs := s.Validate(rel, valid); len(probs) != 0 {
		t.Fatalf("valid row has problems: %+v", probs)
	}
	cases := []struct {
		name, fm, field, want string
	}{
		{"invented field", "id: IMP-129\nstatus: done\nresolved: 2026-10-02", "resolved", "not in the schema"},
		{"required missing", "id: IMP-129", "status", "required field"},
		{"select off-list", "id: IMP-129\nstatus: fixed", "status", "not one of open, in-progress, done"},
		{"bad date", "id: IMP-129\nstatus: open\ncreated: ieri", "created", "ISO date"},
		{"id vs file name", "id: IMP-130\nstatus: open", "id", "does not match the file name"},
		{"not a number", "id: IMP-129\nstatus: open\npoints: many", "points", "not a number"},
		{"not a bool", "id: IMP-129\nstatus: open\nblocked: maybe", "blocked", "true or false"},
		{"multi-select off-list", "id: IMP-129\nstatus: open\nlabels: [ui, web]", "labels", "\"web\" is not one of"},
		{"not a url", "id: IMP-129\nstatus: open\nlink: example.com", "link", "http(s) URL"},
		{"not a wikilink", "id: IMP-129\nstatus: open\nplan: p/plans/x", "plan", "wikilink"},
		{"not a list", "id: IMP-129\nstatus: open\naliases: a", "aliases", "want a list"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			probs := s.Validate(rel, tc.fm)
			for _, p := range probs {
				if p.Field == tc.field && strings.Contains(p.Message, tc.want) {
					return
				}
			}
			t.Errorf("want a problem on %q containing %q, got %+v", tc.field, tc.want, probs)
		})
	}
}

func TestParse_Template(t *testing.T) {
	base := "type: database\nsource: p/docs/improvements\nfields:\n  id: {type: text}\ntemplate: "
	for in, want := range map[string]string{
		"p/templates/improvement":          "p/templates/improvement.md",
		"p/templates/improvement.md":       "p/templates/improvement.md",
		`"[[p/templates/improvement]]"`:    "p/templates/improvement.md",
		`"[[p/templates/improvement|x]]"`:  "p/templates/improvement.md",
		"/p/templates/../tpl/improvement/": "p/tpl/improvement.md",
	} {
		s, err := Parse("p/docs/improvements.md", base+in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if s.Template != want {
			t.Errorf("%s: template = %q, want %q", in, s.Template, want)
		}
	}
	for _, in := range []string{
		"q/templates/improvement",          // another project
		"improvement",                      // no project
		"p/../q/x",                         // escapes the project
		"p/docs/improvements/IMP-template", // a row
	} {
		if _, err := Parse("p/docs/improvements.md", base+in); err == nil {
			t.Errorf("%s: want an error", in)
		}
	}
	if s, _ := Parse("p/docs/improvements.md", strings.TrimSuffix(base, "\ntemplate: ")); s.Template != "" {
		t.Errorf("no template key: template = %q", s.Template)
	}
}

func TestNextName(t *testing.T) {
	for _, c := range []struct {
		names []string
		want  string
	}{
		{[]string{"IMP-001", "IMP-137", "IMP-002"}, "IMP-138"},
		{[]string{"T-009", "T-001"}, "T-010"},
		{[]string{"T-1", "T-9"}, "T-10"},
		{[]string{"IMP-1", "IMP-099"}, "IMP-100"},
		{[]string{"BUG-001", "IMP-004", "IMP-005", "notes"}, "IMP-006"}, // the most common prefix
		{[]string{"b-1", "a-1"}, "a-2"},                                 // a tie goes to the first prefix
		{[]string{"readme", "ideas"}, ""},
		{nil, ""},
	} {
		if got := NextName(c.names); got != c.want {
			t.Errorf("NextName(%v) = %q, want %q", c.names, got, c.want)
		}
	}
}

func TestOptionOrderOf(t *testing.T) {
	mk := func(fields string) *Schema {
		s, err := Parse("p/db.md", "type: database\nsource: p/x\nfields:\n"+fields)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	imp := mk("  status: {type: select, options: [open, done]}\n  priority: {type: select, options: [low, high]}\n")
	bug := mk("  status: {type: select, options: [open, done]}\n  severity: {type: select, options: [low, high]}\n")
	odd := mk("  status: {type: select, options: [done, open]}\n  priority: {type: text}\n")
	if got := OptionOrderOf([]*Schema{imp, bug}, "status"); strings.Join(got, ",") != "open,done" {
		t.Errorf("same options in both: %v", got)
	}
	if got := OptionOrderOf([]*Schema{imp, bug}, "priority"); strings.Join(got, ",") != "low,high" {
		t.Errorf("declared by one: %v", got)
	}
	if got := OptionOrderOf([]*Schema{imp, odd}, "status"); got != nil {
		t.Errorf("options that disagree: %v", got)
	}
	if got := OptionOrderOf([]*Schema{imp, odd}, "priority"); got != nil {
		t.Errorf("a select and a text: %v", got)
	}
	if got := OptionOrderOf([]*Schema{imp}, "missing"); got != nil {
		t.Errorf("undeclared: %v", got)
	}
}

// A row is read as the index reads it: as text typed by the schema, so an
// id written 007 matches the file 007.md, which a YAML number would not.
func TestValidate_ValuesAsText(t *testing.T) {
	s, err := Parse("p/db.md", "type: database\nsource: p/rows\nfields:\n  id: {type: text}\n  done: {type: checkbox}\n  points: {type: number}\n  due: {type: date}\n")
	if err != nil {
		t.Fatal(err)
	}
	if probs := s.Validate("p/rows/007.md", "id: 007\ndone: true\npoints: 1.10\ndue: 2026-10-04\n"); len(probs) != 0 {
		t.Errorf("problems = %+v", probs)
	}
	if probs := s.Validate("p/rows/008.md", "id: 008\ndone: yes\n"); len(probs) != 1 || probs[0].Field != "done" {
		t.Errorf("yes is not a checkbox value: %+v", probs)
	}
}

// row_views: each entry's title, and the rest as the spec of a view;
// the shape is checked when the schema is read, the spec when it runs.
func TestParse_RowViews(t *testing.T) {
	s, err := Parse("p/docs/improvements.md", schemaFM+"\nrow_views:\n  - title: Plans\n    from: p/plans\n    where: [implements_imp contains this.id]\n  - {title: Links, from: p/plans, where: [links contains this], as: list}\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.RowViews) != 2 || s.RowViews[0].Title != "Plans" || s.RowViews[1].Title != "Links" {
		t.Fatalf("row views = %+v", s.RowViews)
	}
	if spec := s.RowViews[0].Spec; strings.Contains(spec, "title") || !strings.Contains(spec, "from: p/plans") || !strings.Contains(spec, "implements_imp contains this.id") {
		t.Errorf("spec = %q", spec)
	}
	if s, err := Parse("p/docs/improvements.md", schemaFM+"\nrow_views:\n"); err != nil || s.RowViews != nil {
		t.Errorf("an empty row_views: %+v, %v", s, err)
	}
	many := "\nrow_views:\n" + strings.Repeat("  - {title: x, from: p/plans}\n", MaxRowViews+1)
	for name, extra := range map[string]string{
		"not a list": "\nrow_views: {title: x}",
		"not a map":  "\nrow_views: [x]",
		"no title":   "\nrow_views:\n  - from: p/plans",
		"no from":    "\nrow_views:\n  - title: x",
		"too many":   many,
	} {
		if _, err := Parse("p/docs/improvements.md", schemaFM+extra); err == nil || !strings.Contains(err.Error(), "row") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
