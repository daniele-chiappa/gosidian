package vault

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestWatcher_NewSubdirRace covers the race where a subdirectory and a file
// inside are created in rapid succession. Without the rescan-on-create the
// file's CREATE event is lost because the watch on the subdir wasn't active
// yet. The fix walks the new directory immediately after adding the watch.
func TestWatcher_NewSubdirRace(t *testing.T) {
	v := newTestVault(t)
	idx := openIndex(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- v.Watch(ctx, idx, nil)
	}()

	// Give the watcher a moment to install its initial watches. This is a
	// readiness sleep, not a sleep-then-assert: the assertion below polls
	// with a 2s deadline. Watch exposes no "ready" signal to wait on
	// (BUG-032 survey); adding one for a test alone was judged not worth it.
	time.Sleep(150 * time.Millisecond)

	// Create dir + file in tight sequence — this is the race.
	subdir := filepath.Join(v.Root, "fresh")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subdir, "note.md"), []byte("# hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Wait long enough for the debounce + index upsert.
	deadline := time.Now().Add(2 * time.Second)
	var found bool
	for time.Now().Before(deadline) {
		if n, _ := idx.Note("fresh/note.md"); n != nil {
			found = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	<-done

	if !found {
		t.Errorf("watcher did not index fresh/note.md after subdir race")
	}
}

// A save renames a temporary file over the note (BUG-106): the watcher sees
// the note change, not the temporary file, and a state dir inside the vault
// stays out of the index (BUG-098).
func TestWatcher_SaveByRename(t *testing.T) {
	v := newTestVault(t)
	write(t, v.Root, "p/n.md", "# old")
	write(t, v.Root, "state/x.md", "# state")
	v.SetStateDir(filepath.Join(v.Root, "state"))
	idx := openIndex(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- v.Watch(ctx, idx, nil)
	}()
	time.Sleep(150 * time.Millisecond) // readiness, as above

	if err := v.Save("p/n.md", []byte("---\ntitle: new\n---\n\nsaved whole")); err != nil {
		t.Fatal(err)
	}
	write(t, v.Root, "state/y.md", "# state")
	deadline := time.Now().Add(2 * time.Second)
	var title string
	for time.Now().Before(deadline) {
		if n, _ := idx.Note("p/n.md"); n != nil && n.Title == "new" {
			title = n.Title
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	<-done
	if title != "new" {
		t.Errorf("the watcher did not index the saved note")
	}
	for _, p := range []string{"state/x.md", "state/y.md"} {
		if n, _ := idx.Note(p); n != nil {
			t.Errorf("%s is indexed", p)
		}
	}
	if all, _ := idx.AllNotes(); len(all) != 1 {
		t.Errorf("index holds %d notes, want only p/n.md", len(all))
	}
}

// A node_modules created while the server runs is not indexed, nor is a
// hidden folder (BUG-115, S5-9); a folder moved out of the vault takes its
// notes out of the index (S5-15).
func TestWatcher_SkippedFoldersAndRemovedFolders(t *testing.T) {
	v := newTestVault(t)
	write(t, v.Root, "p/keep.md", "# keep")
	write(t, v.Root, "p/sub/a.md", "# a")
	idx := openIndex(t)
	if err := v.ScanInto(idx); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- v.Watch(ctx, idx, nil)
	}()
	time.Sleep(150 * time.Millisecond) // readiness, as above

	staged := filepath.Join(t.TempDir(), "node_modules")
	write(t, staged, "pkg/README.md", "# readme")
	if err := os.Rename(staged, filepath.Join(v.Root, "p", "node_modules")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(v.Root, "p", "sub"), filepath.Join(t.TempDir(), "sub")); err != nil {
		t.Fatal(err)
	}
	write(t, v.Root, "p/new.md", "# new") // the last event, a sign the others were handled
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if n, _ := idx.Note("p/new.md"); n != nil {
			if gone, _ := idx.Note("p/sub/a.md"); gone == nil {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	<-done
	if n, _ := idx.Note("p/sub/a.md"); n != nil {
		t.Error("a note of a folder moved out is still indexed")
	}
	if n, _ := idx.Note("p/node_modules/pkg/README.md"); n != nil {
		t.Error("a node_modules created at run time was indexed")
	}
	if n, _ := idx.Note("p/keep.md"); n == nil {
		t.Error("an untouched note left the index")
	}
}
