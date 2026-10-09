package trash

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newBin(t *testing.T) (*Bin, string) {
	t.Helper()
	dir := t.TempDir()
	return New(dir, 30*24*time.Hour), dir
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBin_DiscardAndRestoreNote(t *testing.T) {
	b, root := newBin(t)
	write(t, root, "Work/task.md", "# task")

	id, err := b.DiscardNote("Work/task.md")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "Work/task.md")); err == nil {
		t.Errorf("note should be moved out")
	}

	entries, err := b.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != id {
		t.Errorf("list = %+v", entries)
	}
	if entries[0].OriginPath != "Work/task.md" {
		t.Errorf("origin = %q", entries[0].OriginPath)
	}

	restored, _, err := b.Restore(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 1 || restored[0] != "Work/task.md" {
		t.Errorf("restored = %+v", restored)
	}
	if _, err := os.Stat(filepath.Join(root, "Work/task.md")); err != nil {
		t.Errorf("note not back: %v", err)
	}
}

func TestBin_DiscardProject(t *testing.T) {
	b, root := newBin(t)
	write(t, root, "Old/a.md", "a")
	write(t, root, "Old/sub/b.md", "b")

	id, notes, err := b.DiscardProject("Old", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 2 {
		t.Errorf("expected 2 notes recorded, got %d", len(notes))
	}
	if _, err := os.Stat(filepath.Join(root, "Old")); err == nil {
		t.Errorf("project should be gone")
	}

	restored, _, err := b.Restore(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 2 {
		t.Errorf("restored count = %d, want 2", len(restored))
	}
	if _, err := os.Stat(filepath.Join(root, "Old", "a.md")); err != nil {
		t.Errorf("not restored: %v", err)
	}
}

func TestBin_PurgeAndPruneExpired(t *testing.T) {
	b, root := newBin(t)
	write(t, root, "x.md", "x")
	id, _ := b.DiscardNote("x.md")
	if err := b.Purge(id); err != nil {
		t.Fatal(err)
	}
	if entries, _ := b.List(); len(entries) != 0 {
		t.Errorf("trash should be empty, got %+v", entries)
	}

	// Prune-expired with retention shorter than the entry age — entry is
	// already gone so nothing to prune; verify zero return.
	if removed, _ := b.PruneExpired(); removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
}

// The meta given to DiscardProject travels with the trashed folder, comes
// back from Restore and does not land in the vault (IMP-124).
func TestBin_ProjectMeta(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "P"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "P", "n.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := New(root, -1)
	id, notes, err := b.DiscardProject("P", []byte(`{"flags":{"visibility":"private"}}`))
	if err != nil || len(notes) != 1 {
		t.Fatalf("discard: %v %v", notes, err)
	}
	meta, err := b.ProjectMeta(id)
	if err != nil || string(meta) != `{"flags":{"visibility":"private"}}` {
		t.Fatalf("ProjectMeta = %q, %v", meta, err)
	}
	restored, back, err := b.Restore(id)
	if err != nil || len(restored) != 1 || string(back) != string(meta) {
		t.Fatalf("Restore = %v, %q, %v", restored, back, err)
	}
	if _, err := os.Stat(filepath.Join(root, "P", projectMetaFile)); !os.IsNotExist(err) {
		t.Errorf("the meta file came back into the vault: %v", err)
	}

	// Without meta, and for a note, there is none.
	id2, _, err := b.DiscardProject("P", nil)
	if err != nil {
		t.Fatal(err)
	}
	if meta, err := b.ProjectMeta(id2); meta != nil || err != nil {
		t.Errorf("ProjectMeta without meta = %q, %v", meta, err)
	}
}

// Ids that are not a single name in the bin are refused before any disk
// operation.
func TestBin_RefusesBadIDs(t *testing.T) {
	b := New(t.TempDir(), -1)
	for _, id := range []string{"", ".", "..", "../x", "a/b", `a\b`, "a\x00b"} {
		if err := b.Purge(id); err == nil {
			t.Errorf("Purge(%q) accepted", id)
		}
		if _, _, err := b.Restore(id); err == nil {
			t.Errorf("Restore(%q) accepted", id)
		}
		if _, err := b.ProjectMeta(id); err == nil {
			t.Errorf("ProjectMeta(%q) accepted", id)
		}
	}
}

// A project folder that already holds a file with the sidecar's name is not
// trashed: the file is neither overwritten nor followed.
func TestBin_ProjectMetaNameReserved(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "P")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, projectMetaFile), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := New(root, -1).DiscardProject("P", []byte("{}")); err == nil {
		t.Fatal("discard over an existing sidecar name succeeded")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, projectMetaFile)); string(b) != "mine" {
		t.Errorf("the existing file was changed: %q", b)
	}
}

