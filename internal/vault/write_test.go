package vault

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// A save replaces the note whole: a reader during the write sees the old
// content or the new, never an empty or half-written note (BUG-106).
func TestVault_SaveIsWhole(t *testing.T) {
	v := newTestVault(t)
	v.SetCacheSize(0)
	a := bytes.Repeat([]byte("a"), 1<<20)
	b := bytes.Repeat([]byte("b"), 1<<20)
	if err := v.Save("p/n.md", a); err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(v.Root, "p", "n.md")

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var torn int
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			got, err := os.ReadFile(abs)
			if err != nil || !(bytes.Equal(got, a) || bytes.Equal(got, b)) {
				torn++
			}
		}
	}()
	for i := 0; i < 40; i++ {
		content := a
		if i%2 == 0 {
			content = b
		}
		if err := v.Save("p/n.md", content); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	if torn > 0 {
		t.Errorf("%d reads saw a missing, empty or partial note", torn)
	}
	left, _ := filepath.Glob(filepath.Join(v.Root, "p", ".*"))
	if len(left) > 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

// A write that fails keeps the note as it was, and leaves nothing behind;
// a note keeps its permissions across saves.
func TestVault_SaveFailureKeepsTheNote(t *testing.T) {
	v := newTestVault(t)
	write(t, v.Root, "p/n.md", "old")
	abs := filepath.Join(v.Root, "p", "n.md")
	if err := os.Chmod(abs, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := v.Save("p/n.md", []byte("new")); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(abs); st.Mode().Perm() != 0o600 {
		t.Errorf("mode after a save = %v, want 0600", st.Mode().Perm())
	}

	if os.Geteuid() == 0 {
		t.Skip("root writes into a read-only folder")
	}
	dir := filepath.Dir(abs)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if err := v.Save("p/n.md", []byte("newer")); err == nil {
		t.Fatal("a save into a read-only folder succeeded")
	}
	if got, _ := os.ReadFile(abs); string(got) != "new" {
		t.Errorf("content after a failed save = %q, want the previous one", got)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".*")); len(left) > 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

// A save cut short by a crash leaves its temporary file: the scan at the
// next start removes it once it is old, so the folder can be pruned again.
func TestVault_ScanRemovesStaleWrites(t *testing.T) {
	v := newTestVault(t)
	write(t, v.Root, "p/sub/n.md", "# n")
	stale := filepath.Join(v.Root, "p", "sub", ".gosidian-write-123.tmp")
	fresh := filepath.Join(v.Root, "p", ".gosidian-write-456.tmp")
	write(t, v.Root, "p/sub/.gosidian-write-123.tmp", "half")
	write(t, v.Root, "p/.gosidian-write-456.tmp", "on its way")
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Scan(openIndex(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("the stale temporary file is still there")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("a recent temporary file was removed: %v", err)
	}
}
