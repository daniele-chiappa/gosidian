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

// The diff of two texts is its hunks, without the header that names the
// temporary files; two equal texts have none (IMP-222).
func TestDiff(t *testing.T) {
	requireGit(t)
	got, err := Diff([]byte("a\nb\nc\n"), []byte("a\nB\nc\n"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n"; got != want {
		t.Errorf("diff = %q, want %q", got, want)
	}
	if got, err := Diff([]byte("same\n"), []byte("same\n")); err != nil || got != "" {
		t.Errorf("equal texts = %q %v, want none", got, err)
	}
	if got, err := Diff(nil, []byte("new\n")); err != nil || got != "@@ -0,0 +1 @@\n+new\n" {
		t.Errorf("from nothing = %q %v", got, err)
	}
}

// A note's path is no pattern: p/[a].md has its own history, not the one
// of p/a.md, which [a] would match.
func TestSync_HistoryLiteralPath(t *testing.T) {
	s, _, write := startWrites(t)
	write("p/a.md", "# A\n")
	flushNow(s)
	write("p/[a].md", "# Bracket\n")
	flushNow(s)
	write("p/a.md", "# A again\n")
	flushNow(s)
	commits, err := s.History("p/[a].md", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 1 || commits[0].Path != "p/[a].md" || commits[0].Status != "added" {
		t.Errorf("history of p/[a].md = %+v, want its creation alone", commits)
	}
}
