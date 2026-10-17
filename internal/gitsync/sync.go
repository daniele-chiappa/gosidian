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
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gosidian/gosidian/internal/config"
	"github.com/gosidian/gosidian/internal/metrics"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/vault"
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

	// projects (optional) drives the per-project skip_git_sync flag: when set,
	// refreshGitignore renders the managed block of .gitignore with one
	// `/<name>/` line per skipped project. nil = no per-project filtering.
	projects *projects.Store

	// stateRel is the state dir's vault-relative path when it sits inside
	// the vault ("" otherwise): one more managed line keeps the credentials
	// off the remote (BUG-098).
	stateRel string
	// untracked holds the ignored folders a commit has taken out of git's
	// index in this process: the state dir, the projects with git sync off.
	// The ignore lines keep them out afterwards. Used by commitAndPush only.
	untracked map[string]bool
	// bigIN holds the files over INMaxTrackedBytes of the IN folders git
	// tracks (track_in_git), taken out of a commit in this process: the
	// managed .gitignore names them afterwards. Used by commitAndPush only.
	bigIN map[string]bool

	// workMu runs one commit at a time: a timer's flush and Flush at
	// shutdown ran together, the second met the first's index.lock, and the
	// last changes were left out (BUG-115, S5-6).
	workMu sync.Mutex

	// tokens (optional) is the on-disk token fallback used by authToken when
	// the env var is unset. Allows the operator to rotate the PAT from the
	// web UI without restarting the container.
	tokens *TokenStore

	// onCommit are told of each commit git sync makes (OnCommit).
	onCommit []func(CommitChanges)

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

// CommitChanges is a commit git sync made: its SHA, its date, and the
// files it changed, as vault paths (the commit trigger of the pipelines,
// IMP-170).
type CommitChanges struct {
	SHA   string
	Date  time.Time
	Files []Change
}

// Change is a file a commit changed: Status is added, modified, deleted or
// renamed; From is the path a renamed file had.
type Change struct {
	Path   string
	From   string
	Status string
}

// OnCommit registers fn, told of each commit git sync makes, after it is
// made and before any push. fn runs on the commit's goroutine, in the
// order of the commits, and must be quick. Safe to call at any time.
func (s *Sync) OnCommit(fn func(CommitChanges)) {
	if fn == nil {
		return
	}
	s.mu.Lock()
	s.onCommit = append(s.onCommit, fn)
	s.mu.Unlock()
}

// SetProjects wires the per-project flag store. When non-nil, refreshGitignore
// renders one `<name>/` line per project marked SkipGitSync inside the
// managed block of .gitignore. Safe to call before Start; ignored if the
// store is nil (current behaviour preserved).
func (s *Sync) SetProjects(p *projects.Store) {
	s.mu.Lock()
	s.projects = p
	s.mu.Unlock()
}

// SetStateDir names the state dir's vault-relative path (vault.Vault's
// StateDirInside) so the managed .gitignore leaves it out; "" when the state
// dir is outside the vault. The default, .gosidian, has its line already.
// Safe to call before Start.
func (s *Sync) SetStateDir(rel string) {
	if rel == ".gosidian" {
		rel = ""
	}
	s.mu.Lock()
	s.stateRel = rel
	s.mu.Unlock()
}

