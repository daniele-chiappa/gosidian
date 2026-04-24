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

	// Give the watcher a moment to install its initial watches.
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
