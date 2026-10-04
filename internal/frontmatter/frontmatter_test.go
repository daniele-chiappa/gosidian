package frontmatter

import (
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/gosidian/gosidian/internal/parser"
)

func setKeys(t *testing.T, in string, set []Field, unset []string) string {
	t.Helper()
	out, err := SetKeys([]byte(in), set, unset)
	if err != nil {
		t.Fatalf("SetKeys: %v", err)
	}
	return string(out)
}

func TestSetKeys_ReplaceKeepsTheRest(t *testing.T) {
	in := "---\ntitle: \"Plan: one\"\n# a comment\nstatus: open   \n\ntags: [a, b]\n---\n\n# Body\nstatus: not frontmatter\n"
	got := setKeys(t, in, []Field{{"status", "done"}}, nil)
	want := "---\ntitle: \"Plan: one\"\n# a comment\nstatus: done\n\ntags: [a, b]\n---\n\n# Body\nstatus: not frontmatter\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestSetKeys_NewKeyGoesLast(t *testing.T) {
	got := setKeys(t, "---\ntitle: T\n---\nbody\n", []Field{{"priority", "high"}, {"closed", "2026-10-03"}}, nil)
	if want := "---\ntitle: T\npriority: high\nclosed: 2026-10-03\n---\nbody\n"; got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// A new key goes before tags when tags is the last key, as the vault's
// notes are written; otherwise last.
func TestSetKeys_NewKeyBeforeTrailingTags(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"tags last", "---\ntitle: T\ntags: [a]\n---\n", "---\ntitle: T\nstatus: open\ntags: [a]\n---\n"},
		{"block tags last", "---\ntitle: T\ntags:\n  - a\n---\n", "---\ntitle: T\nstatus: open\ntags:\n  - a\n---\n"},
		{"tags in the middle", "---\ntags: [a]\ntitle: T\n---\n", "---\ntags: [a]\ntitle: T\nstatus: open\n---\n"},
	} {
		if got := setKeys(t, c.in, []Field{{"status", "open"}}, nil); got != c.want {
			t.Errorf("%s: got\n%s\nwant\n%s", c.name, got, c.want)
		}
	}
	// Tags set in the same call stays where it is, after the new key.
	if got := setKeys(t, "---\ntitle: T\ntags: [a]\n---\n", []Field{{"tags", []string{"b"}}, {"status", "open"}}, nil); got != "---\ntitle: T\nstatus: open\ntags: [b]\n---\n" {
		t.Errorf("tags set too: got\n%s", got)
	}
	// Tags removed: the new key goes last.
	if got := setKeys(t, "---\ntitle: T\ntags: [a]\n---\n", []Field{{"status", "open"}}, []string{"tags"}); got != "---\ntitle: T\nstatus: open\n---\n" {
		t.Errorf("tags removed: got\n%s", got)
	}
}

