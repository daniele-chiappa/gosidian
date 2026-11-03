package v1

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/projects"
)

type restoreBody struct {
	Restored bool         `json:"restored"`
	Note     noteResponse `json:"note"`
	Commit   *historyItem `json:"commit"`
}

func (f *notesFixture) restore(t *testing.T, rel, body, bearer string) (int, restoreBody, string) {
	t.Helper()
	rec := f.req(t, http.MethodPost, "/api/v1/notes/"+rel+"/restore", body, bearer)
	var out restoreBody
	if rec.code == http.StatusOK {
		if err := json.Unmarshal([]byte(rec.body), &out); err != nil {
			t.Fatal(err)
		}
	}
	return rec.code, out, rec.body
}

func (f *notesFixture) etagOf(t *testing.T, rel string) string {
	t.Helper()
	rec := f.req(t, http.MethodGet, "/api/v1/notes/"+rel, "", f.bearer)
	if rec.code != http.StatusOK {
		t.Fatalf("read %s = %d", rel, rec.code)
	}
	return rec.headers.Get("ETag")
}

func restoreJSON(sha, ifMatch string) string {
	b, _ := json.Marshal(map[string]string{"sha": sha, "if_match": ifMatch})
	return string(b)
}

// A version restored is the note again, saved as any save is (the index,
// the audit log with the version in To), and committed at once in the
// writer's name, so the history shows it at its top (IMP-222, D5).
func TestRestore_WritesTheVersionAndCommits(t *testing.T) {
	f := newNotesFixture(t)
	s := f.withGit(t)
	f.commitNote(t, s, "p/a.md", "# A\n\nyesterday\n")
	f.commitNote(t, s, "p/a.md", "# A\n\ntoday\n")
	old := f.history(t, "p/a.md", f.bearer)[1].SHA

	code, out, raw := f.restore(t, "p/a.md", restoreJSON(old, f.etagOf(t, "p/a.md")), f.bearer)
	if code != http.StatusOK || !out.Restored {
		t.Fatalf("restore = %d %s", code, raw)
	}
	if out.Note.Content != "# A\n\nyesterday\n" {
		t.Errorf("note = %q", out.Note.Content)
	}
	if b, _ := os.ReadFile(filepath.Join(f.vaultRoot, "p/a.md")); string(b) != "# A\n\nyesterday\n" {
		t.Errorf("on disk = %q", b)
	}
	if rows, _ := f.idx.Search("yesterday", 10); len(rows) != 1 {
		t.Errorf("the index finds the restored text %d times, want 1", len(rows))
	}
	want := fmt.Sprintf("restore p/a.md to %s", old[:7])
	h := f.history(t, "p/a.md", f.bearer)
	if len(h) != 3 || h[0].Subject != want || h[0].Author != "owner <gosidian@localhost>" {
		t.Errorf("history top = %+v, want %q by owner", h[0], want)
	}
	if out.Commit == nil || out.Commit.SHA != h[0].SHA {
		t.Errorf("commit = %+v, want the top of the history %s", out.Commit, h[0].SHA)
	}
	entries, _ := f.auditLog.Tail(1)
	if len(entries) != 1 || entries[0].Action != audit.ActionRestore || entries[0].Path != "p/a.md" || entries[0].To != old || entries[0].Actor != "owner" {
		t.Errorf("audit = %+v", entries)
	}
}

// A version the same as the note writes nothing, and says so.
func TestRestore_SameVersion(t *testing.T) {
	f := newNotesFixture(t)
	s := f.withGit(t)
	f.commitNote(t, s, "p/a.md", "# A\n")
	top := f.history(t, "p/a.md", f.bearer)[0].SHA
	before, _ := f.auditLog.Tail(100)
	code, out, raw := f.restore(t, "p/a.md", restoreJSON(top, ""), f.bearer)
	if code != http.StatusOK || out.Restored || out.Commit != nil || out.Note.Content != "# A\n" {
		t.Errorf("same = %d %s", code, raw)
	}
	if after, _ := f.auditLog.Tail(100); len(after) != len(before) {
		t.Errorf("audit grew from %d to %d", len(before), len(after))
	}
	if n := len(f.history(t, "p/a.md", f.bearer)); n != 1 {
		t.Errorf("%d commits, want 1", n)
	}
}

