package vault

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadCache_HitAndStale(t *testing.T) {
	dir := t.TempDir()
	v := New(dir)

	path := filepath.Join(dir, "n.md")
	if err := os.WriteFile(path, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}

	n1, err := v.Load("n.md")
	if err != nil {
		t.Fatal(err)
	}
	if v.cache.Len() != 1 {
		t.Fatalf("cache len = %d, want 1", v.cache.Len())
	}

	// Second load on the same mtime → cache hit, same instance.
	n2, _ := v.Load("n.md")
	if n1 != n2 {
		t.Errorf("cache miss on unchanged mtime — returned different instances")
	}

	// Rewrite the file via the Vault API → Save should invalidate the cache.
	if err := v.Save("n.md", []byte("v2 content longer")); err != nil {
		t.Fatal(err)
	}
	if v.cache.Len() != 0 {
		t.Errorf("Save did not invalidate cache, len = %d", v.cache.Len())
	}
	n3, _ := v.Load("n.md")
	if string(n3.Content) != "v2 content longer" {
		t.Errorf("reload returned stale content: %q", n3.Content)
	}
}

func TestLoadCache_ExternalWriteDetected(t *testing.T) {
	dir := t.TempDir()
	v := New(dir)

	path := filepath.Join(dir, "x.md")
	if err := os.WriteFile(path, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	n1, _ := v.Load("x.md")

	// Simulate an external write (outside Vault.Save) with a different mtime.
	// We touch the file with a later time to force a stale-cache detection.
	later := n1.ModTime.Add(2 * 1e9) // +2s
	if err := os.WriteFile(path, []byte("changed from outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(path, later, later)

	n2, _ := v.Load("x.md")
	if string(n2.Content) != "changed from outside" {
		t.Errorf("stale cache served despite external write: %q", n2.Content)
	}
}

func TestLoadCache_Eviction(t *testing.T) {
	c := newLoadCache(2)
	// Insert 3, oldest should be evicted.
	c.Put("a", &Note{Path: "a"})
	c.Put("b", &Note{Path: "b"})
	c.Put("c", &Note{Path: "c"})
	if c.Len() != 2 {
		t.Errorf("len = %d, want 2", c.Len())
	}
	if _, ok := c.entries["a"]; ok {
		t.Errorf("a should have been evicted")
	}
}

func TestNoteETag_StableWhenUnchanged(t *testing.T) {
	dir := t.TempDir()
	v := New(dir)
	if err := v.Save("e.md", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	n1, _ := v.Load("e.md")
	n2, _ := v.Load("e.md")
	if n1.ETag() != n2.ETag() {
		t.Errorf("etag changed on identical reads: %q vs %q", n1.ETag(), n2.ETag())
	}
	// Rewrite with different content → etag must change (different size).
	_ = v.Save("e.md", []byte("hello world"))
	n3, _ := v.Load("e.md")
	if n3.ETag() == n1.ETag() {
		t.Errorf("etag did not change after content change: %q", n3.ETag())
	}
}
