package gitsync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSync_HistoryAndShow(t *testing.T) {
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
	time.Sleep(150 * time.Millisecond)

	if err := os.WriteFile(filepath.Join(dir, "note.md"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.TriggerCommit()
	time.Sleep(150 * time.Millisecond)

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

	// Show the most recent commit for that file
	diff, err := s.Show("note.md", commits[0].SHA)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "note.md") {
		t.Errorf("diff should mention the file: %s", diff)
	}
}

func TestSync_RestoreReturnsHistoricalBytes(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	cfg := testCfg()
	cfg.Debounce = 50 * time.Millisecond
	s := New(dir, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = s.Start(ctx)

	_ = os.WriteFile(filepath.Join(dir, "n.md"), []byte("first"), 0o644)
	s.TriggerCommit()
	time.Sleep(150 * time.Millisecond)
	commits, _ := s.History("n.md", 5)
	firstSHA := commits[0].SHA

	_ = os.WriteFile(filepath.Join(dir, "n.md"), []byte("second"), 0o644)
	s.TriggerCommit()
	time.Sleep(150 * time.Millisecond)

	bytes, err := s.Restore("n.md", firstSHA)
	if err != nil {
		t.Fatal(err)
	}
	if string(bytes) != "first" {
		t.Errorf("restored bytes = %q, want %q", bytes, "first")
	}
}
