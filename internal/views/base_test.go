package views

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/parser"
)

const booksBase = `filters:
  and:
    - file.inFolder("Books")
    - 'status != "done"'
properties:
  status:
    displayName: Status
views:
  - type: table
    name: Reading
    order: [file.name, note.author, status, formula.ppu]
    sort:
      - property: note.year
        direction: DESC
      - property: file.name
        direction: ASC
    limit: 20
  - type: cards
    name: Shelf
    filters:
      or:
        - file.hasTag("novel")
        - file.hasTag("essay")
  - type: list
    name: Linked
    filters: 'file.hasLink(this.file) && !status.isEmpty()'
`

func baseViews(t *testing.T, rel, src string) *Base {
	t.Helper()
	b, err := TranslateBase(rel, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Each view of a base becomes a view block: the folder from, the filters
// where, order the columns, the sort keys, the limit; what has no
// equivalent is a warning, not a guess.
func TestTranslateBase(t *testing.T) {
	b := baseViews(t, "p/books.base", booksBase)
	if len(b.Views) != 3 || len(b.Warnings) != 1 || !strings.Contains(b.Warnings[0], "display names") {
		t.Fatalf("base = %+v", b)
	}
	want := []struct {
		name, spec string
		warns      []string
	}{
		{"Reading", "from: p/Books\nwhere:\n    - status != done\nsort: year desc, title asc\ncolumns:\n    - title\n    - author\n    - status\nlimit: 20\n",
			[]string{"the column formula.ppu has no equivalent"}},
		{"Shelf", "from: p/Books\nwhere:\n    - status != done\n    - tags in [novel, essay]\n",
			[]string{"a cards view has no equivalent: shown as a table"}},
		{"Linked", "from: p/Books\nwhere:\n    - status != done\n    - links contains this\n    - status exists\nas: list\n",
			[]string{"file.hasLink(this) names the base itself"}},
	}
	for i, w := range want {
		v := b.Views[i]
		if v.Name != w.name || v.Spec != w.spec {
			t.Errorf("view %d = %q\n%s\nwant %q\n%s", i, v.Name, v.Spec, w.name, w.spec)
		}
		if len(v.Warnings) != len(w.warns) {
			t.Errorf("view %d warnings = %q, want %q", i, v.Warnings, w.warns)
			continue
		}
		for j, ww := range w.warns {
			if !strings.Contains(v.Warnings[j], ww) {
				t.Errorf("view %d warning %d = %q, want %q", i, j, v.Warnings[j], ww)
			}
		}
		if _, err := Parse(v.Spec, Context{This: ThisFields("p/books.base", nil)}); err != nil {
			t.Errorf("view %d does not parse: %v\n%s", i, err, v.Spec)
		}
	}
}

// The sort keeps its keys up to the first without an equivalent, and at
// most four (IMP-143).
func TestTranslateBase_Sort(t *testing.T) {
	key := func(p, d string) string { return "      - property: " + p + "\n        direction: " + d + "\n" }
	for _, tc := range []struct{ keys, sort, warn string }{
		{key("note.year", "DESC") + key("formula.x", "ASC") + key("file.name", "ASC"), "sort: year desc\n", "the sort stops before it (formula.x, file.name ignored)"},
		{key("formula.x", "ASC") + key("file.name", "ASC"), "", "sorting by formula.x has no equivalent: the view keeps its default order"},
		{key("a", "ASC") + key("b", "DESC") + key("c", "ASC") + key("d", "ASC") + key("e", "ASC"), "sort: a asc, b desc, c asc, d asc\n", "at most 4 keys: e ignored"},
	} {
		src := "views:\n  - type: table\n    name: V\n    sort:\n" + tc.keys
		v := baseViews(t, "p/x.base", src).Views[0]
		if tc.sort != "" && !strings.Contains(v.Spec, tc.sort) || tc.sort == "" && strings.Contains(v.Spec, "sort:") {
			t.Errorf("spec:\n%s\nwant %q", v.Spec, tc.sort)
		}
		if len(v.Warnings) != 1 || !strings.Contains(v.Warnings[0], tc.warn) {
			t.Errorf("warnings = %q, want %q", v.Warnings, tc.warn)
		}
	}
}

// Filters with no equivalent are said and left out; those always true of a
// note go silently.
func TestTranslateBase_Filters(t *testing.T) {
	cases := []struct {
		filters string
		where   string // the where of the view, "" for none
		warn    string // a warning that must be there, "" for none
		from    string
	}{
		{`'file.ext == "md"'`, "", "", "p"},
		{`'file.mtime > now() - "1 week"'`, "", `the filter "file.mtime > now() - \"1 week\"" has no equivalent`, "p"},
		{"{or: ['status == \"a\"', 'priority == \"b\"']}", "", "or: status ==", "p"},
		{"{or: ['status == \"a\"', \"status == 'b'\"]}", "    - status in [a, b]\n", "", "p"},
		{"{not: ['status == \"a\"', 'file.hasTag(\"x\")', 'title.contains(\"y\")']}", "    - status != a\n    - tags != x\n", `not: "title.contains(\"y\")"`, "p"},
		{"{or: ['file.inFolder(\"A\")', 'file.inFolder(\"p/B\")']}", "", "", "\n    - p/A\n    - p/B"},
		{"{and: ['file.inFolder(\"A\")', 'file.inFolder(\"A/sub\")']}", "", "", "p/A/sub"},
		{"{and: ['file.inFolder(\"A\")', 'file.inFolder(\"B\")']}", "", `file.inFolder("p/B") with another folder`, "p/A"},
		{`'price >= 2.5 && (done == true)'`, "    - price >= 2.5\n    - done = true\n", "", "p"},
		{`'note.tags.contains("#todo")'`, "    - tags = todo\n", "", "p"},
		{`'file.hasLink("Some note")'`, "    - links contains [[Some note]]\n", "", "p"},
		{`'file.hasProperty("due")'`, "    - due exists\n", "", "p"},
		{`'author == "Le Guin, Ursula"'`, "    - field: author\n      op: eq\n      value: Le Guin, Ursula\n", "", "p"},
		{`'file.name == "x"'`, "", "has no equivalent", "p"},
		{`'status == "a" || priority == "b" && done == true'`, "", "has no equivalent", "p"},
	}
	for _, c := range cases {
		b := baseViews(t, "p/x.base", "filters: "+c.filters+"\n")
		v := b.Views[0]
		if v.Name != "Table" {
			t.Errorf("%s: a base without views has one table, got %q", c.filters, v.Name)
		}
		from := "from: " + c.from + "\n"
		if strings.HasPrefix(c.from, "\n") {
			from = "from:" + c.from + "\n"
		}
		if !strings.HasPrefix(v.Spec, from) {
			t.Errorf("%s: from in\n%s\nwant %q", c.filters, v.Spec, c.from)
		}
		where := ""
		if i := strings.Index(v.Spec, "where:\n"); i >= 0 {
			where = v.Spec[i+len("where:\n"):]
		}
		if where != c.where {
			t.Errorf("%s: where\n%q\nwant\n%q", c.filters, where, c.where)
		}
		joined := strings.Join(v.Warnings, "\n")
		if c.warn == "" && joined != "" || !strings.Contains(joined, c.warn) {
			t.Errorf("%s: warnings %q, want %q", c.filters, v.Warnings, c.warn)
		}
		resolveAny := func(string) string { return "p/some-note.md" }
		if _, err := Parse(v.Spec, Context{This: ThisFields("p/x.base", nil), Resolve: resolveAny}); err != nil {
			t.Errorf("%s: does not parse: %v\n%s", c.filters, err, v.Spec)
		}
	}
}

// The note a base reads as: its views computed like any other, this the
// base; a file that does not parse says why, with its text.
func TestBaseMarkdown(t *testing.T) {
	idx, err := index.Open(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	for p, body := range map[string]string{
		"p/Books/dune.md":      "---\ntitle: Dune\nauthor: Herbert\nyear: 1965\nstatus: reading\ntags: [novel]\n---\n\nSee [[p/books.base]].\n",
		"p/Books/walden.md":    "---\ntitle: Walden\nauthor: Thoreau\nyear: 1854\nstatus: reading\ntags: [essay]\n---\n",
		"p/Books/odyssey.md":   "---\ntitle: Odyssey\nauthor: Homer\nyear: -700\nstatus: done\ntags: [novel]\n---\n",
		"p/Other/elsewhere.md": "---\ntitle: Elsewhere\nstatus: reading\n---\n",
	} {
		fm := parser.ParseFrontmatterFields(parser.ExtractFrontmatterRaw([]byte(body)))
		if err := idx.Upsert(index.NoteDoc{Path: p, Title: fm["title"].(string), Body: body, ModTime: 1, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
	}
	md := BaseMarkdown("p/books.base", []byte(booksBase))
	c := Context{This: ThisFields("p/books.base", nil), Resolve: idx.Resolve}
	out, _ := RenderNote(md, true, c, idx.Query)
	s := string(out)
	for _, want := range []string{
		"# books\n", "> Obsidian base `p/books.base`, read-only", "- ⚠️ display names have no equivalent",
		"## Reading", "- ⚠️ the column formula.ppu has no equivalent", "| title | author | status |",
		"## Shelf", "## Linked",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("lacks %q:\n%s", want, s)
		}
	}
	reading := s[strings.Index(s, "## Reading"):strings.Index(s, "## Shelf")]
	if strings.Index(reading, "Dune") > strings.Index(reading, "Walden") || strings.Contains(reading, "Odyssey") || strings.Contains(reading, "Elsewhere") {
		t.Errorf("Reading: the books not done, newest first:\n%s", reading)
	}
	linked := s[strings.Index(s, "## Linked"):]
	if !strings.Contains(linked, "- ⚠️ file.hasLink(this) names the base itself") || !strings.Contains(linked, "_No matching notes._") {
		t.Errorf("Linked: links to a base are not followed, and it says so:\n%s", linked)
	}

	bad := string(BaseMarkdown("p/bad.base", []byte("views: [\n")))
	if !strings.Contains(bad, "⚠️ base: not valid YAML") || !strings.Contains(bad, "````yaml\nviews: [\n````") {
		t.Errorf("a base that does not parse:\n%s", bad)
	}
}
