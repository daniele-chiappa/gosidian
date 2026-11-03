package vault

import (
	"sync"
	"testing"
	"time"
)

func TestLockPath_SerializesSamePath(t *testing.T) {
	v := New(t.TempDir())
	const n = 32
	counter := 0
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := v.LockPath("proj/note.md")
			defer unlock()
			counter++ // unsynchronized without the lock — -race would flag it
		}()
	}
	wg.Wait()
	if counter != n {
		t.Fatalf("counter = %d, want %d", counter, n)
	}
}

func TestLockPath_CanonicalizesKey(t *testing.T) {
	v := New(t.TempDir())
	unlock := v.LockPath("a.md")
	acquired := make(chan struct{})
	go func() {
		u := v.LockPath("./a.md") // same canonical path, different spelling
		close(acquired)
		u()
	}()
	select {
	case <-acquired:
		t.Fatal("second LockPath acquired while first was still held")
	case <-time.After(50 * time.Millisecond):
	}
	unlock()
	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("second LockPath never acquired after unlock")
	}
}

// A path is busy while a writer holds its lock, or the lock of a folder
// above it (a project renamed or deleted), and free once the writer lets
// go: git sync leaves a busy file for the commit after the writer's record
// (BUG-147). A path never locked is free, and asking takes no lock.
func TestPathBusy(t *testing.T) {
	v := New(t.TempDir())
	if v.PathBusy("p/a.md") {
		t.Fatal("a path never locked is busy")
	}
	unlock := v.LockPath("./p/a.md")
	for rel, want := range map[string]bool{"p/a.md": true, "./p/a.md": true, "p/b.md": false, "p": false, "q/a.md": false} {
		if got := v.PathBusy(rel); got != want {
			t.Errorf("p/a.md locked: PathBusy(%q) = %v, want %v", rel, got, want)
		}
	}
	unlock()
	if v.PathBusy("p/a.md") {
		t.Error("p/a.md is busy once its writer let go")
	}
	unlock = v.LockPath("p")
	for rel, want := range map[string]bool{"p": true, "p/a.md": true, "p/sub/b.md": true, "pq/a.md": false, "q/a.md": false} {
		if got := v.PathBusy(rel); got != want {
			t.Errorf("p locked: PathBusy(%q) = %v, want %v", rel, got, want)
		}
	}
	unlock()
	done := make(chan struct{})
	go func() {
		v.PathBusy("p/a.md")
		v.LockPath("p/a.md")()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a writer waits for the lock after PathBusy asked")
	}
}

// LockPaths takes the locks of several paths in the order of their names,
// the same for every caller: two writers of the same paths, named the
// other way round, never wait for each other forever. A path named twice
// is locked once.
func TestLockPaths(t *testing.T) {
	v := New(t.TempDir())
	unlockB := v.LockPath("p/b.md")
	got := make(chan struct{})
	go func() {
		v.LockPaths("p/b.md", "p/a.md")()
		close(got)
	}()
	// a.md comes first: it is taken while b.md is still held.
	for deadline := time.Now().Add(2 * time.Second); !v.PathBusy("p/a.md"); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("LockPaths(b, a) did not take a.md first")
		}
	}
	unlockB()
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("LockPaths never got b.md")
	}

	done := make(chan func())
	go func() { done <- v.LockPaths("p/a.md", "./p/a.md", "p") }()
	select {
	case unlock := <-done:
		if !v.PathBusy("p") || !v.PathBusy("p/a.md") {
			t.Error("LockPaths left a path free")
		}
		unlock()
		if v.PathBusy("p") || v.PathBusy("p/a.md") {
			t.Error("a path stays busy after the unlock")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("LockPaths waits for itself on a path named twice")
	}
}

func TestLockPath_IndependentPaths(t *testing.T) {
	v := New(t.TempDir())
	unlockA := v.LockPath("a.md")
	defer unlockA()
	done := make(chan struct{})
	go func() {
		u := v.LockPath("b.md")
		u()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("locking a different path blocked behind an unrelated lock")
	}
}
