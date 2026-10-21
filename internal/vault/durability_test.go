package vault

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseDurability(t *testing.T) {
	for in, want := range map[string]Durability{"": DurableBatch, "batch": DurableBatch, "always": DurableAlways, "none": DurableNone} {
		got, err := ParseDurability(in)
		if err != nil || got != want {
			t.Errorf("ParseDurability(%q) = %v, %v; want %v", in, got, err, want)
		}
		if in != "" && got.String() != in {
			t.Errorf("%v.String() = %q, want %q", got, got.String(), in)
		}
	}
	if _, err := ParseDurability("sometimes"); err == nil {
		t.Error("ParseDurability(sometimes): no error")
	}
}

// With durability batch the writes of a batch wait for SyncBatch, which
// brings them to disk together; a write on its own is synced as always
// (IMP-185). With always and none nothing waits for a SyncBatch.
func TestDurability_BatchWaitsForSyncBatch(t *testing.T) {
	allowed := map[string]bool{".png": true}
	pending := func(v *Vault) int {
		v.batch.mu.Lock()
		defer v.batch.mu.Unlock()
		return len(v.batch.dirs)
	}

	v := newTestVault(t)
	if err := v.Save("p/single.md", []byte("# one")); err != nil {
		t.Fatal(err)
	}
	if n := pending(v); n != 0 {
		t.Fatalf("a write on its own waits for SyncBatch: %d folders", n)
	}
	if err := v.SaveBatched("p/a/n.md", []byte("# a")); err != nil {
		t.Fatal(err)
	}
	if err := v.SaveAttachmentBatched("p/attachments/x.png", []byte("png"), allowed); err != nil {
		t.Fatal(err)
	}
	if canSyncFS {
		if n := pending(v); n != 2 {
			t.Fatalf("%d folders wait for SyncBatch, want 2", n)
		}
	}
	if err := v.SyncBatch(); err != nil {
		t.Fatal(err)
	}
	if n := pending(v); n != 0 {
		t.Fatalf("after SyncBatch %d folders still wait", n)
	}
	for _, rel := range []string{"p/a/n.md", "p/attachments/x.png"} {
		if _, err := os.Stat(filepath.Join(v.Root, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s: %v", rel, err)
		}
	}

	// A folder gone before SyncBatch is skipped.
	if err := v.SaveBatched("p/gone/n.md", []byte("# g")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(v.Root, "p", "gone")); err != nil {
		t.Fatal(err)
	}
	if err := v.SyncBatch(); err != nil {
		t.Fatalf("SyncBatch after a folder went: %v", err)
	}

	for _, d := range []Durability{DurableAlways, DurableNone} {
		v := newTestVault(t)
		v.SetDurability(d)
		if err := v.SaveBatched("p/n.md", []byte("# n")); err != nil {
			t.Fatal(err)
		}
		if n := pending(v); n != 0 {
			t.Errorf("durability %v: %d folders wait for SyncBatch", d, n)
		}
	}
}
