package vault

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// An attachment saved again with the bytes it has is not written again
// (IMP-185): the file stays the same one, with a new mtime, which
// memory_gc_attachments reads as its age. Other bytes replace it.
func TestSaveAttachment_SameBytesNotRewritten(t *testing.T) {
	v := newTestVault(t)
	allowed := map[string]bool{".png": true}
	rel := "p/attachments/a.png"
	abs := filepath.Join(v.Root, "p", "attachments", "a.png")
	if err := v.SaveAttachment(rel, []byte("png bytes"), allowed); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(abs, old, old); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(abs)
	if err != nil {
		t.Fatal(err)
	}

	if err := v.SaveAttachment(rel, []byte("png bytes"), allowed); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(abs)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Error("the same bytes were written again: the file was replaced")
	}
	if time.Since(after.ModTime()) > time.Minute {
		t.Errorf("mtime %v, want renewed", after.ModTime())
	}

	if err := v.SaveAttachment(rel, []byte("other bytes"), allowed); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(abs); string(got) != "other bytes" {
		t.Errorf("content %q, want the new bytes", got)
	}
}