// An entry whose decoded origin leaves the vault, or lands in a hidden
// folder, is refused by Restore and fails ValidOrigin: checkID only sees the
// raw id, where "/" is still %2F (BUG-083).
func TestBin_RestoreRefusesOriginsOutsideTheVault(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vault")
	b := New(root, -1)
	if err := os.MkdirAll(b.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{"../escape.md", "a/../../escape.md", ".gosidian/x.md", "a//b.md", "/abs.md"} {
		id := newID(origin)
		if err := os.WriteFile(filepath.Join(b.dir, id), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if ValidOrigin(Origin(id)) {
			t.Errorf("ValidOrigin(%q) = true", Origin(id))
		}
		if _, _, err := b.Restore(id); err == nil {
			t.Errorf("Restore of origin %q succeeded", origin)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape.md")); !os.IsNotExist(err) {
		t.Errorf("a file was written outside the vault: %v", err)
	}
	for _, ok := range []string{"p/n.md", "n.md", "p/sub/n.md", "P"} {
		if !ValidOrigin(ok) {
			t.Errorf("ValidOrigin(%q) = false", ok)
		}
	}
}

// Trashing the last note of a folder removes the folders it left empty,
// up to the project (IMP-129).
func TestDiscardNote_PrunesEmptyFolders(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "p", "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "p", "a", "b", "n.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := New(root, -1)
	if _, err := b.DiscardNote("p/a/b/n.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "p", "a")); err == nil {
		t.Error("p/a was left on disk")
	}
	if _, err := os.Stat(filepath.Join(root, "p")); err != nil {
		t.Errorf("the project went too: %v", err)
	}
}

func TestBin_DiscardFolder(t *testing.T) {
	b, root := newBin(t)
	write(t, root, "Work/a/docs/one.md", "1")
	write(t, root, "Work/a/docs/sub/two.md", "2")
	write(t, root, "Work/a/docs/attachments/pic.png", "png")
	write(t, root, "Work/keep.md", "k")

	id, notes, err := b.DiscardFolder("Work/a/docs")
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 2 {
		t.Errorf("notes = %v, want the 2 notes", notes)
	}
	// Work/a held only docs: it goes too, the project stays.
	if _, err := os.Stat(filepath.Join(root, "Work", "a")); err == nil {
		t.Error("Work/a was left on disk, empty")
	}
	if _, err := os.Stat(filepath.Join(root, "Work", "keep.md")); err != nil {
		t.Errorf("the rest of the project went too: %v", err)
	}

	entries, err := b.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].OriginPath != "Work/a/docs" || !entries[0].IsDir || entries[0].IsProject() {
		t.Fatalf("entries = %+v, want one folder entry that is not a project", entries)
	}

	restored, meta, err := b.Restore(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 2 || meta != nil {
		t.Errorf("restored = %v meta = %q, want the 2 notes and no meta", restored, meta)
	}
	if _, err := os.Stat(filepath.Join(root, "Work/a/docs/attachments/pic.png")); err != nil {
		t.Errorf("the attachment did not come back: %v", err)
	}
}

func TestBin_DiscardFolderRefuses(t *testing.T) {
	b, root := newBin(t)
	write(t, root, "Work/note.md", "n")
	if err := os.Symlink(filepath.Join(root, "Work"), filepath.Join(root, "Work", "link")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"Work", "Work/note.md", "Work/missing", "Work/link"} {
		if _, _, err := b.DiscardFolder(rel); err == nil {
			t.Errorf("DiscardFolder(%q) succeeded", rel)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "Work", "note.md")); err != nil {
		t.Errorf("the project was touched: %v", err)
	}
}

func TestEntry_IsProject(t *testing.T) {
	cases := []struct {
		e    Entry
		want bool
	}{
		{Entry{OriginPath: "Work", IsDir: true}, true},
		{Entry{OriginPath: "Work/docs", IsDir: true}, false},
		{Entry{OriginPath: "Work/note.md"}, false},
		{Entry{OriginPath: "note.md"}, false},
	}
	for _, c := range cases {
		if got := c.e.IsProject(); got != c.want {
			t.Errorf("%+v IsProject = %v, want %v", c.e, got, c.want)
		}
	}
}

// A name with "%" in it comes back as it was: "%20" in a name is not a
// space, and the root note "B%2Fx.md" is not a note of project B
// (BUG-112, S2-4).
func TestBin_PercentInNames(t *testing.T) {
	b, root := newBin(t)
	for _, rel := range []string{"proj/My%20Note.md", "B%2Fx.md", "proj/100%.md"} {
		write(t, root, rel, "# x")
		id, err := b.DiscardNote(rel)
		if err != nil {
			t.Fatal(err)
		}
		if got := Origin(id); got != rel {
			t.Errorf("origin of %q = %q", rel, got)
		}
		restored, _, err := b.Restore(id)
		if err != nil {
			t.Fatalf("restore %q: %v", rel, err)
		}
		if len(restored) != 1 || restored[0] != rel {
			t.Errorf("restored %q as %v", rel, restored)
		}
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%q is not back in place: %v", rel, err)
		}
	}
}
