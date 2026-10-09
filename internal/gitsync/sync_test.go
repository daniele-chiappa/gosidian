package gitsync

import (
	"context"
	"errors"
	"fmt"
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
	// Flush is synchronous: it stops the debounce timer and runs the commit
	// inline (BUG-006/BUG-032 — never sleep-then-assert on git timing).
	s.Flush()

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

// The managed lines name one folder at the vault root, literally: a skipped
// project docs is not every docs/ of the vault, and #private, !x and a*b are
// names, not a comment, a negation and a pattern (BUG-107). A state dir
// inside the vault gets its line, and leaves the index when a commit holds
// it already (BUG-098).
func TestSync_GitignoreNamesOneFolder(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	pstore, err := projects.Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"docs", "#private", "!x", "a*b"} {
		if err := pstore.Set(name, projects.Flags{SkipGitSync: true}); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []string{"docs/a.md", "#private/a.md", "!x/a.md", "a*b/a.md", "aXb/a.md", "other/docs/a.md", "state/tokens.json"} {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(rel), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := New(dir, testCfg())
	s.SetProjects(pstore)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	s.TriggerCommit()
	s.Flush()
	tracked := func() string {
		t.Helper()
		out, err := exec.Command("git", "-C", dir, "ls-files").Output()
		if err != nil {
			t.Fatalf("git ls-files: %v", err)
		}
		return string(out)
	}
	if got, want := tracked(), "aXb/a.md\nother/docs/a.md\nstate/tokens.json\n"; !strings.HasSuffix(got, want) || strings.Count(got, "\n") != 4 {
		t.Errorf("tracked =\n%s\nwant .gitignore and\n%s", got, want)
	}

	// The state dir was committed before the vault hid it.
	s.SetStateDir("state")
	s.TriggerCommit()
	s.Flush()
	if got := tracked(); strings.Contains(got, "state/") {
		t.Errorf("the state dir is still tracked:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "state", "tokens.json")); err != nil {
		t.Errorf("untracking removed the file: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	for _, line := range []string{"/state/", "/docs/", "/#private/", "/!x/", "/a\\*b/"} {
		if !strings.Contains(string(body), "\n"+line+"\n") {
			t.Errorf(".gitignore lacks %q:\n%s", line, body)
		}
	}
}

// realRemoteCorruption is what git 2.x printed on 2026-10-07 for a push to a
// bare repository whose object files were emptied, as flush receives it
// (IMP-056). Over a file:// remote git also repeats the remote's lines
// without the "remote:" prefix.
const realRemoteCorruption = "git push: remote: error: object file /t/remote.git/objects/a2/8d194da3b896d560d4caca3562adcffe8e2d1c is empty        \n" +
	"remote: fatal: bad object refs/heads/main        \n" +
	"error: object file /t/remote.git/objects/a2/8d194da3b896d560d4caca3562adcffe8e2d1c is empty\n" +
	"fatal: bad object refs/heads/main\n" +
	"To ../remote.git\n" +
	" ! [remote rejected] main -> main (missing necessary objects)\n" +
	"error: failed to push some refs to '../remote.git'"

func TestClassifyGitFailure(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want gitFailure
	}{
		// Local: the BUG-015 corruption class.
		{"bad object HEAD", errors.New("fatal: bad object HEAD"), failureLocalCorruption},
		{"empty object file", errors.New("error: object file .git/objects/85/e894de9b is empty"), failureLocalCorruption},
		{"loose object corrupt", errors.New("error: loose object 85e894 is corrupt"), failureLocalCorruption},
		{"upper-case", errors.New("FATAL: BAD OBJECT HEAD"), failureLocalCorruption},
		// Remote (IMP-056): the vault is fine, the remote is not.
		{"real push output", errors.New(realRemoteCorruption), failureRemoteCorruption},
		{"http transport", errors.New("git push: remote: error: object file ./objects/ab/cdef is empty\nTo https://git.example.com/v.git\n ! [remote rejected] main -> main (missing necessary objects)"), failureRemoteCorruption},
		{"unpacker error", errors.New("git push: error: remote unpack failed: unpack-objects abnormal exit\n ! [remote rejected] main -> main (unpacker error)"), failureRemoteCorruption},
		{"remote line only", errors.New("git push: remote: fatal: loose object 85e894 (stored in ./objects/85/e894) is corrupt"), failureRemoteCorruption},
		// Other: divergence is fail-loud per ADR-002, a declining hook is a
		// refusal, not corruption.
		{"non-fast-forward", errors.New("! [rejected] main -> main (non-fast-forward)"), failureOther},
		{"rejected", errors.New("updates were rejected because the remote contains work"), failureOther},
		{"fetch first", errors.New("git push: To ../remote.git\n ! [rejected]        main -> main (fetch first)\nerror: failed to push some refs"), failureOther},
		{"hook declined", errors.New("git push: remote: protected branch        \nTo ../remote.git\n ! [remote rejected] main -> main (pre-receive hook declined)\nerror: failed to push some refs to '../remote.git'"), failureOther},
		{"nothing to commit", errors.New("nothing to commit, working tree clean"), failureOther},
		{"network", errors.New("git push: could not resolve host"), failureOther},
		{"nil", nil, failureOther},
	}
	for _, tc := range cases {
		if got := classifyGitFailure(tc.err); got != tc.want {
			t.Errorf("%s: classifyGitFailure = %d, want %d", tc.name, got, tc.want)
		}
	}
}

// A push to a remote whose objects are broken is recorded with the remote's
// hint, not the vault's "git fsck" one (IMP-056).
func TestSync_RemoteCorruptionMessage(t *testing.T) {
	requireGit(t)
	bare := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "init", "-q", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v %s", err, out)
	}
	dir := t.TempDir()
	cfg := testCfg()
	cfg.Debounce = 10 * time.Second
	cfg.Push = true
	cfg.Remote = bare
	s := New(dir, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	_ = os.WriteFile(filepath.Join(dir, "a.md"), []byte("a"), 0o644)
	s.TriggerCommit()
	s.Flush()
	if st := s.Status(); !st.Healthy {
		t.Fatalf("first push: %+v", st)
	}

	// Break the remote: empty every object file it holds.
	err := filepath.WalkDir(filepath.Join(bare, "objects"), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if err := os.Chmod(p, 0o644); err != nil {
			return err
		}
		return os.WriteFile(p, nil, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(dir, "a.md"), []byte("a, then b"), 0o644)
	s.TriggerCommit()
	s.Flush()
	st := s.Status()
	if st.Healthy || !strings.Contains(st.LastError, "the remote repository is corrupt") || !strings.Contains(st.LastError, "the vault here is fine") {
		t.Fatalf("status = %+v", st)
	}
	if strings.Contains(st.LastError, "in the vault; see BUG-015") {
		t.Errorf("the remote's corruption got the vault's hint: %q", st.LastError)
	}
}

// A project committed before its git sync went off leaves git's index at
// the next commit: ignoring a folder does not untrack it, and it kept going
// to the remote (BUG-115, S5-5).
func TestSync_SkippedProjectLeavesTheIndex(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	pstore, err := projects.Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"keep/a.md", "private/b.md"} {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(rel), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := New(dir, testCfg())
	s.SetProjects(pstore)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	s.TriggerCommit()
	s.Flush()
	if err := pstore.Set("private", projects.Flags{SkipGitSync: true}); err != nil {
		t.Fatal(err)
	}
	s.TriggerCommit()
	s.Flush()
	out, _ := exec.Command("git", "-C", dir, "ls-files").Output()
	if strings.Contains(string(out), "private/") || !strings.Contains(string(out), "keep/a.md") {
		t.Errorf("tracked after the skip:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "private", "b.md")); err != nil {
		t.Errorf("untracking removed the file: %v", err)
	}
}

// Flushes at once run one commit at a time: a timer's and the one at
// shutdown met each other's index.lock (BUG-115, S5-6).
func TestSync_ConcurrentFlushes(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	s := New(dir, testCfg())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	for w := 0; w < 4; w++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for i := 0; i < 5; i++ {
				_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("n-%d.md", w)), []byte(fmt.Sprint(i)), 0o644)
				s.TriggerCommit()
				s.Flush()
			}
		}()
	}
	for w := 0; w < 4; w++ {
		<-done
	}
	if st := s.Status(); !st.Healthy {
		t.Errorf("concurrent flushes left the sync unhealthy: %+v", st)
	}
	if out, _ := exec.Command("git", "-C", dir, "status", "--porcelain").Output(); len(out) != 0 {
		t.Errorf("changes left out of the commits:\n%s", out)
	}
}

// The push token goes to git in its environment, never on its command
// line, which every process of the host reads (BUG-115, S5-7).
func TestSync_PushTokenNotInArgs(t *testing.T) {
	bin := t.TempDir()
	rec := filepath.Join(t.TempDir(), "rec")
	script := "#!/bin/sh\necho \"args: $*\" > " + rec + "\nenv >> " + rec + "\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TEST_PUSH_TOKEN", "secret-push-token")
	cfg := testCfg()
	cfg.Push, cfg.TokenEnv = true, "TEST_PUSH_TOKEN"
	s := New(t.TempDir(), cfg)
	if err := s.push(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(rec)
	if err != nil {
		t.Fatal(err)
	}
	args, envs, _ := strings.Cut(string(got), "\n")
	if strings.Contains(args, "secret-push-token") || !strings.Contains(args, "push -q origin main") {
		t.Errorf("git args = %q", args)
	}
	if !strings.Contains(envs, "GIT_CONFIG_VALUE_0=Authorization: token secret-push-token") {
		t.Error("the token is not in git's environment")
	}
}

// A branch name git would read as an option, or not as a branch, is
// refused (BUG-115, S5-8).
func TestCheckBranch(t *testing.T) {
	for _, ok := range []string{"main", "release/2.71", "feat-x", "v2.71.3"} {
		if err := CheckBranch(ok); err != nil {
			t.Errorf("CheckBranch(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"", "--receive-pack=touch /tmp/x", "-x", "a b", "a..b", "a~1", "x.lock", "a/", "/a", "a@{1}", "a:b", "a\\b"} {
		if err := CheckBranch(bad); err == nil {
			t.Errorf("CheckBranch(%q) accepted", bad)
		}
	}
}

// A vault that is a git worktree (its .git a file) is a repository: git
// init does not run over it and no init commit lands on its branch
// (IMP-163).
func TestSync_WorktreeVaultIsARepository(t *testing.T) {
	requireGit(t)
	main := t.TempDir()
	git := func(dir string, args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=T", "-c", "user.email=t@example.com"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return string(out)
	}
	git(main, "init", "-q", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(main, "a.md"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(main, "add", "a.md")
	git(main, "commit", "-q", "-m", "first")
	vault := filepath.Join(t.TempDir(), "wt")
	git(main, "worktree", "add", "-q", "-b", "vault", vault)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := New(vault, testCfg()).Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	if st, err := os.Lstat(filepath.Join(vault, ".git")); err != nil || st.IsDir() {
		t.Fatalf(".git of the worktree: %v %v", st, err)
	}
	if log := git(vault, "log", "--oneline"); strings.Contains(log, "init gosidian vault") {
		t.Errorf("an init commit on the worktree's branch:\n%s", log)
	}
}
