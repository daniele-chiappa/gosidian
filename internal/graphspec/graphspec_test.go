package graphspec

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	s, problems := Parse("source: vec-pg/chunks\nwith: {collection: procedure, k: 8}\ncolor: doc\nmode: 3d\n")
	if len(problems) != 0 || s.Source != "vec-pg/chunks" || s.With["collection"] != "procedure" || s.Mode != "3d" || s.Color != "doc" {
		t.Fatalf("%+v %v", s, problems)
	}
	if p, g, ok := s.Plugin(); !ok || p != "vec-pg" || g != "chunks" {
		t.Errorf("plugin %s %s %v", p, g, ok)
	}
	s, problems = Parse("project: gosidian\ntag: adr\ndepth: 2\n")
	if len(problems) != 0 || s.Source != SourceLinks || s.Tag != "adr" {
		t.Errorf("links: %+v %v", s, problems)
	}
	if s, problems := Parse(""); len(problems) != 0 || s.Source != SourceLinks {
		t.Errorf("empty: %+v %v", s, problems)
	}
	for src, want := range map[string]string{
		"source: Vec/x":                   "source",
		"source: vec-pg/chunks\ntag: adr": "filters the links",
		"with: {k: 1}":                    "for a plugin's graph",
		"mode: 4d":                        "2d or 3d",
		"depth: 9":                        "at most 6",
		"limit: x":                        "whole number",
		"colour: doc":                     "no field",
		"source: vec-pg/chunks\nwith: 3":  "map of the source's arguments",
		"- a\n- b":                        "map of fields",
		"source: [a":                      "does not parse",
	} {
		_, problems := Parse(src)
		found := false
		for _, p := range problems {
			if strings.Contains(p.Msg, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: want a problem with %q, got %v", src, want, problems)
		}
	}
	_, problems = Parse("source: links\n\ncolour: doc\n")
	if len(problems) != 1 || problems[0].Line != 3 {
		t.Errorf("line of the problem: %v", problems)
	}
}

func TestFind(t *testing.T) {
	body := "---\ntype: graph\n---\n\n# Mappa\n\n```graph\nsource: vec-pg/docs\n```\n\ntext\n\n```graph\n```\n"
	blocks := Find([]byte(body))
	if len(blocks) != 2 || blocks[0].Line != 7 || !strings.Contains(blocks[0].Src, "vec-pg/docs") {
		t.Fatalf("%+v", blocks)
	}
}
