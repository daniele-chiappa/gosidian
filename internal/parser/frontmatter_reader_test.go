package parser

import (
	"reflect"
	"testing"
)

// A frontmatter that is valid YAML is read with yaml.Node, its values kept as
// the text written (IMP-138).
func TestFrontmatterEntries_YAML(t *testing.T) {
	raw := "title: \"Plan: one\"\ncode: 007\nratio: 1.10\nupdated: 2026-10-04\nempty:\nblank: \"\"\n" +
		"tags: [p, \"#x\", type:doc]\naliases: [\"a, b\", c]\nrelated: [[p/note]]\nlinks: [[[p/a]], [[p/b|B]]]\n" +
		"status: open # a comment\nsummary: |\n  two\n  lines\nharness:\n  name: dev\n  tools: [Read, Edit]\n" +
		"base: &b shared\ncopy: *b\n"
	got := map[string]FMEntry{}
	var order []string
	for _, e := range FrontmatterEntries(raw) {
		got[e.Key] = e
		order = append(order, e.Key)
	}
	want := []string{"title", "code", "ratio", "updated", "empty", "blank", "tags", "aliases", "related", "links", "status", "summary", "harness", "base", "copy"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v", order)
	}
	for key, w := range map[string]FMEntry{
		"title":   {Key: "title", Kind: FMScalar, Text: "Plan: one"},
		"code":    {Key: "code", Kind: FMScalar, Text: "007"},
		"ratio":   {Key: "ratio", Kind: FMScalar, Text: "1.10"},
		"updated": {Key: "updated", Kind: FMScalar, Text: "2026-10-04"},
		"empty":   {Key: "empty"},
		"blank":   {Key: "blank", Kind: FMScalar},
		"tags":    {Key: "tags", Kind: FMList, Items: []string{"p", "#x", "type:doc"}},
		"aliases": {Key: "aliases", Kind: FMList, Items: []string{"a, b", "c"}},
		"related": {Key: "related", Kind: FMScalar, Text: "[[p/note]]"},
		"links":   {Key: "links", Kind: FMList, Items: []string{"[[p/a]]", "[[p/b|B]]"}},
		"status":  {Key: "status", Kind: FMScalar, Text: "open", Comment: "# a comment"},
		"summary": {Key: "summary", Kind: FMScalar, Text: "two\nlines\n"},
		"harness": {Key: "harness", Kind: FMMap, Sub: map[string]any{"name": "dev", "tools": []string{"Read", "Edit"}}},
		"copy":    {Key: "copy", Kind: FMScalar, Text: "shared"},
	} {
		if !reflect.DeepEqual(got[key], w) {
			t.Errorf("%s = %+v, want %+v", key, got[key], w)
		}
	}
	if cut := CutByComment(raw); !reflect.DeepEqual(cut, []string{"status: open"}) {
		t.Errorf("CutByComment = %v", cut)
	}
}

// What the old callers get from the YAML reader: scalars as text, tags and
// other lists as []string, tags trimmed of a leading # as always.
func TestFrontmatterAdapters_YAML(t *testing.T) {
	raw := "title: \"Plan: one\"\ntags: [p, \"#x\"]\nimplements_imp: [IMP-1, IMP-2]\ncsv: a, b\nharness:\n  tools: [Read]\nimportance: 5\n"
	fields := ParseFrontmatterFields(raw)
	if fields["title"] != "Plan: one" || !reflect.DeepEqual(fields["tags"], []string{"p", "x"}) ||
		!reflect.DeepEqual(fields["implements_imp"], []string{"IMP-1", "IMP-2"}) || fields["csv"] != "a, b" {
		t.Errorf("ParseFrontmatterFields = %#v", fields)
	}
	if _, ok := fields["harness"]; ok {
		t.Error("a nested map is not a field")
	}
	if got := FrontmatterList(raw, "csv"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("a scalar read as a list splits on commas, as tags: a, b always did: %v", got)
	}
	if got := ExtractFrontmatterBlock(raw, "harness"); !reflect.DeepEqual(got, map[string]any{"tools": []string{"Read"}}) {
		t.Errorf("ExtractFrontmatterBlock = %#v", got)
	}
	if !HasFrontmatterKey(raw, "harness") || HasFrontmatterKey(raw, "tools") || frontmatterTitle(raw) != "Plan: one" || Importance(raw) != 5 {
		t.Error("HasFrontmatterKey, frontmatterTitle or Importance")
	}
}

// A frontmatter that is not valid YAML is read line by line, as before, so a
// quoting slip does not hide a note.
func TestFrontmatterEntries_FallbackOnInvalidYAML(t *testing.T) {
	raw := "title: Plan: one\ntags: [p, type:plan]\nimplements_imp:\n  - IMP-1\nstatus: draft\n"
	if _, ok := yamlEntries(raw); ok {
		t.Fatal("the raw should not parse as YAML")
	}
	got := map[string]FMEntry{}
	for _, e := range FrontmatterEntries(raw) {
		got[e.Key] = e
	}
	if got["title"].Text != "Plan: one" || !reflect.DeepEqual(got["tags"].Items, []string{"p", "type:plan"}) ||
		!reflect.DeepEqual(got["implements_imp"].Items, []string{"IMP-1"}) || got["status"].Text != "draft" {
		t.Errorf("fallback = %+v", got)
	}
	if fields := ParseFrontmatterFields(raw); fields["title"] != "Plan: one" {
		t.Errorf("ParseFrontmatterFields fallback = %#v", fields)
	}
	// A key written twice is not valid YAML either: the first one counts.
	if es := FrontmatterEntries("status: open\nstatus: done\n"); len(es) != 1 || es[0].Text != "open" {
		t.Errorf("duplicate key = %+v", es)
	}
}

// The wikilinks of the frontmatter's top-level values are links, with the
// key they are written under (IMP-127 iteration 2); tags, nested maps and
// links in inline code are not.
func TestFrontmatterLinks(t *testing.T) {
	raw := "title: Row\ntags: [p, \"[[not/a/link]]\"]\nrelated: [\"[[p/a|A]]\", \"[[p/b#Heading]]\"]\n" +
		"origin: [[p/c]]\nlinks: [[[p/d]], [[p/e]]]\ndescription: \"see `[[p/code]]` and [[p/f]]\"\n" +
		"harness:\n  link: \"[[p/nested]]\"\nstatus: open\n"
	var got []string
	for _, l := range FrontmatterLinks(raw) {
		got = append(got, l.Field+">"+l.Target+"|"+l.Alias)
	}
	want := []string{"related>p/a|A", "related>p/b#Heading|", "origin>p/c|", "links>p/d|", "links>p/e|", "description>p/f|"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FrontmatterLinks = %v, want %v", got, want)
	}
	// The line reader's fallback finds them too.
	if ls := FrontmatterLinks("title: Plan: one\nrelated: \"[[p/a]]\"\n"); len(ls) != 1 || ls[0].Field != "related" || ls[0].Target != "p/a" {
		t.Errorf("fallback = %+v", ls)
	}
}
