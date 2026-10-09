package gitsync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSync_History(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	cfg := testCfg()
	cfg.Debounce = 50 * time.Millisecond
	s := New(dir, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}

	// Two distinct commits on the same file
	if err := os.WriteFile(filepath.Join(dir, "note.md"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.TriggerCommit()
	s.Flush()

	if err := os.WriteFile(filepath.Join(dir, "note.md"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.TriggerCommit()
	s.Flush()

	commits, err := s.History("note.md", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) < 2 {
		t.Fatalf("expected at least 2 commits, got %d: %+v", len(commits), commits)
	}
	if commits[0].SHA == "" || commits[0].ShortSHA == "" {
		t.Errorf("missing sha in commit: %+v", commits[0])
	}
}