// SetTokenStore wires the on-disk token fallback. authToken consults the
// env var first (cfg.TokenEnv), then the store. nil keeps the legacy
// env-only behaviour.
func (s *Sync) SetTokenStore(t *TokenStore) {
	s.mu.Lock()
	s.tokens = t
	s.mu.Unlock()
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
	err := CheckBranch(s.cfg.Branch)
	if err == nil {
		err = s.ensureRepo()
	}
	if err != nil {
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

// Commits says whether the sync makes commits: on, and its repository set
// up. A failed commit or push does not stop it: the next commit may work.
func (s *Sync) Commits() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.Enabled && !s.initFailed
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

// repoCorruptionSignatures are substrings git emits when a repository is
// structurally broken — an empty/corrupt loose object or an unreadable HEAD:
// the vault's own (the BUG-015 class) or, on a "remote:" line, the remote's
// (IMP-056). They leave out "non-fast-forward"/"rejected", which signal a
// benign remote divergence handled fail-loud per ADR-002, not corruption.
var repoCorruptionSignatures = []string{
	"bad object",   // "fatal: bad object HEAD"
	"object file",  // "error: object file .git/objects/.. is empty"
	"loose object", // "error: loose object .. is corrupt"
	"corrupt",
}

// gitFailure classifies a failed commit or push for the hint /healthz shows.
type gitFailure int

const (
	// failureOther is any other failure: network, credentials, a rejected
	// non-fast-forward push, a hook that declines it.
	failureOther gitFailure = iota
	// failureLocalCorruption is the vault's own .git broken (BUG-015).
	failureLocalCorruption
	// failureRemoteCorruption is the remote repository broken (IMP-056):
	// the vault here is fine.
	failureRemoteCorruption
)

// remoteCorruptionReasons are the reasons git prints after a
// "[remote rejected]" ref when the remote could not store the objects:
// "! [remote rejected] main -> main (missing necessary objects)".
var remoteCorruptionReasons = []string{"missing necessary objects", "unpacker error", "unpack failed"}

// classifyGitFailure tells a corrupt remote from a corrupt vault and from
// everything else. Case-insensitive.
//   - Remote: a "[remote rejected]" ref with a reason of
//     remoteCorruptionReasons, or a "remote:" line carrying a corruption
//     signature ("remote: error: object file … is empty"). A ref the remote
//     rejects for another reason (a hook declining it) is no corruption.
//   - Local: a corruption signature in a message that does not mention a
//     rejected push: divergence (remote ahead) stays fail-loud as it is,
//     per ADR-002.
func classifyGitFailure(err error) gitFailure {
	if err == nil {
		return failureOther
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "[remote rejected]") {
		for _, r := range remoteCorruptionReasons {
			if strings.Contains(msg, r) {
				return failureRemoteCorruption
			}
		}
	}
	for _, line := range strings.Split(msg, "\n") {
		line = strings.TrimPrefix(strings.TrimSpace(line), "git push: ")
		if strings.HasPrefix(line, "remote:") && hasCorruptionSignature(line) {
			return failureRemoteCorruption
		}
	}
	if strings.Contains(msg, "non-fast-forward") || strings.Contains(msg, "rejected") {
		return failureOther
	}
	if hasCorruptionSignature(msg) {
		return failureLocalCorruption
	}
	return failureOther
}

func hasCorruptionSignature(s string) bool {
	for _, sig := range repoCorruptionSignatures {
		if strings.Contains(s, sig) {
			return true
		}
	}
	return false
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

// flush is the timer callback — does the actual git work. A flush that
// finds a commit running waits for it, then commits what came after.
func (s *Sync) flush() {
	s.workMu.Lock()
	defer s.workMu.Unlock()
	s.mu.Lock()
	if !s.pending {
		s.mu.Unlock()
		return
	}
	s.pending = false
	s.mu.Unlock()

	if err := s.commitAndPush(); err != nil {
		s.failed(err)
		return
	}
	s.succeeded()
}

// failed records a failed commit or push: the metric, the status, with
// the hint of a corrupt repository, and the log. It returns the error as
// recorded.
func (s *Sync) failed(err error) error {
	metrics.GitSyncCommits.WithLabelValues("failure").Inc()
	switch classifyGitFailure(err) {
	case failureLocalCorruption:
		err = fmt.Errorf("repository corruption detected — manual repair "+
			"required (run `git fsck --full` in the vault; see BUG-015): %w", err)
	case failureRemoteCorruption:
		err = fmt.Errorf("the remote repository is corrupt — the vault here "+
			"is fine: repair the remote (`git fsck --full` in its bare "+
			"repository), or recreate it empty and the next push fills it "+
			"(IMP-056): %w", err)
	}
	s.recordError(err)
	log.Printf("gitsync: %v", err)
	return err
}

// succeeded records a commit, or a flush with nothing to commit.
func (s *Sync) succeeded() {
	metrics.GitSyncCommits.WithLabelValues("success").Inc()
	s.recordCommitSuccess()
}

// ensureRepo makes sure the vault directory is a git repository. If not, it
// runs `git init`, sets the default branch, writes a sane .gitignore, and
// configures the author identity in the local repo only (never touches
// global config). It also aligns the `origin` remote with cfg.Remote when
// one is configured.
func (s *Sync) ensureRepo() error {
	// A .git file is a repository too: a worktree or a submodule, whose
	// gitdir it names. Taken for none, it had git init run over it and an
	// init commit added to its branch (IMP-163).
	gitDir := filepath.Join(s.vaultDir, ".git")
	fresh := false
	if _, err := os.Lstat(gitDir); errors.Is(err, fs.ErrNotExist) {
		if err := s.run("git", "init", "-q", "--initial-branch="+s.cfg.Branch); err != nil {
			return err
		}
		fresh = true
	}
	if err := s.refreshGitignore(); err != nil {
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

// CheckBranch refuses a branch name git would not take as one, or would
// take for an option: the owner sets it from Settings, and a name starting
// with "-" reached `git push` as an option, --receive-pack=<command> among
// them (BUG-115, S5-8).
func CheckBranch(name string) error {
	bad := name == "" || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "/") ||
		strings.HasSuffix(name, "/") || strings.HasSuffix(name, ".") || strings.HasSuffix(name, ".lock") ||
		strings.Contains(name, "..") || strings.Contains(name, "//") || strings.Contains(name, "@{") || name == "@"
	for _, r := range name {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(" ~^:?*[\\", r) {
			bad = true
		}
	}
	if bad {
		return fmt.Errorf("git branch %q is not a valid branch name", name)
	}
	return nil
}

// gitignoreManagedBegin and gitignoreManagedEnd bracket the lines this package
// owns inside the vault's .gitignore. Anything outside the markers is
// preserved verbatim so the user can hand-edit additional patterns.
const (
	gitignoreManagedBegin = "# gosidian-managed-begin"
	gitignoreManagedEnd   = "# gosidian-managed-end"
)

// refreshGitignore rewrites the managed block of <vault>/.gitignore so it
// always reflects (a) gosidian's runtime state exclusions and (b) any
// project flagged SkipGitSync via the projects.Store. Idempotent — safe to
// call on every sync. User-added lines outside the markers survive.
func (s *Sync) refreshGitignore() error {
	path := filepath.Join(s.vaultDir, ".gitignore")
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	var managedLines []string
	managedLines = append(managedLines,
		gitignoreManagedBegin,
		"# Managed by gosidian — do not edit between these markers.",
		".gosidian/",
		"*.tmp",
		"*.swp",
		".DS_Store",
	)
	if s.stateRel != "" {
		managedLines = append(managedLines, gitignoreDir(s.stateRel))
	}
	if s.projects != nil {
		for _, name := range s.projects.SkipNamesForGit() {
			managedLines = append(managedLines, gitignoreDir(name))
		}
		// The IN folders (ADR-037): left out, unless the project tracks
		// its own; then only its files over the cap.
		ignored, tracked := s.projects.INFoldersForGit()
		for _, name := range ignored {
			managedLines = append(managedLines, gitignoreDir(name+"/"+vault.INFolder))
		}
		for _, p := range s.bigINUnder(tracked) {
			managedLines = append(managedLines, gitignoreFile(p))
		}
	}
	managedLines = append(managedLines, gitignoreManagedEnd)

	managed := strings.Join(managedLines, "\n") + "\n"

	// Strip any existing managed block from the old file content so we can
	// regenerate it. Lines outside the markers are preserved.
	user := stripManagedBlock(string(old))
	user = strings.TrimSpace(user)

	var combined string
	if user == "" {
		combined = managed
	} else {
		combined = managed + "\n" + user + "\n"
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(combined), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// gitignoreDir is the .gitignore line for the vault folder rel and nothing
// else (BUG-107): anchored at the vault root, so `docs` does not match the
// docs/ of every project, and with the pattern characters escaped, so a
// name with `*` or `[` matches only itself. The leading slash also keeps a
// name starting with `#` or `!` from reading as a comment or a negation.
func gitignoreDir(rel string) string {
	var b strings.Builder
	b.WriteByte('/')
	for _, r := range rel {
		switch r {
		case '\\', '*', '?', '[':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	b.WriteByte('/')
	return b.String()
}

// gitignoreFile is the .gitignore line for the file at the vault-relative
// path rel, anchored and escaped as gitignoreDir does; trailing spaces,
// which git drops from a line, are escaped too.
func gitignoreFile(rel string) string {
	line := strings.TrimSuffix(gitignoreDir(rel), "/")
	trimmed := strings.TrimRight(line, " ")
	return trimmed + strings.Repeat(`\ `, len(line)-len(trimmed))
}

// INMaxTrackedBytes is the largest file of an IN folder git tracks, for a
// project with track_in_git (IMP-172): a bigger one stays out of the
// history, and of a push that could not carry it.
const INMaxTrackedBytes = 50 << 20

// bigINUnder returns, sorted, the files of bigIN in the IN folders of the
// projects listed.
func (s *Sync) bigINUnder(projects []string) []string {
	var out []string
	for p := range s.bigIN {
		for _, name := range projects {
			if strings.HasPrefix(p, name+"/"+vault.INFolder+"/") {
				out = append(out, p)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// unstageBigIN takes out of the commit being made the files over
// INMaxTrackedBytes that `git add -A` staged in the IN folders git tracks,
// and names them in the managed .gitignore from then on.
func (s *Sync) unstageBigIN() error {
	if s.projects == nil {
		return nil
	}
	_, tracked := s.projects.INFoldersForGit()
	if len(tracked) == 0 {
		return nil
	}
	args := []string{"diff", "--cached", "--name-only", "-z", "--diff-filter=AM", "--"}
	for _, name := range tracked {
		args = append(args, ":(literal)"+name+"/"+vault.INFolder)
	}
	out, err := s.capture("git", args...)
	if err != nil {
		return fmt.Errorf("staged IN files: %w", err)
	}
	var big []string
	for _, p := range strings.Split(out, "\x00") {
		if p == "" {
			continue
		}
		if st, err := os.Lstat(filepath.Join(s.vaultDir, filepath.FromSlash(p))); err == nil && st.Size() > INMaxTrackedBytes {
			big = append(big, p)
		}
	}
	if len(big) == 0 {
		return nil
	}
	rm := []string{"rm", "-q", "--cached", "--ignore-unmatch", "--"}
	for _, p := range big {
		rm = append(rm, ":(literal)"+p)
		if s.bigIN == nil {
			s.bigIN = map[string]bool{}
		}
		s.bigIN[p] = true
		log.Printf("gitsync: %s is over %d MiB: left out of git", p, INMaxTrackedBytes>>20)
	}
	if err := s.run("git", rm...); err != nil {
		return fmt.Errorf("untrack big IN files: %w", err)
	}
	if err := s.refreshGitignore(); err != nil {
		return fmt.Errorf("refresh gitignore: %w", err)
	}
	return s.run("git", "add", "--", ".gitignore")
}

// stripManagedBlock removes the gosidian-managed marker block (and everything
// between the markers) from a .gitignore body. Returns the user-managed
// remainder, possibly empty. Tolerant of mismatched markers: a stray begin
// without end (or vice versa) returns the input unchanged.
func stripManagedBlock(s string) string {
	lines := strings.Split(s, "\n")
	begin := -1
	end := -1
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if t == gitignoreManagedBegin && begin == -1 {
			begin = i
		} else if t == gitignoreManagedEnd && begin != -1 && end == -1 {
			end = i
		}
	}
	if begin == -1 || end == -1 || end < begin {
		return s
	}
	out := append([]string{}, lines[:begin]...)
	out = append(out, lines[end+1:]...)
	return strings.Join(out, "\n")
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
	committed, err := s.commit("")
	if err != nil || !committed || !s.cfg.Push {
		return err
	}
	return s.push(context.Background())
}

// commit stages what changed in the vault and commits it with msg (""
// gives the auto message), then tells the listeners. false: there was
// nothing to commit. The caller holds workMu.
func (s *Sync) commit(msg string) (bool, error) {
	// Refresh the managed block of .gitignore from the current projects state
	// before staging, so newly skipped projects are excluded from the next
	// commit (and ones unchecked become trackable again).
	if err := s.refreshGitignore(); err != nil {
		return false, fmt.Errorf("refresh gitignore: %w", err)
	}
	if err := s.untrackIgnored(); err != nil {
		return false, err
	}

	// Detect whether there is anything to commit.
	out, err := s.capture("git", "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("git status: %w", err)
	}
	if strings.TrimSpace(out) == "" {
		return false, nil
	}

	if err := s.run("git", "add", "-A"); err != nil {
		return false, fmt.Errorf("git add: %w", err)
	}
	if err := s.unstageBigIN(); err != nil {
		return false, err
	}
	if s.run("git", "diff", "--cached", "--quiet") == nil {
		return false, nil // all it staged was left out
	}

	if msg == "" {
		msg = "auto: " + time.Now().UTC().Format(time.RFC3339)
	}
	author := fmt.Sprintf("%s <%s>", s.cfg.AuthorName, s.cfg.AuthorEmail)
	if err := s.run("git", "commit", "-q", "--author", author, "-m", msg); err != nil {
		return false, fmt.Errorf("git commit: %w", err)
	}
	log.Printf("gitsync: committed %q", firstLine(msg))
	s.mu.Lock()
	listeners := s.onCommit
	s.mu.Unlock()
	if len(listeners) > 0 {
		if c, err := s.lastCommit(); err != nil {
			log.Printf("gitsync: reading the commit for its listeners: %v", err)
		} else {
			for _, fn := range listeners {
				fn(c)
			}
		}
	}
	return true, nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// untrackIgnored takes out of git's index, once per folder and process,
// what the managed .gitignore names but a commit already holds: ignoring
// does not untrack, so a state dir committed before the vault hid it, or a
// project committed before its git sync went off, kept going to the remote
// with every change (BUG-098, BUG-115 S5-5). The files stay on disk; the
// history keeps what was pushed.
func (s *Sync) untrackIgnored() error {
	want := map[string]bool{}
	if s.stateRel != "" {
		want[s.stateRel] = true
	}
	if s.projects != nil {
		for _, name := range s.projects.SkipNamesForGit() {
			want[name] = true
		}
		ignored, _ := s.projects.INFoldersForGit()
		for _, name := range ignored {
			want[name+"/"+vault.INFolder] = true
		}
	}
	for dir := range s.untracked {
		if !want[dir] {
			delete(s.untracked, dir) // tracked again: a later skip untracks again
		}
	}
	for dir := range want {
		if s.untracked[dir] {
			continue
		}
		if err := s.run("git", "rm", "-r", "-q", "--cached", "--ignore-unmatch", "--", ":(literal)"+dir); err != nil {
			return fmt.Errorf("untrack %s: %w", dir, err)
		}
		if s.untracked == nil {
			s.untracked = map[string]bool{}
		}
		s.untracked[dir] = true
	}
	return nil
}

// push pushes the branch and refs besides it (a tag of CommitNow). With
// --follow-tags it takes along the annotated tags of the branch that the
// remote lacks: a tag whose push failed goes with the next one.
func (s *Sync) push(ctx context.Context, refs ...string) error {
	var env []string
	if tok := s.authToken(); tok != "" {
		// A per-invocation header, so the token never lands in git config,
		// passed in the environment: on the command line any process of the
		// host read it in ps and /proc/<pid>/cmdline (BUG-115, S5-7).
		env = []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.extraheader", "GIT_CONFIG_VALUE_0=Authorization: token " + tok}
	}
	args := append([]string{"push", "-q", "--follow-tags", "origin", s.cfg.Branch}, refs...)
	if err := s.runEnv(ctx, env, "git", args...); err != nil {
		return fmt.Errorf("git push: %w", err)
	}
	return nil
}

// authToken resolves the PAT for `git push`. Precedence aligns with the
// gosidian convention `CLI > env > file > default`:
//  1. The env var named by cfg.TokenEnv, when non-empty. This preserves the
//     original behaviour and gives operators a one-shot override for
//     debugging or CI without touching the on-disk file.
//  2. The TokenStore on-disk value (file under <vault>/.gosidian), when
//     wired. Lets the web UI settings rotate the token without a restart.
//
// Empty result = unauthenticated push (let git fail naturally so the user
// sees a clear "credentials required" rather than a silent skip).
func (s *Sync) authToken() string {
	if s.cfg.TokenEnv != "" {
		if v := strings.TrimSpace(os.Getenv(s.cfg.TokenEnv)); v != "" {
			return v
		}
	}
	if s.tokens != nil {
		return strings.TrimSpace(s.tokens.Get())
	}
	return ""
}

// run executes a git command in the vault dir, propagating stdout/stderr
// to the server log via errors when it fails.
func (s *Sync) run(cmd string, args ...string) error {
	return s.runEnv(context.Background(), nil, cmd, args...)
}

// runEnv is run with env added to the process environment, killed when
// ctx ends: only for a push, which leaves no lock behind.
func (s *Sync) runEnv(ctx context.Context, env []string, cmd string, args ...string) error {
	c := exec.CommandContext(ctx, cmd, args...)
	c.Dir = s.vaultDir
	if len(env) > 0 {
		c.Env = append(os.Environ(), env...)
	}
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

// lastCommit reads HEAD: its SHA, date and changed files. -z keeps the
// paths as they are (no quoting of accents), -M finds the renames, --root
// lists the files of a first commit.
func (s *Sync) lastCommit() (CommitChanges, error) {
	head, err := s.capture("git", "log", "-1", "--format=%H %ct")
	if err != nil {
		return CommitChanges{}, err
	}
	sha, secs, ok := strings.Cut(strings.TrimSpace(head), " ")
	n, err := strconv.ParseInt(secs, 10, 64)
	if !ok || err != nil {
		return CommitChanges{}, fmt.Errorf("git log: %q", head)
	}
	out, err := s.capture("git", "diff-tree", "--no-commit-id", "--name-status", "-r", "-z", "-M", "--root", sha)
	if err != nil {
		return CommitChanges{}, err
	}
	return CommitChanges{SHA: sha, Date: time.Unix(n, 0).UTC(), Files: parseNameStatus(out)}, nil
}

// parseNameStatus reads `git diff-tree --name-status -z`: a status, then
// the path, or the two paths of a rename or a copy.
func parseNameStatus(out string) []Change {
	f := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
	var changes []Change
	for i := 0; i+1 < len(f); {
		st := f[i]
		if st == "" {
			i++
			continue
		}
		switch st[0] {
		case 'R', 'C':
			if i+2 >= len(f) {
				return changes
			}
			status := "renamed"
			if st[0] == 'C' {
				status = "added"
			}
			changes = append(changes, Change{Path: f[i+2], From: f[i+1], Status: status})
			i += 3
			continue
		case 'A':
			changes = append(changes, Change{Path: f[i+1], Status: "added"})
		case 'D':
			changes = append(changes, Change{Path: f[i+1], Status: "deleted"})
		default: // M, T: the content or the kind of the file
			changes = append(changes, Change{Path: f[i+1], Status: "modified"})
		}
		i += 2
	}
	return changes
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
