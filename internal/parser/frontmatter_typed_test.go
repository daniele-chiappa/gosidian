package parser

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TypedFrontmatter gives the values with the types YAML gives them, maps
// and lists nested as written, and keeps what the vault means by title,
// tags, an unquoted wikilink, a date and a code with leading zeros
// (IMP-198).
func TestTypedFrontmatter(t *testing.T) {
	raw := "title: 2026\ndraft: true\noff: false\ncount: 3\nneg: -12\nratio: 0.5\nexp: 1e3\nepoch: 1792237963\n" +
		"code: 007\nzip: 00189\nhex: 0x1F\ncreated: 2026-10-17\nat: 2026-10-17T10:00:00Z\nyes: yes\nquoted: \"true\"\nnum: \"3\"\n" +
		"inf: .inf\nnan: .nan\nempty:\nblank: \"\"\ntags: [doc, \"#rev\", type:doc]\nlist: [1, two, false, ~]\n" +
		"revisioni:\n  V1: 1792237963\n  V2: 1792241563\nnested:\n  deep: {ok: true, n: [1, 2.5], none: }\n  links: [[[p/a]], \"[[p/b]]\"]\n" +
		"related: [[p/other]]\nlinks:\n  - [[p/a|A]]\n  - \"[[p/b]]\"\nsummary: |\n  two\n  lines\nbase: &b {k: 1}\ncopy: *b\ndup: 1\ndup: 2\n"
	got := TypedFrontmatter(raw)
	want := map[string]any{
		"title": "2026", "draft": true, "off": false, "count": 3, "neg": -12, "ratio": 0.5, "exp": 1000.0, "epoch": 1792237963,
		"code": "007", "zip": "00189", "hex": 31, "created": "2026-10-17", "at": "2026-10-17T10:00:00Z", "yes": "yes",
		"quoted": "true", "num": "3", "inf": ".inf", "nan": ".nan", "blank": "",
		"tags":      []any{"doc", "rev", "type:doc"},
		"list":      []any{1, "two", false, nil},
		"revisioni": map[string]any{"V1": 1792237963, "V2": 1792241563},
		"nested": map[string]any{
			"deep":  map[string]any{"ok": true, "n": []any{1, 2.5}, "none": nil},
			"links": []any{"[[p/a]]", "[[p/b]]"},
		},
		"related": "[[p/other]]",
		"links":   []any{"[[p/a|A]]", "[[p/b]]"},
		"summary": "two\nlines\n",
		"base":    map[string]any{"k": 1},
		"copy":    map[string]any{"k": 1},
		"dup":     1,
	}
	if !reflect.DeepEqual(got, want) {
		for k, w := range want {
			if !reflect.DeepEqual(got[k], w) {
				t.Errorf("%s = %#v, want %#v", k, got[k], w)
			}
		}
		for k := range got {
			if _, ok := want[k]; !ok {
				t.Errorf("%s = %#v, not wanted", k, got[k])
			}
		}
	}
	if _, err := json.Marshal(got); err != nil {
		t.Errorf("not JSON: %v", err)
	}
}

// A frontmatter that is not valid YAML is read line by line, as text; an
// empty one is an empty map.
func TestTypedFrontmatter_Fallbacks(t *testing.T) {
	if got := TypedFrontmatter(""); got == nil || len(got) != 0 {
		t.Errorf("empty = %#v", got)
	}
	got := TypedFrontmatter("title: Plan: one\ncount: 3\ntags: [a, b]\n")
	if got["title"] != "Plan: one" || got["count"] != "3" || !reflect.DeepEqual(got["tags"], []string{"a", "b"}) {
		t.Errorf("invalid YAML = %#v", got)
	}
}