// The note changed since the window read it: 412 with the etag now, as a
// save, and nothing written.
func TestRestore_StaleIfMatch(t *testing.T) {
	f := newNotesFixture(t)
	s := f.withGit(t)
	f.commitNote(t, s, "p/a.md", "# A\n\none\n")
	stale := f.etagOf(t, "p/a.md")
	f.commitNote(t, s, "p/a.md", "# A\n\ntwo\n")
	old := f.history(t, "p/a.md", f.bearer)[1].SHA
	code, _, raw := f.restore(t, "p/a.md", restoreJSON(old, stale), f.bearer)
	if code != http.StatusPreconditionFailed || !strings.Contains(raw, "current_etag") {
		t.Errorf("stale = %d %s, want 412 with current_etag", code, raw)
	}
	if b, _ := os.ReadFile(filepath.Join(f.vaultRoot, "p/a.md")); string(b) != "# A\n\ntwo\n" {
		t.Errorf("on disk = %q", b)
	}
}

// Who reads a note without writing it cannot restore it; a guest neither.
func TestRestore_ReaderOnly(t *testing.T) {
	f := newNotesFixture(t)
	s := f.withGit(t)
	f.commitNote(t, s, "p/a.md", "# A\n\none\n")
	f.commitNote(t, s, "p/a.md", "# A\n\ntwo\n")
	old := f.history(t, "p/a.md", f.bearer)[1].SHA
	f.setVisibility(t, "p", projects.VisibilityInternal)
	_, member := f.memberUser(t, "m1")
	if len(f.history(t, "p/a.md", member)) != 2 {
		t.Fatal("the member reads the history")
	}
	if code, _, raw := f.restore(t, "p/a.md", restoreJSON(old, ""), member); code != http.StatusForbidden {
		t.Errorf("reader = %d %s, want 403", code, raw)
	}
	if b, _ := os.ReadFile(filepath.Join(f.vaultRoot, "p/a.md")); string(b) != "# A\n\ntwo\n" {
		t.Errorf("on disk = %q", b)
	}
}

// No sha, one of no version of the note, or the version that deleted it:
// nothing to restore.
func TestRestore_NoSuchVersion(t *testing.T) {
	f := newNotesFixture(t)
	s := f.withGit(t)
	f.commitNote(t, s, "p/a.md", "# A\n")
	f.commitNote(t, s, "p/b.md", "# B\n")
	if err := os.Remove(filepath.Join(f.vaultRoot, "p/a.md")); err != nil {
		t.Fatal(err)
	}
	s.TriggerCommit()
	s.Flush()
	f.commitNote(t, s, "p/a.md", "# A again\n")
	other := f.history(t, "p/b.md", f.bearer)[0].SHA
	deleted := f.history(t, "p/a.md", f.bearer)[1].SHA

	for _, tc := range []struct {
		body string
		want int
	}{
		{`{}`, http.StatusBadRequest},
		{restoreJSON(other, ""), http.StatusNotFound},
		{restoreJSON("zzzzzzz", ""), http.StatusNotFound},
		{restoreJSON(deleted, ""), http.StatusUnprocessableEntity},
	} {
		if code, _, raw := f.restore(t, "p/a.md", tc.body, f.bearer); code != tc.want {
			t.Errorf("%s = %d %s, want %d", tc.body, code, raw, tc.want)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(f.vaultRoot, "p/a.md")); string(b) != "# A again\n" {
		t.Errorf("on disk = %q", b)
	}
}

// Without git sync there are no versions to restore.
func TestRestore_NoGitSync(t *testing.T) {
	f := newNotesFixture(t)
	f.seedNote(t, "p/a.md", "# A\n")
	if code, _, _ := f.restore(t, "p/a.md", restoreJSON("0123456789abcdef0123456789abcdef01234567", ""), f.bearer); code != http.StatusServiceUnavailable {
		t.Errorf("no git sync = %d, want 503", code)
	}
}
