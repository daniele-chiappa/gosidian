package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/config"
	"github.com/gosidian/gosidian/internal/gitsync"
	"github.com/gosidian/gosidian/internal/projects"
)

// withGit wires a git sync on the fixture's vault (IMP-222).
func (f *notesFixture) withGit(t *testing.T) *gitsync.Sync {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	s := gitsync.New(f.vaultRoot, config.GitConfig{Enabled: true, Branch: "main", AuthorName: "Gosidian", AuthorEmail: "gosidian@localhost", Debounce: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	f.router.deps.GitSync = s
	// As cmd/gosidian wires it: the audit log names the writers, and the
	// vault's locks the writes on their way (BUG-147).
	f.auditLog.OnWrite(func(e audit.Entry) { s.RecordWrite(string(e.Action), e.Path, e.To, e.Actor) })
	s.SetBusy(f.router.deps.Vault.PathBusy)
	return s
}

// commitNote writes a note and commits it at once.
func (f *notesFixture) commitNote(t *testing.T, s *gitsync.Sync, rel, content string) {
	t.Helper()
	f.seedNote(t, rel, content)
	s.TriggerCommit()
	s.Flush()
}

type historyItem struct {
	SHA     string `json:"sha"`
	Path    string `json:"path"`
	Author  string `json:"author"`
	Subject string `json:"subject"`
}

func (f *notesFixture) history(t *testing.T, rel, bearer string) []historyItem {
	t.Helper()
	rec := f.req(t, http.MethodGet, "/api/v1/notes/"+rel+"/history", "", bearer)
	if rec.code != http.StatusOK {
		t.Fatalf("history of %s = %d %s", rel, rec.code, rec.body)
	}
	var out struct {
		Items []historyItem `json:"items"`
	}
	if err := json.Unmarshal([]byte(rec.body), &out); err != nil {
		t.Fatal(err)
	}
	return out.Items
}

type versionBody struct {
	SHA     string  `json:"sha"`
	Path    string  `json:"path"`
	Subject string  `json:"subject"`
	Content *string `json:"content"`
	Diff    *string `json:"diff"`
	Same    bool    `json:"same"`
	Omitted string  `json:"omitted"`
	ETag    string  `json:"etag"`
}

func (f *notesFixture) version(t *testing.T, rel, sha, bearer string) (int, versionBody) {
	t.Helper()
	rec := f.req(t, http.MethodGet, "/api/v1/notes/"+rel+"/history/"+sha, "", bearer)
	var v versionBody
	if rec.code == http.StatusOK {
		if err := json.Unmarshal([]byte(rec.body), &v); err != nil {
			t.Fatal(err)
		}
	}
	return rec.code, v
}

// A version of a note gives its text, and the diff from it to the note as
// it is now: the lines it had that are gone, the lines added since (F-01).
// The newest version is the note itself: no difference.
func TestHistoryVersion_ContentAndDiff(t *testing.T) {
	f := newNotesFixture(t)
	s := f.withGit(t)
	f.commitNote(t, s, "p/a.md", "# A\n\nline one\nline two\n")
	f.commitNote(t, s, "p/a.md", "# A\n\nline one\nline 2\n")

	h := f.history(t, "p/a.md", f.bearer)
	if len(h) != 2 || h[0].Path != "p/a.md" || h[1].Path != "p/a.md" {
		t.Fatalf("history = %+v, want two versions of p/a.md", h)
	}
	code, old := f.version(t, "p/a.md", h[1].SHA, f.bearer)
	if code != http.StatusOK {
		t.Fatalf("old version = %d", code)
	}
	if old.SHA != h[1].SHA || old.Path != "p/a.md" || old.Content == nil || *old.Content != "# A\n\nline one\nline two\n" {
		t.Errorf("old version = %+v", old)
	}
	if old.Diff == nil || !strings.HasPrefix(*old.Diff, "@@ ") || !strings.Contains(*old.Diff, "\n-line two\n") || !strings.Contains(*old.Diff, "\n+line 2\n") || old.Same {
		t.Errorf("diff = %v, want the hunk from line two to line 2", old.Diff)
	}
	_, now := f.version(t, "p/a.md", h[0].SHA, f.bearer)
	if !now.Same || now.Diff == nil || *now.Diff != "" {
		t.Errorf("newest version = %+v, want same and no diff", now)
	}
	// The etag of the note the diff was made with: the if_match of a
	// restore from it.
	if want := strings.Trim(f.etagOf(t, "p/a.md"), `"`); old.ETag != want || now.ETag != want {
		t.Errorf("etag = %q, %q, want the note's %q", old.ETag, now.ETag, want)
	}
	// A short sha names the version as well.
	if code, v := f.version(t, "p/a.md", h[1].SHA[:7], f.bearer); code != http.StatusOK || v.SHA != h[1].SHA {
		t.Errorf("short sha = %d %+v", code, v)
	}
}

// A renamed note has its versions under the name it had then.
func TestHistoryVersion_Renamed(t *testing.T) {
	f := newNotesFixture(t)
	s := f.withGit(t)
	body := "# Old\n\nA text long enough for git to find the move as a rename.\n"
	f.commitNote(t, s, "p/old.md", body)
	if err := os.Rename(filepath.Join(f.vaultRoot, "p/old.md"), filepath.Join(f.vaultRoot, "p/new.md")); err != nil {
		t.Fatal(err)
	}
	s.TriggerCommit()
	s.Flush()

	h := f.history(t, "p/new.md", f.bearer)
	if len(h) != 2 || h[0].Path != "p/new.md" || h[1].Path != "p/old.md" {
		t.Fatalf("history = %+v, want p/new.md then p/old.md", h)
	}
	code, v := f.version(t, "p/new.md", h[1].SHA, f.bearer)
	if code != http.StatusOK || v.Path != "p/old.md" || v.Content == nil || *v.Content != body || !v.Same {
		t.Errorf("version before the rename = %d %+v", code, v)
	}
}

// A sha that is no version of this note, or no sha at all, is not found.
func TestHistoryVersion_UnknownAndMalformed(t *testing.T) {
	f := newNotesFixture(t)
	s := f.withGit(t)
	f.commitNote(t, s, "p/a.md", "# A\n")
	f.commitNote(t, s, "p/b.md", "# B\n")
	other := f.history(t, "p/b.md", f.bearer)[0].SHA
	for _, sha := range []string{other, "0123456789abcdef0123456789abcdef01234567", "zzzzzzz", "abc", "HEAD"} {
		if code, _ := f.version(t, "p/a.md", sha, f.bearer); code != http.StatusNotFound {
			t.Errorf("sha %q = %d, want 404", sha, code)
		}
	}
	// A note in a folder named history is still a note.
	f.seedNote(t, "p/history/2026.md", "# 2026\n")
	if rec := f.req(t, http.MethodGet, "/api/v1/notes/p/history/2026.md", "", f.bearer); rec.code != http.StatusOK {
		t.Errorf("p/history/2026.md = %d %s", rec.code, rec.body)
	}
}

// A note moved out of a project its reader cannot see shows none of the
// versions it had there, and the subject of the move does not name it.
func TestHistoryVersion_ProjectNotReadable(t *testing.T) {
	f := newNotesFixture(t)
	s := f.withGit(t)
	body := "# X\n\nA text long enough for git to find the move as a rename.\n"
	f.commitNote(t, s, "secret/x.md", body)
	if err := os.MkdirAll(filepath.Join(f.vaultRoot, "pub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(f.vaultRoot, "secret/x.md"), filepath.Join(f.vaultRoot, "pub/x.md")); err != nil {
		t.Fatal(err)
	}
	s.RecordWrite("rename", "secret/x.md", "pub/x.md", "owner")
	s.TriggerCommit()
	s.Flush()
	f.setVisibility(t, "pub", projects.VisibilityInternal)
	_, member := f.memberUser(t, "m1")

	all := f.history(t, "pub/x.md", f.bearer)
	if len(all) != 2 || all[0].Subject != "rename secret/x.md -> pub/x.md" {
		t.Fatalf("the owner's history = %+v", all)
	}
	h := f.history(t, "pub/x.md", member)
	if len(h) != 1 || h[0].Path != "pub/x.md" || strings.Contains(h[0].Subject, "secret") {
		t.Errorf("the member's history = %+v, want the move alone, without secret", h)
	}
	if code, _ := f.version(t, "pub/x.md", all[1].SHA, member); code != http.StatusNotFound {
		t.Errorf("the version in secret for the member = %d, want 404", code)
	}
	if code, v := f.version(t, "pub/x.md", all[0].SHA, member); code != http.StatusOK || strings.Contains(v.Subject, "secret") {
		t.Errorf("the move for the member = %d %+v", code, v)
	}
	if code, v := f.version(t, "pub/x.md", all[1].SHA, f.bearer); code != http.StatusOK || v.Path != "secret/x.md" {
		t.Errorf("the version in secret for the owner = %d %+v", code, v)
	}
}

// A version over 1 MiB, or one that is not text, gives neither its text
// nor a diff, and says why; so does the version in which the note was
// deleted.
func TestHistoryVersion_Omitted(t *testing.T) {
	f := newNotesFixture(t)
	s := f.withGit(t)
	f.commitNote(t, s, "p/big.md", "# Big\n\n"+strings.Repeat("x", 1<<20)+"\n")
	f.commitNote(t, s, "p/big.md", "# Big\n")
	f.commitNote(t, s, "p/bin.md", "# Bin\n\x00\x01\n")
	f.commitNote(t, s, "p/bin.md", "# Bin\n")
	f.commitNote(t, s, "p/gone.md", "# Gone\n")
	if err := os.Remove(filepath.Join(f.vaultRoot, "p/gone.md")); err != nil {
		t.Fatal(err)
	}
	s.TriggerCommit()
	s.Flush()
	f.commitNote(t, s, "p/gone.md", "# Back\n")

	for rel, want := range map[string]string{"p/big.md": "too_large", "p/bin.md": "not_text"} {
		h := f.history(t, rel, f.bearer)
		code, v := f.version(t, rel, h[1].SHA, f.bearer)
		if code != http.StatusOK || v.Content != nil || v.Diff != nil || v.Omitted != want {
			t.Errorf("%s = %d %+v, want omitted %s", rel, code, v, want)
		}
	}
	h := f.history(t, "p/gone.md", f.bearer)
	if len(h) != 3 {
		t.Fatalf("history of p/gone.md = %+v, want 3", h)
	}
	code, v := f.version(t, "p/gone.md", h[1].SHA, f.bearer)
	if code != http.StatusOK || v.Content != nil || v.Diff != nil || v.Omitted != "deleted" {
		t.Errorf("the deleted version = %d %+v", code, v)
	}
}

// Without git sync, or with it off, a version is unavailable (503).
func TestHistoryVersion_NoGitSync(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "p/a.md", "# A\n")
	sha := "0123456789abcdef0123456789abcdef01234567"
	if code, _ := f.version(t, "p/a.md", sha, f.bearer); code != http.StatusServiceUnavailable {
		t.Errorf("no git sync = %d, want 503", code)
	}
	f.router.deps.GitSync = gitsync.New(f.vaultRoot, config.GitConfig{})
	if code, _ := f.version(t, "p/a.md", sha, f.bearer); code != http.StatusServiceUnavailable {
		t.Errorf("git sync off = %d, want 503", code)
	}
}
