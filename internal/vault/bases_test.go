package vault

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
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

// The file tree lists bases and canvases in one walk (IMP-160, S2-15).
func TestListBasesAndCanvases(t *testing.T) {
	v := newTestVault(t)
	write(t, v.Root, "p/books.base", "views: []\n")
	write(t, v.Root, "p/board.canvas", "{}\n")
	write(t, v.Root, "p/note.md", "# Note\n")
	write(t, v.Root, "q/map.canvas", "{}\n")

	all, err := v.ListBasesAndCanvases("")
	if err != nil || !slices.Equal(all, []string{"p/board.canvas", "p/books.base", "q/map.canvas"}) {
		t.Errorf("ListBasesAndCanvases = %v, %v", all, err)
	}
	if p, _ := v.ListBasesAndCanvases("p"); !slices.Equal(p, []string{"p/board.canvas", "p/books.base"}) {
		t.Errorf("ListBasesAndCanvases(p) = %v", p)
	}
}

// A base or a canvas that is a link, or sits in a linked folder, is not
// read: it reached files outside the vault (IMP-163).
func TestLoadReadOnly_RefusesLinks(t *testing.T) {
	v := newTestVault(t)
	outside := t.TempDir()
	write(t, outside, "secret.base", "views: []\n")
	write(t, outside, "dir/board.canvas", "{}\n")
	write(t, v.Root, "p/note.md", "# Note\n")
	if err := os.Symlink(filepath.Join(outside, "secret.base"), filepath.Join(v.Root, "p", "secret.base")); err != nil {
		t.Skip("no symlinks:", err)
	}
	if err := os.Symlink(filepath.Join(outside, "dir"), filepath.Join(v.Root, "p", "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := v.LoadBase("p/secret.base"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a linked base: %v", err)
	}
	if _, err := v.LoadCanvas("p/linked/board.canvas"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a canvas in a linked folder: %v", err)
	}
	write(t, v.Root, "q/boards/other.canvas", "{}\n")
	if err := os.Symlink(filepath.Join(v.Root, "q", "boards"), filepath.Join(v.Root, "p", "inner")); err != nil {
		t.Fatal(err)
	}
	if _, err := v.LoadCanvas("p/inner/other.canvas"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a canvas of another project through a linked folder: %v", err)
	}
	write(t, v.Root, "p/real.base", "views: []\n")
	if _, err := v.LoadBase("p/real.base"); err != nil {
		t.Errorf("a plain base: %v", err)
	}
}
