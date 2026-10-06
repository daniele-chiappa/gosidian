package vault

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gosidian/gosidian/internal/index"
)

func exists(t *testing.T, root, rel string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

// Deleting, moving or renaming the last note of a folder removes the
// folders it left empty, up to the project, which stays (IMP-129).
func TestPruneEmptyParents(t *testing.T) {
	root := t.TempDir()
	v := New(root)
	idx, err := index.Open(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	for _, rel := range []string{"p/a/b/one.md", "p/a/keep.md", "p/c/d/two.md", "p/top.md", "q/x/note.md"} {
		if err := v.Save(rel, []byte("# n\n")); err != nil {
			t.Fatal(err)
		}
	}
	if err := v.Delete("p/a/b/one.md"); err != nil {
		t.Fatal(err)
	}
	if exists(t, root, "p/a/b") || !exists(t, root, "p/a") {
		t.Errorf("after delete: p/a/b exists=%v, p/a exists=%v (keep.md holds it)", exists(t, root, "p/a/b"), exists(t, root, "p/a"))
	}
	// The last note of the project: the project folder stays.
	if err := v.Delete("p/top.md"); err != nil {
		t.Fatal(err)
	}
	if err := v.Delete("p/a/keep.md"); err != nil {
		t.Fatal(err)
	}
	if exists(t, root, "p/a") || !exists(t, root, "p") {
		t.Errorf("p/a exists=%v, p exists=%v", exists(t, root, "p/a"), exists(t, root, "p"))
	}

	// A rename out of a folder prunes the folder it left.
	if _, err := v.RenameNote(idx, "p/c/d/two.md", "p/two.md"); err != nil {
		t.Fatal(err)
	}
	if exists(t, root, "p/c") {
		t.Error("the rename left p/c on disk")
	}
	// And so does a move to another project.
	if _, err := v.MoveNote(idx, "q/x/note.md", "p"); err != nil {
		t.Fatal(err)
	}
	if exists(t, root, "q/x") || !exists(t, root, "q") {
		t.Errorf("after the move: q/x exists=%v, q exists=%v", exists(t, root, "q/x"), exists(t, root, "q"))
	}

	// The last attachment of a folder goes with its folder.
	if err := v.SaveAttachment("p/attachments/f.png", []byte("x"), map[string]bool{".png": true}); err != nil {
		t.Fatal(err)
	}
	if err := v.DeleteAttachment("p/attachments/f.png", map[string]bool{".png": true}); err != nil {
		t.Fatal(err)
	}
	if exists(t, root, "p/attachments") {
		t.Error("an empty attachments/ was left")
	}
}

// A folder pruned meanwhile is made again by the next write into it.
func TestSave_RecreatesAPrunedFolder(t *testing.T) {
	root := t.TempDir()
	v := New(root)
	if err := v.Save("p/a/one.md", []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "p", "a")); err != nil {
		t.Fatal(err)
	}
	if err := v.Save("p/a/two.md", []byte("y")); err != nil {
		t.Errorf("save into a pruned folder: %v", err)
	}
}
