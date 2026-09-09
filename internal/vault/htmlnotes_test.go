package vault

import "testing"

func pathInList(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func TestVault_HTMLNotesGating(t *testing.T) {
	dir := t.TempDir()
	v := New(dir)
	if err := v.Save("proj/a.md", []byte("# a")); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "proj/page.html", "<html><body>x</body></html>")

	// Flag off (default): .html is invisible, .md is a note.
	if v.IsNoteFile("x.html") {
		t.Error("IsNoteFile(.html) must be false when disabled")
	}
	if !v.IsNoteFile("x.md") {
		t.Error("IsNoteFile(.md) must always be true")
	}
	list, err := v.List()
	if err != nil {
		t.Fatal(err)
	}
	if pathInList(list, "proj/page.html") {
		t.Errorf("html note listed with flag off: %v", list)
	}
	if !pathInList(list, "proj/a.md") {
		t.Errorf("md note missing from list: %v", list)
	}

	// Flag on: .html becomes a first-class note.
	v.SetHTMLNotes(true)
	if !v.IsNoteFile("x.html") {
		t.Error("IsNoteFile(.html) must be true when enabled")
	}
	list, err = v.List()
	if err != nil {
		t.Fatal(err)
	}
	if !pathInList(list, "proj/page.html") {
		t.Errorf("html note not listed with flag on: %v", list)
	}

	// Non-note extensions stay excluded regardless of the flag.
	if v.IsNoteFile("x.txt") || v.IsNoteFile("img.png") {
		t.Error("only .md/.html are note files")
	}
}

func TestVault_RenamePreservesHTMLExtension(t *testing.T) {
	dir := t.TempDir()
	v := New(dir)
	v.SetHTMLNotes(true)
	idx := openIndex(t)
	write(t, dir, "proj/dash.html", "<html><body>hi</body></html>")
	if n, lerr := v.Load("proj/dash.html"); lerr == nil {
		_ = idx.Upsert(toIndexNote(n))
	}
	// Rename without an extension on the target must keep .html.
	if _, err := v.RenameNote(idx, "proj/dash.html", "proj/board"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if !v.Exists("proj/board.html") {
		t.Error("rename should have preserved the .html extension → proj/board.html")
	}
	if v.Exists("proj/board.md") {
		t.Error("rename must not coerce a .html note to .md")
	}
}
