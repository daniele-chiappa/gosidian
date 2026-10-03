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
