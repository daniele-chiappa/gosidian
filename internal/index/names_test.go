package index

import (
	"slices"
	"testing"
)

func namedPaths(t *testing.T, idx *Index, q string, projects []string, limit int) []string {
	t.Helper()
	rows, err := idx.NotesByName(q, projects, limit)
	if err != nil {
		t.Fatalf("NotesByName(%q): %v", q, err)
	}
	out := []string{}
	for _, r := range rows {
		out = append(out, r.Path)
	}
	return out
}

// A wikilink names a note by its title or its file name: `[[nota-b` finds
// nota-beta.md, titled "Nota beta", which a filter on the title alone
// missed (IMP-218, F-27). The body and the folders do not name it.
func TestNotesByName_TitleAndFileName(t *testing.T) {
	idx := openTest(t)
	upsert(t, idx, "agile-lab/notes/nota-beta.md", "Nota beta", "unrelated")
	upsert(t, idx, "agile-lab/notes/nota-alfa.md", "Nota alfa", "unrelated")
	upsert(t, idx, "agile-lab/notes/x.md", "X", "nota-b only in the body")
	upsert(t, idx, "agile-lab/nota-bfolder/y.md", "Y", "")

	if got, want := namedPaths(t, idx, "nota-b", nil, 10), []string{"agile-lab/notes/nota-beta.md"}; !slices.Equal(got, want) {
		t.Errorf("nota-b = %q, want %q (the file name)", got, want)
	}
	if got, want := namedPaths(t, idx, "nota b", nil, 10), []string{"agile-lab/notes/nota-beta.md"}; !slices.Equal(got, want) {
		t.Errorf("nota b = %q, want %q (the title)", got, want)
	}
	if got, want := namedPaths(t, idx, "NOTA", nil, 10), []string{"agile-lab/notes/nota-alfa.md", "agile-lab/notes/nota-beta.md"}; !slices.Equal(got, want) {
		t.Errorf("NOTA = %q, want %q", got, want)
	}
}

// A title that starts with the text comes first, then a file name that
// starts with it, then a title that contains it, then a file name; within
// each, by title and path.
func TestNotesByName_Order(t *testing.T) {
	idx := openTest(t)
	upsert(t, idx, "p/canal.md", "Rio", "")
	upsert(t, idx, "p/two.md", "Metal", "")
	upsert(t, idx, "p/alfa.md", "Prima", "")
	upsert(t, idx, "p/b.md", "Albero", "")
	upsert(t, idx, "p/a.md", "Alba", "")
	want := []string{"p/a.md", "p/b.md", "p/alfa.md", "p/two.md", "p/canal.md"}
	if got := namedPaths(t, idx, "al", nil, 10); !slices.Equal(got, want) {
		t.Errorf("al = %q, want %q", got, want)
	}
	if got := namedPaths(t, idx, "al", nil, 2); !slices.Equal(got, want[:2]) {
		t.Errorf("al, limit 2 = %q, want %q", got, want[:2])
	}
}

// The projects scope the names as they scope the recent notes: nil every
// note, an empty list none.
func TestNotesByName_Scope(t *testing.T) {
	idx := openTest(t)
	upsert(t, idx, "mine/plan.md", "Plan", "")
	upsert(t, idx, "other/plan.md", "Plan", "")
	if got, want := namedPaths(t, idx, "plan", []string{"mine"}, 10), []string{"mine/plan.md"}; !slices.Equal(got, want) {
		t.Errorf("plan in mine = %q, want %q", got, want)
	}
	if got, want := namedPaths(t, idx, "plan", nil, 10), []string{"mine/plan.md", "other/plan.md"}; !slices.Equal(got, want) {
		t.Errorf("plan anywhere = %q, want %q", got, want)
	}
	if got := namedPaths(t, idx, "plan", []string{}, 10); len(got) != 0 {
		t.Errorf("plan in no project = %q, want none", got)
	}
}

// Case is folded beyond ASCII, which SQLite's lower() leaves as it is; the
// extension is no part of the name, and the text is literal.
func TestNotesByName_FoldingExtensionsAndLiterals(t *testing.T) {
	idx := openTest(t)
	upsert(t, idx, "p/ercole.md", "Èrcole", "")
	upsert(t, idx, "p/pagina.html", "", "")
	upsert(t, idx, "p/note.md", "Note", "")
	upsert(t, idx, "p/a_b.md", "Ab", "")
	for _, q := range []string{"èr", "ÈR"} {
		if got, want := namedPaths(t, idx, q, nil, 10), []string{"p/ercole.md"}; !slices.Equal(got, want) {
			t.Errorf("%s = %q, want %q", q, got, want)
		}
	}
	if got, want := namedPaths(t, idx, "pagina", nil, 10), []string{"p/pagina.html"}; !slices.Equal(got, want) {
		t.Errorf("pagina = %q, want %q (an untitled HTML note by its file name)", got, want)
	}
	if got := namedPaths(t, idx, "md", nil, 10); len(got) != 0 {
		t.Errorf("md = %q, want none: the extension is no part of a name", got)
	}
	if got, want := namedPaths(t, idx, "_", nil, 10), []string{"p/a_b.md"}; !slices.Equal(got, want) {
		t.Errorf("_ = %q, want %q (no wildcard)", got, want)
	}
	if got := namedPaths(t, idx, "  ", nil, 10); len(got) != 0 {
		t.Errorf("blank = %q, want none", got)
	}
}
