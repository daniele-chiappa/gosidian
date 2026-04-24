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

	restored, err := b.Restore(id)
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

	id, notes, err := b.DiscardProject("Old")
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 2 {
		t.Errorf("expected 2 notes recorded, got %d", len(notes))
	}
	if _, err := os.Stat(filepath.Join(root, "Old")); err == nil {
		t.Errorf("project should be gone")
	}

	restored, err := b.Restore(id)
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
