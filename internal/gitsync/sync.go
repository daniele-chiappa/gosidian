// Package gitsync debounces vault changes and commits them to a local git
// repository inside the vault directory, optionally pushing to a remote.
//
// The sync model is deliberately simple: no pull, no merge resolution. The
// assumption is that this gosidian instance is the only writer to the remote.
// If the remote has diverged (push fails non-fast-forward), the error is
// logged loudly and the retry is left to the user — we never attempt to
// resolve conflicts automatically.
package gitsync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gosidian/gosidian/internal/config"
	"github.com/gosidian/gosidian/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

// statusGauge is overridable so tests can observe metric transitions without
// going through the global default registry. In production, Set() on the real
// metrics.GitSyncStatus gauge.
var statusGauge prometheus.Gauge = metrics.GitSyncStatus

// Sync owns the debounce timer and git state for a single vault.
type Sync struct {
	vaultDir string
	cfg      config.GitConfig

	mu      sync.Mutex
	timer   *time.Timer
	pending bool
	stopped bool
	// initFailed is set when Start's ensureRepo returned an error. Once true,
	// all TriggerCommit/Flush become no-ops: the repo is unusable for the
	// lifetime of this process (restart required to recover). Distinct from
	// runtime commit failures, which are retriable at the next TriggerCommit.
	initFailed bool

	// Runtime health, updated from Start and from each flush. Read by /healthz
	// and by Prometheus scraping. Zero-value = never started yet.
	status Status
}

// Status is a point-in-time snapshot of the gitsync subsystem health. Intended
// for /healthz and metrics. Safe to copy.
type Status struct {
	// Enabled mirrors cfg.Enabled. When false, all other fields are zero.
	Enabled bool `json:"enabled"`
	// Healthy is true when the last relevant operation (Start or commit)
	// succeeded. A disabled Sync is considered healthy (nothing to break).
	Healthy bool `json:"healthy"`
	// LastError carries the most recent error string, empty while healthy.
	LastError string `json:"last_error,omitempty"`
	// LastErrorAt is the time LastError was recorded. Zero when no error yet.
	LastErrorAt time.Time `json:"last_error_at,omitempty"`
	// LastCommitAt is the time of the most recent successful commit. Zero if
	// none since the process started.
	LastCommitAt time.Time `json:"last_commit_at,omitempty"`
}

// metric code mirrors the tri-state gauge values consumed by Prometheus.
const (
	statusCodeDisabled = 0
	statusCodeHealthy  = 1
	statusCodeDegraded = 2
)

// New builds a Sync bound to the given vault and config. Call Start before
// using TriggerCommit; on first call Start ensures the repo is initialized.
func New(vaultDir string, cfg config.GitConfig) *Sync {
	return &Sync{vaultDir: vaultDir, cfg: cfg}
}

// Start initializes the repository if needed and launches a background
// goroutine that stops the timer when ctx is cancelled. Safe to call multiple
// times.
//
// Returns an error only when gitsync is enabled AND the repo init failed. The
// caller is expected to treat that error as non-fatal and log it — the Sync's
// Status() will report Healthy=false so /healthz and metrics reflect the
// degradation without abating the rest of the process.
func (s *Sync) Start(ctx context.Context) error {
	if !s.cfg.Enabled {
		s.mu.Lock()
		s.status = Status{Enabled: false, Healthy: true}
		s.mu.Unlock()
		statusGauge.Set(statusCodeDisabled)
		return nil
	}
	if err := s.ensureRepo(); err != nil {
		wrapped := fmt.Errorf("git init: %w", err)
		s.mu.Lock()
		s.initFailed = true
		s.mu.Unlock()
		s.recordError(wrapped)
		return wrapped
	}
	s.mu.Lock()
	s.status.Enabled = true
	s.status.Healthy = true
	s.status.LastError = ""
	s.status.LastErrorAt = time.Time{}
	s.mu.Unlock()
	statusGauge.Set(statusCodeHealthy)
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		s.stopped = true
		if s.timer != nil {
			s.timer.Stop()
		}
		s.mu.Unlock()
	}()
	return nil
}

// Status returns a snapshot of the current sync health. Thread-safe.
func (s *Sync) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// recordError marks the sync as degraded, setting LastError/LastErrorAt and
// updating the Prometheus gauge. Safe for concurrent callers.
func (s *Sync) recordError(err error) {
	s.mu.Lock()
	s.status.Enabled = s.cfg.Enabled
	s.status.Healthy = false
	s.status.LastError = err.Error()
	s.status.LastErrorAt = time.Now().UTC()
	s.mu.Unlock()
	statusGauge.Set(statusCodeDegraded)
}

// recordCommitSuccess marks the sync as healthy again after a successful
// commit. Recovery from a prior degraded state is automatic.
func (s *Sync) recordCommitSuccess() {
	s.mu.Lock()
	s.status.Enabled = s.cfg.Enabled
	s.status.Healthy = true
	s.status.LastError = ""
	s.status.LastErrorAt = time.Time{}
	s.status.LastCommitAt = time.Now().UTC()
	s.mu.Unlock()
	statusGauge.Set(statusCodeHealthy)
}