func TestSetKeys_Unset(t *testing.T) {
	in := "---\ntitle: T\nstatus: open\nimplements:\n  - IMP-1\n  - IMP-2\nfields:\n  a: {type: text}\n---\n"
	got := setKeys(t, in, nil, []string{"status", "implements", "fields", "absent"})
	if want := "---\ntitle: T\n---\n"; got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestSetKeys_BlockListBecomesInline(t *testing.T) {
	in := "---\nimplements:\n  - IMP-1\n  - IMP-2\nnext: x\n---\n"
	got := setKeys(t, in, []Field{{"implements", []string{"IMP-3"}}}, nil)
	if want := "---\nimplements: [IMP-3]\nnext: x\n---\n"; got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestSetKeys_NoFrontmatter(t *testing.T) {
	got := setKeys(t, "# Title\n\ntext\n", []Field{{"status", "open"}}, nil)
	if want := "---\nstatus: open\n---\n# Title\n\ntext\n"; got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if got := setKeys(t, "# Title\n", nil, []string{"status"}); got != "# Title\n" {
		t.Errorf("removing from a note without frontmatter changed it: %q", got)
	}
}

func TestSetKeys_CRLF(t *testing.T) {
	in := "---\r\ntitle: T\r\nstatus: open\r\n---\r\nbody\r\n"
	got := setKeys(t, in, []Field{{"status", "done"}, {"priority", "low"}}, nil)
	if want := "---\r\ntitle: T\r\nstatus: done\r\npriority: low\r\n---\r\nbody\r\n"; got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if got := setKeys(t, "body\r\n", []Field{{"status", "open"}}, nil); got != "---\r\nstatus: open\r\n---\r\nbody\r\n" {
		t.Errorf("new frontmatter in a CRLF note: %q", got)
	}
}

func TestSetKeys_Unterminated(t *testing.T) {
	_, err := SetKeys([]byte("---\ntitle: T\nbody\n"), []Field{{"status", "open"}}, nil)
	if !errors.Is(err, ErrUnterminated) {
		t.Errorf("err = %v, want ErrUnterminated", err)
	}
}

// A value whose YAML spans lines in a way a one-line edit would damage is
// refused, and the note is left alone.
func TestSetKeys_RefusesComplexValues(t *testing.T) {
	cases := map[string]string{
		"nested map":          "note:\n  a: 1\n",
		"block scalar":        "note: |\n  text\n",
		"multi-line flow":     "note: [a,\n  b]\n",
		"anchor":              "note: &base x\n",
		"duplicate key":       "note: a\nnote: b\n",
		"value past a blank":  "note:\n  - a\n\n  - b\n",
		"map in a block list": "note:\n  - a: 1\n",
		"comment in a list":   "note:\n  - a\n  # why\n  - b\n",
	}
	for name, fm := range cases {
		_, err := SetKeys([]byte("---\n"+fm+"---\n"), []Field{{"note", "x"}}, nil)
		var ce *ComplexError
		if !errors.As(err, &ce) || ce.Key != "note" {
			t.Errorf("%s: err = %v, want a ComplexError on note", name, err)
		}
	}
	for _, fm := range []string{"note: &base x\n", "note: a\nnote: b\n", "note:\n  - a\n\n  - b\n"} {
		if _, err := SetKeys([]byte("---\n"+fm+"---\n"), nil, []string{"note"}); err == nil {
			t.Errorf("removing note from %q: want an error", fm)
		}
	}
}

func TestSetKeys_Encoding(t *testing.T) {
	cases := []struct {
		value any
		line  string
	}{
		{"done", `k: done`},
		{"Iterazione 2 — editor", `k: Iterazione 2 — editor`},
		{"Iterazione 2: editor", `k: "Iterazione 2: editor"`},
		{`Sync v1.12 "Agent workflow"`, `k: Sync v1.12 "Agent workflow"`},
		{`back\slash`, `k: back\slash`},
		{"true", `k: "true"`},
		{"007", `k: "007"`},
		{"1.10", `k: "1.10"`},
		{"2026-10-03", `k: 2026-10-03`},
		{"[draft] x", `k: "[draft] x"`},
		{"[[nota]]", `k: "[[nota]]"`},
		{"#hash", `k: "#hash"`},
		{"a #b", `k: "a #b"`},
		{" lead", `k: " lead"`},
		{"", `k: ""`},
		{"one\ntwo", `k: "one\ntwo"`},
		{`"quoted" text`, `k: "\"quoted\" text"`},
		{true, `k: true`},
		{float64(3), `k: 3`},
		{2.5, `k: 2.5`},
		{7, `k: 7`},
		{nil, `k:`},
		{[]string{"a", "b"}, `k: [a, b]`},
		{[]string{}, `k: []`},
		{[]string{"type:doc", "IMP-1"}, `k: [type:doc, IMP-1]`},
		{[]string{"[[a]]", "[[b]]"}, `k: ["[[a]]", "[[b]]"]`},
		{[]string{"a, b", "c"}, `k: ["a, b", c]`}, // one reader: the comma stays inside the item
		{[]string{`say "hi" now`, `a\b`}, `k: ["say \"hi\" now", "a\\b"]`},
		{[]any{"x", float64(2), true}, `k: [x, "2", "true"]`},
	}
	for _, c := range cases {
		got := setKeys(t, "---\n---\n", []Field{{"k", c.value}}, nil)
		if want := "---\n" + c.line + "\n---\n"; got != want {
			t.Errorf("%#v: got %q, want %q", c.value, got, want)
		}
	}
}

func TestSetKeys_RefusesValues(t *testing.T) {
	cases := map[string][]Field{
		"list item with a quote at an end": {{"k", []string{`say "hi"`}}},
		"list item with #":                 {{"k", []string{"#x"}}},
		"list item with spaces":            {{"k", []string{" a"}}},
		"list item with quotes":            {{"k", []string{`say "hi"`}}},
		"map value":                        {{"k", map[string]any{"a": 1}}},
		"not a number":                     {{"k", math.NaN()}},
		"invalid key":                      {{"bad key", "x"}},
		"key set twice":                    {{"k", "a"}, {"k", "b"}},
	}
	for name, set := range cases {
		_, err := SetKeys([]byte("---\ntitle: T\n---\n"), set, nil)
		var ve *ValueError
		if !errors.As(err, &ve) {
			t.Errorf("%s: err = %v, want a ValueError", name, err)
		}
	}
	if _, err := SetKeys([]byte("---\nk: a\n---\n"), []Field{{"k", "b"}}, []string{"k"}); err == nil {
		t.Error("a key both set and removed: want an error")
	}
}

// What SetKeys writes reads back the same through the line-based reader the
// index uses and through yaml.v3, in a real note.
func TestSetKeys_ReadBack(t *testing.T) {
	in := "---\ntitle: Old\nstatus: open\ntags: [p, type:doc]\n---\n\n# Note\n"
	set := []Field{
		{"title", "Iterazione 2: editor \"3D\""},
		{"status", "done"},
		{"closed", "2026-10-03"},
		{"related", []string{"[[p/a]]", "[[p/b]]"}},
		{"tags", []string{"p", "type:doc", "topic:views"}},
		{"done", true},
		{"estimate", 1.5},
	}
	out := setKeys(t, in, set, nil)
	raw := parser.FrontmatterRawForPath("p/n.md", []byte(out))
	fields := parser.ParseFrontmatterFields(raw)
	for k, want := range map[string]string{"title": `Iterazione 2: editor "3D"`, "status": "done", "closed": "2026-10-03", "done": "true", "estimate": "1.5"} {
		if fields[k] != want {
			t.Errorf("line reader %s = %#v, want %q", k, fields[k], want)
		}
	}
	if got := parser.FrontmatterList(raw, "related"); !reflect.DeepEqual(got, []string{"[[p/a]]", "[[p/b]]"}) {
		t.Errorf("line reader related = %v", got)
	}
	if got := fields["tags"]; !reflect.DeepEqual(got, []string{"p", "type:doc", "topic:views"}) {
		t.Errorf("line reader tags = %v", got)
	}
	var y map[string]any
	if err := yaml.Unmarshal([]byte(raw), &y); err != nil {
		t.Fatalf("yaml.v3: %v\n%s", err, raw)
	}
	if y["title"] != `Iterazione 2: editor "3D"` || y["done"] != true || y["estimate"] != 1.5 {
		t.Errorf("yaml.v3 read %v", y)
	}
	if !strings.HasSuffix(out, "---\n\n# Note\n") {
		t.Errorf("body changed:\n%s", out)
	}
}

// YAMLError names the line of the note, counting the opening --- as 1.
func TestYAMLError(t *testing.T) {
	if err := YAMLError("title: ok\ntags: [a, b]\n"); err != nil {
		t.Errorf("valid YAML: %v", err)
	}
	err := YAMLError("title: ok\ndescription: Plan: one\n")
	if err == nil || !strings.HasPrefix(err.Error(), "line 3 of the note: ") {
		t.Errorf("a value with \": \": %v", err)
	}
	if err := YAMLError("related: [[p/note]]\n"); err != nil {
		t.Logf("an unquoted wikilink parses as a nested list in YAML: %v", err)
	}
	if err := YAMLError("tags: [{{PROJECT}}, type:doc]\n"); err == nil {
		t.Error("an unquoted {{placeholder}} in a flow list is not valid YAML")
	}
}

// A list item with a comma needs a frontmatter that is valid YAML: otherwise
// the note is read line by line, which splits the item.
func TestSetKeys_CommaItemNeedsValidYAML(t *testing.T) {
	if _, err := SetKeys([]byte("---\ntitle: ok\n---\n"), []Field{{"k", []string{"a, b"}}}, nil); err != nil {
		t.Errorf("valid YAML: %v", err)
	}
	_, err := SetKeys([]byte("---\ntitle: Plan: one\n---\n"), []Field{{"k", []string{"a, b"}}}, nil)
	var ve *ValueError
	if !errors.As(err, &ve) || ve.Key != "k" {
		t.Errorf("invalid YAML elsewhere: err = %v", err)
	}
}
