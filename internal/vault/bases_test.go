package vault

import (
	"errors"
	"slices"
	"testing"
)

// An Obsidian base is read and listed, never written (IMP-118).
func TestBases(t *testing.T) {
	v := newTestVault(t)
	write(t, v.Root, "p/books.base", "views:\n  - type: table\n")
	write(t, v.Root, "p/sub/more.BASE", "views: []\n")
	write(t, v.Root, "p/.hidden.base", "views: []\n")
	write(t, v.Root, "p/.obsidian/x.base", "views: []\n")
	write(t, v.Root, "q/other.base", "views: []\n")
	write(t, v.Root, "p/note.md", "# Note\n")

	all, err := v.ListBases("")
	if err != nil || !slices.Equal(all, []string{"p/books.base", "p/sub/more.BASE", "q/other.base"}) {
		t.Errorf("ListBases = %v, %v", all, err)
	}
	if p, _ := v.ListBases("p"); !slices.Equal(p, []string{"p/books.base", "p/sub/more.BASE"}) {
		t.Errorf("ListBases(p) = %v", p)
	}
	if none, err := v.ListBases("nope"); err != nil || len(none) != 0 {
		t.Errorf("a missing project has none: %v %v", none, err)
	}

	b, err := v.LoadBase("p/books.base")
	if err != nil || b.Title != "books" || string(b.Content) != "views:\n  - type: table\n" || b.ETag() == "" {
		t.Errorf("LoadBase = %+v, %v", b, err)
	}
	if _, err := v.LoadBase("p/note.md"); !errors.Is(err, ErrNotBase) {
		t.Errorf("a note is no base: %v", err)
	}
	if _, err := v.Load("p/books.base"); !errors.Is(err, ErrNotNote) {
		t.Errorf("a base is no note: %v", err)
	}
	if err := v.Save("p/books.base", []byte("x")); !errors.Is(err, ErrNotNote) {
		t.Errorf("a base is never written: %v", err)
	}
}