// TriggerCommit resets the debounce timer. When it fires, a commit (and
// optional push) is executed. Safe to call from multiple goroutines.
func (s *Sync) TriggerCommit() {
	if !s.cfg.Enabled {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || s.initFailed {
		return
	}
	s.pending = true
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = time.AfterFunc(s.cfg.Debounce, s.flush)
}

// Flush forces an immediate commit attempt (no debounce). Useful from tests
// and from graceful shutdown.
func (s *Sync) Flush() {
	if !s.cfg.Enabled {
		return
	}
	s.mu.Lock()
	if s.initFailed {
		s.mu.Unlock()
		return
	}
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.mu.Unlock()
	s.flush()
}

// flush is the timer callback — does the actual git work.
func (s *Sync) flush() {
	s.mu.Lock()
	if !s.pending {
		s.mu.Unlock()
		return
	}
	s.pending = false
	s.mu.Unlock()

	if err := s.commitAndPush(); err != nil {
		metrics.GitSyncCommits.WithLabelValues("failure").Inc()
		s.recordError(err)
		log.Printf("gitsync: %v", err)
		return
	}
	metrics.GitSyncCommits.WithLabelValues("success").Inc()
	s.recordCommitSuccess()
}

// ensureRepo makes sure the vault directory is a git repository. If not, it
// runs `git init`, sets the default branch, writes a sane .gitignore, and
// configures the author identity in the local repo only (never touches
// global config). It also aligns the `origin` remote with cfg.Remote when
// one is configured.
func (s *Sync) ensureRepo() error {
	gitDir := filepath.Join(s.vaultDir, ".git")
	fresh := false
	if st, err := os.Stat(gitDir); err != nil || !st.IsDir() {
		if err := s.run("git", "init", "-q", "-b", s.cfg.Branch); err != nil {
			return err
		}
		fresh = true
	}
	if err := s.ensureGitignore(); err != nil {
		return err
	}
	if err := s.ensureConfig(); err != nil {
		return err
	}
	if err := s.ensureRemote(); err != nil {
		return err
	}
	if fresh {
		// Absorb the fresh .gitignore into an initial commit so a subsequent
		// no-op TriggerCommit doesn't accidentally commit it.
		if err := s.run("git", "add", ".gitignore"); err != nil {
			return err
		}
		author := fmt.Sprintf("%s <%s>", s.cfg.AuthorName, s.cfg.AuthorEmail)
		if err := s.run("git", "commit", "-q", "--author", author, "-m", "chore: init gosidian vault"); err != nil {
			return err
		}
	}
	return nil
}

// ensureRemote aligns the `origin` remote to cfg.Remote. Missing remote is
// added; mismatched URL is updated; unset cfg.Remote leaves things alone.
func (s *Sync) ensureRemote() error {
	if strings.TrimSpace(s.cfg.Remote) == "" {
		return nil
	}
	current, err := s.capture("git", "remote", "get-url", "origin")
	if err != nil {
		// No origin yet — add it.
		return s.run("git", "remote", "add", "origin", s.cfg.Remote)
	}
	if strings.TrimSpace(current) == s.cfg.Remote {
		return nil
	}
	return s.run("git", "remote", "set-url", "origin", s.cfg.Remote)
}

// ensureGitignore writes a default .gitignore excluding gosidian's runtime
// state so auto-commits never pick it up. Leaves any existing file untouched.
func (s *Sync) ensureGitignore() error {
	path := filepath.Join(s.vaultDir, ".gitignore")
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	content := `# Managed by gosidian — runtime state should never be versioned.
.gosidian/
*.tmp
*.swp
.DS_Store
`
	return os.WriteFile(path, []byte(content), 0o644)
}

func (s *Sync) ensureConfig() error {
	if err := s.run("git", "config", "--local", "user.name", s.cfg.AuthorName); err != nil {
		return err
	}
	if err := s.run("git", "config", "--local", "user.email", s.cfg.AuthorEmail); err != nil {
		return err
	}
	return nil
}

func (s *Sync) commitAndPush() error {
	// Detect whether there is anything to commit.
	out, err := s.capture("git", "status", "--porcelain")
	if err != nil {
		return fmt.Errorf("git status: %w", err)
	}
	if strings.TrimSpace(out) == "" {
		return nil
	}

	if err := s.run("git", "add", "-A"); err != nil {
		return fmt.Errorf("git add: %w", err)
	}

	msg := "auto: " + time.Now().UTC().Format(time.RFC3339)
	author := fmt.Sprintf("%s <%s>", s.cfg.AuthorName, s.cfg.AuthorEmail)
	if err := s.run("git", "commit", "-q", "--author", author, "-m", msg); err != nil {
		return fmt.Errorf("git commit: %w", err)
	}
	log.Printf("gitsync: committed %q", msg)

	if !s.cfg.Push {
		return nil
	}
	return s.push()
}

func (s *Sync) push() error {
	args := []string{}
	if tok := s.authToken(); tok != "" {
		// Use a per-invocation header so the token never lands in git config.
		args = append(args, "-c", "http.extraheader=Authorization: token "+tok)
	}
	args = append(args, "push", "-q", "origin", s.cfg.Branch)
	if err := s.run("git", args...); err != nil {
		return fmt.Errorf("git push: %w", err)
	}
	return nil
}

func (s *Sync) authToken() string {
	if s.cfg.TokenEnv == "" {
		return ""
	}
	return strings.TrimSpace(os.Getenv(s.cfg.TokenEnv))
}

// run executes a git command in the vault dir, propagating stdout/stderr
// to the server log via errors when it fails.
func (s *Sync) run(cmd string, args ...string) error {
	c := exec.Command(cmd, args...)
	c.Dir = s.vaultDir
	var stderr bytes.Buffer
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return err
		}
		return errors.New(msg)
	}
	return nil
}

func (s *Sync) capture(cmd string, args ...string) (string, error) {
	c := exec.Command(cmd, args...)
	c.Dir = s.vaultDir
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return "", err
		}
		return "", errors.New(msg)
	}
	return stdout.String(), nil
}
