package gitsync

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/config"
)

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

func testCfg() config.GitConfig {
	return config.GitConfig{
		Enabled:     true,
		Branch:      "main",
		AuthorName:  "Test Bot",
		AuthorEmail: "bot@example.com",
		Debounce:    50 * time.Millisecond,
		Push:        false,
	}
}

func TestSync_InitAndCommit(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	s := New(dir, testCfg())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("git init should have created .git: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "note.md"), []byte("# hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.TriggerCommit()

	// Wait past debounce
	time.Sleep(200 * time.Millisecond)

	out, err := exec.Command("git", "-C", dir, "log", "--oneline").Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if !strings.Contains(string(out), "auto:") {
		t.Errorf("expected auto commit, got: %s", out)
	}
}

func TestSync_NoChangesNoCommit(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	s := New(dir, testCfg())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = s.Start(ctx)

	// Trigger without any file change
	s.TriggerCommit()
	time.Sleep(200 * time.Millisecond)

	out, _ := exec.Command("git", "-C", dir, "log", "--oneline").CombinedOutput()
	// Repo is empty (no initial commit), git log fails silently — that's fine.
	// What matters is that no auto commit got created.
	if strings.Contains(string(out), "auto:") {
		t.Errorf("unexpected commit: %s", out)
	}
}

func TestSync_DebounceCoalesces(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	cfg := testCfg()
	cfg.Debounce = 150 * time.Millisecond
	s := New(dir, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = s.Start(ctx)

	// Three rapid writes + triggers should coalesce into one commit.
	for i := 0; i < 3; i++ {
		if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte{byte('0' + i)}, 0o644); err != nil {
			t.Fatal(err)
		}
		s.TriggerCommit()
		time.Sleep(30 * time.Millisecond)
	}
	time.Sleep(300 * time.Millisecond)

	out, err := exec.Command("git", "-C", dir, "log", "--oneline").Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	autoCount := strings.Count(string(out), "auto:")
	if autoCount != 1 {
		t.Errorf("expected exactly 1 auto commit after debounce, got %d: %s", autoCount, out)
	}
}

func TestSync_Flush(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	cfg := testCfg()
	cfg.Debounce = 10 * time.Second // long debounce, Flush should bypass
	s := New(dir, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = s.Start(ctx)

	_ = os.WriteFile(filepath.Join(dir, "x.md"), []byte("x"), 0o644)
	s.TriggerCommit()
	s.Flush()

	out, err := exec.Command("git", "-C", dir, "log", "--oneline").Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if !strings.Contains(string(out), "auto:") {
		t.Errorf("flush should have committed, got: %s", out)
	}
}

func TestSync_RemoteFromConfig(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	cfg := testCfg()
	cfg.Remote = "https://example.invalid/user/repo.git"
	s := New(dir, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		t.Fatalf("origin not set: %v", err)
	}
	if strings.TrimSpace(string(out)) != cfg.Remote {
		t.Errorf("origin = %q, want %q", strings.TrimSpace(string(out)), cfg.Remote)
	}

	// Changing the config URL should rewrite origin on next Start.
	cfg.Remote = "https://example.invalid/other/repo.git"
	s2 := New(dir, cfg)
	if err := s2.Start(ctx); err != nil {
		t.Fatal(err)
	}
	out, _ = exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if strings.TrimSpace(string(out)) != cfg.Remote {
		t.Errorf("origin after rewrite = %q", strings.TrimSpace(string(out)))
	}
}

func TestSync_Disabled(t *testing.T) {
	dir := t.TempDir()
	s := New(dir, config.GitConfig{Enabled: false})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	// No .git should have been created
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		t.Errorf(".git must not be created when disabled")
	}
	// TriggerCommit is a no-op
	s.TriggerCommit()
	// A disabled Sync is considered healthy (nothing to break).
	st := s.Status()
	if st.Enabled {
		t.Errorf("Enabled=true on disabled config")
	}
	if !st.Healthy {
		t.Errorf("Healthy=false on disabled — should be true (no-op state)")
	}
}

func TestSync_StatusHealthyAfterStart(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	s := New(dir, testCfg())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	st := s.Status()
	if !st.Enabled || !st.Healthy {
		t.Errorf("expected enabled+healthy after successful Start, got %+v", st)
	}
	if st.LastError != "" {
		t.Errorf("LastError should be empty on healthy, got %q", st.LastError)
	}
}

// TestSync_GracefulDegradeOnInitFailure checks that Start, when it cannot
// initialize the repo (git binary missing), returns an error AND marks the
// subsystem as degraded so the caller can continue serving.
func TestSync_GracefulDegradeOnInitFailure(t *testing.T) {
	dir := t.TempDir()
	// Make `git` unresolvable for this test only; t.Setenv is auto-reverted.
	t.Setenv("PATH", "")
	s := New(dir, testCfg())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := s.Start(ctx)
	if err == nil {
		t.Fatal("expected error when git binary is missing")
	}
	st := s.Status()
	if st.Healthy {
		t.Errorf("expected Healthy=false after init failure, got %+v", st)
	}
	if st.LastError == "" {
		t.Errorf("expected LastError to be populated after init failure")
	}
	if st.LastErrorAt.IsZero() {
		t.Errorf("expected LastErrorAt to be set")
	}
	// Subsequent triggers must be no-op — the repo is unusable.
	s.TriggerCommit()
	s.Flush()
	// Status should still report degraded (no change from no-op ops).
	st2 := s.Status()
	if st2.Healthy {
		t.Errorf("no-op calls should not flip Healthy, got %+v", st2)
	}
}
