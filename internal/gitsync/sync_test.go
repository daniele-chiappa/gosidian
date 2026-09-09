package gitsync

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/config"
	"github.com/gosidian/gosidian/internal/projects"
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

	// Wait for the debounced commit to land instead of sleeping a fixed
	// amount: on a loaded CI runner `git add` + `git commit` can outlast the
	// debounce window, and returning early lets the TempDir cleanup race the
	// in-flight git process (BUG-028, same family as BUG-006).
	deadline := time.Now().Add(5 * time.Second)
	for countAutoCommits(t, dir) == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	// Grace period: a second, non-coalesced commit would show up here.
	time.Sleep(2 * cfg.Debounce)
	if got := countAutoCommits(t, dir); got != 1 {
		t.Errorf("expected exactly 1 auto commit after debounce, got %d", got)
	}
}

// countAutoCommits returns how many "auto:" commits the repo at dir holds.
func countAutoCommits(t *testing.T, dir string) int {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "log", "--oneline").Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	return strings.Count(string(out), "auto:")
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

func TestSync_RefreshGitignore_ManagedBlock(t *testing.T) {
	// refreshGitignore is pure file I/O — no git binary required.
	dir := t.TempDir()

	pstore, err := projects.Open(filepath.Join(dir, ".gosidian", "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := pstore.Set("alpha", projects.Flags{SkipGitSync: true}); err != nil {
		t.Fatal(err)
	}
	if err := pstore.Set("beta", projects.Flags{SkipGitSync: true}); err != nil {
		t.Fatal(err)
	}

	// Pre-existing user-managed lines outside the marker block must survive.
	gitignorePath := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte("# user content\n*.bak\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New(dir, testCfg())
	s.SetProjects(pstore)
	if err := s.refreshGitignore(); err != nil {
		t.Fatalf("refreshGitignore: %v", err)
	}

	got, err := os.ReadFile(gitignorePath)
	if err != nil {
		t.Fatal(err)
	}
	body := string(got)
	if !strings.Contains(body, "# gosidian-managed-begin") {
		t.Errorf("missing begin marker: %s", body)
	}
	if !strings.Contains(body, "# gosidian-managed-end") {
		t.Errorf("missing end marker: %s", body)
	}
	if !strings.Contains(body, "alpha/") || !strings.Contains(body, "beta/") {
		t.Errorf("missing skipped projects in managed block: %s", body)
	}
	if !strings.Contains(body, ".gosidian/") {
		t.Errorf("baseline .gosidian/ exclusion missing: %s", body)
	}
	if !strings.Contains(body, "*.bak") || !strings.Contains(body, "# user content") {
		t.Errorf("user-managed lines lost: %s", body)
	}

	// Idempotent: running again with the same projects yields the same content.
	if err := s.refreshGitignore(); err != nil {
		t.Fatal(err)
	}
	got2, _ := os.ReadFile(gitignorePath)
	if string(got2) != body {
		t.Errorf("refresh not idempotent:\nfirst:%s\nsecond:%s", body, got2)
	}

	// Removing skip flag updates the managed block but keeps user content.
	if err := pstore.Set("alpha", projects.Flags{}); err != nil {
		t.Fatal(err)
	}
	if err := s.refreshGitignore(); err != nil {
		t.Fatal(err)
	}
	got3, _ := os.ReadFile(gitignorePath)
	if strings.Contains(string(got3), "alpha/") {
		t.Errorf("alpha/ should be removed: %s", got3)
	}
	if !strings.Contains(string(got3), "beta/") {
		t.Errorf("beta/ should remain: %s", got3)
	}
	if !strings.Contains(string(got3), "*.bak") {
		t.Errorf("user-managed lines lost on second refresh: %s", got3)
	}
}

func TestIsRepoCorruption(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		// Positive: the BUG-015 corruption class.
		{"bad object HEAD", errors.New("fatal: bad object HEAD"), true},
		{"empty object file", errors.New("error: object file .git/objects/85/e894de9b is empty"), true},
		{"loose object corrupt", errors.New("error: loose object 85e894 is corrupt"), true},
		{"upper-case", errors.New("FATAL: BAD OBJECT HEAD"), true},
		// Negative: divergence is fail-loud per ADR-002, not corruption.
		{"non-fast-forward", errors.New("! [rejected] main -> main (non-fast-forward)"), false},
		{"rejected", errors.New("updates were rejected because the remote contains work"), false},
		// Negative: ordinary failures / nil.
		{"nothing to commit", errors.New("nothing to commit, working tree clean"), false},
		{"network", errors.New("git push: could not resolve host"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		if got := isRepoCorruption(tc.err); got != tc.want {
			t.Errorf("%s: isRepoCorruption(%v) = %v, want %v", tc.name, tc.err, got, tc.want)
		}
	}
}
