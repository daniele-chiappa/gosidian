package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/config"
	"github.com/gosidian/gosidian/internal/gitsync"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/vault"
)

// Who wrote: a web UI write by its username, an MCP write by the token's
// name and its account ("admin" for a CLI token), the name alone once the
// token is gone; only writes of notes and projects count.
func TestAuthorEvent(t *testing.T) {
	store, err := auth.Open(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	_, tok, err := store.Create("claude-cli", nil, []string{auth.ScopeRead, auth.ScopeWrite}, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	names := &authorNames{tokens: store}
	ts := time.Date(2026, 10, 4, 9, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		e    audit.Entry
		want string
		ok   bool
	}{
		{audit.Entry{TS: ts, Source: audit.SourceHTTP, Actor: "daniele", Action: audit.ActionUpdate, Path: "p/a.md"}, "daniele", true},
		{audit.Entry{TS: ts, Source: audit.SourceMCP, Token: tok.ID, Actor: "claude-cli@1a2b3c4d", Action: audit.ActionCreate, Path: "p/a.md"}, "claude-cli (admin)", true},
		{audit.Entry{TS: ts, Source: audit.SourceMCP, Token: "gone1234", Actor: "old-agent@99", Action: audit.ActionAppend, Path: "p/a.md"}, "old-agent", true},
		{audit.Entry{TS: ts, Source: audit.SourceMCP, Token: tok.ID, Actor: "claude-cli@1a2b3c4d", Action: audit.ActionRename, Path: "p/a.md", To: "p/b.md"}, "claude-cli (admin)", true},
		{audit.Entry{TS: ts, Source: audit.SourceMCP, Token: tok.ID, Action: audit.ActionUploadAttachment, Path: "p/attachments/x.png"}, "", false},
		{audit.Entry{TS: ts, Source: audit.SourceHTTP, Actor: "daniele", Action: audit.ActionTokenCreate, Path: "abcd"}, "", false},
		{audit.Entry{TS: ts, Source: audit.SourceAutomation, Actor: "automation", Action: audit.ActionCreate, Path: "p/handoffs/h.md"}, "automation", true},
		{audit.Entry{TS: ts, Source: audit.SourceHTTP, Actor: "daniele", Action: audit.ActionRestore, Path: "p/a.md", To: "0123456789abcdef0123456789abcdef01234567"}, "daniele", true},
	} {
		ev, ok := authorEvent(tc.e, names)
		if ok != tc.ok || ok && (ev.By != tc.want || ev.Action != string(tc.e.Action) || ev.Path != tc.e.Path || ev.To != tc.e.To || !ev.TS.Equal(ts)) {
			t.Errorf("%s %s: %+v %v, want by %q ok=%v", tc.e.Action, tc.e.Actor, ev, ok, tc.want, tc.ok)
		}
	}
}

// A restore from the trash is recorded as a create whose path is the trash
// id and whose To lists the notes restored: it reads as a write of the place
// restored, for git sync, and as a create of each note, by who restored it,
// for the authors (BUG-148). Before, git and the authors saw the id.
func TestAuthorEvents_ARestoreFromTheTrash(t *testing.T) {
	names := &authorNames{}
	ts := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	restore := func(id, to string) audit.Entry {
		return audit.Entry{TS: ts, Source: audit.SourceHTTP, Actor: "daniele", Action: audit.ActionCreate, Path: id, To: to}
	}
	for _, tc := range []struct {
		name   string
		e      audit.Entry
		origin string
		notes  []string
	}{
		{"a note", restore("1793650000000000000__p%2Fa.md", "p/a.md"), "p/a.md", []string{"p/a.md"}},
		{"a folder", restore("1793650000000000000__p%2Ff", "p/f/a.md,p/f/sub/b.md"), "p/f", []string{"p/f/a.md", "p/f/sub/b.md"}},
		{"a name with a comma", restore("1793650000000000000__p%2Ff", "p/f/a, b.md,p/f/c.md"), "p/f", []string{"p/f/a, b.md", "p/f/c.md"}},
		{"a project", restore("1793650000000000000__q", "q/n.md"), "q", []string{"q/n.md"}},
		{"a folder without notes", restore("1793650000000000000__p%2Ff", ""), "p/f", nil},
	} {
		origin, notes, ok := restoreFromTrash(tc.e)
		if !ok || origin != tc.origin || !slices.Equal(notes, tc.notes) {
			t.Errorf("%s: %q %q %v, want %q %q", tc.name, origin, notes, ok, tc.origin, tc.notes)
		}
		evs := authorEvents(tc.e, names)
		if len(evs) != len(tc.notes) {
			t.Errorf("%s: %d author events, want %d: %+v", tc.name, len(evs), len(tc.notes), evs)
			continue
		}
		for i, ev := range evs {
			if ev.Action != string(audit.ActionCreate) || ev.Path != tc.notes[i] || ev.By != "daniele" || !ev.TS.Equal(ts) {
				t.Errorf("%s: author event %+v, want daniele's create of %s", tc.name, ev, tc.notes[i])
			}
		}
	}
	// A create of a note is not a restore.
	plain := audit.Entry{TS: ts, Source: audit.SourceHTTP, Actor: "daniele", Action: audit.ActionCreate, Path: "p/a.md"}
	if _, _, ok := restoreFromTrash(plain); ok {
		t.Error("a create of a note reads as a restore")
	}
	if evs := authorEvents(plain, names); len(evs) != 1 || evs[0].Path != "p/a.md" {
		t.Errorf("a create of a note = %+v", evs)
	}
}

// A folder restored from the trash is committed in the name of who restored
// it, attachments included, with a subject that says so (BUG-148).
func TestWireGitWriters_ARestoreFromTheTrash(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	al, err := audit.Open(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	s := gitsync.New(dir, config.GitConfig{Enabled: true, Branch: "main", AuthorName: "gosidian", AuthorEmail: "bot@example.com", Debounce: time.Hour})
	wireGitWriters(s, al, &authorNames{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	for rel, body := range map[string]string{"p/f/a.md": "# A\n", "p/f/attachments/x.png": "png"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := al.Write(audit.Entry{Source: audit.SourceHTTP, Actor: "daniele", Action: audit.ActionCreate, Path: "1793650000000000000__p%2Ff", To: "p/f/a.md"}); err != nil {
		t.Fatal(err)
	}
	s.TriggerCommit()
	s.Flush()
	for _, rel := range []string{"p/f/a.md", "p/f/attachments/x.png"} {
		h, err := s.History(rel, 5)
		if err != nil {
			t.Fatal(err)
		}
		if len(h) != 1 || h[0].Author != "daniele <bot@example.com>" || h[0].Subject != "restore p/f from the trash" {
			t.Errorf("history of %s = %+v, want daniele's restore p/f from the trash", rel, h)
		}
	}
}

// An attachment uploaded is committed in the name of who uploaded it
// (BUG-148): the audit log named it, but git sync was not told, and it went
// in the auto commit. The authors of the index are for notes, and stay out.
func TestWireGitWriters_AnAttachment(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	al, err := audit.Open(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	s := gitsync.New(dir, config.GitConfig{Enabled: true, Branch: "main", AuthorName: "gosidian", AuthorEmail: "bot@example.com", Debounce: time.Hour})
	wireGitWriters(s, al, &authorNames{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "p", "attachments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "p", "attachments", "x.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := audit.Entry{Source: audit.SourceHTTP, Actor: "daniele", Action: audit.ActionUploadAttachment, Path: "p/attachments/x.png"}
	if err := al.Write(e); err != nil {
		t.Fatal(err)
	}
	s.TriggerCommit()
	s.Flush()
	h, err := s.History("p/attachments/x.png", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 1 || h[0].Author != "daniele <bot@example.com>" || h[0].Subject != "upload p/attachments/x.png" {
		t.Errorf("history = %+v, want daniele's upload p/attachments/x.png", h)
	}
	if evs := authorEvents(e, &authorNames{}); len(evs) != 0 {
		t.Errorf("an attachment in the authors: %+v", evs)
	}
}

// A project's flags changed (git sync off, an IN folder) ask for a commit:
// it takes the project out of git or tracks it again, which waited for the
// next write of a note (BUG-148).
func TestWireGitWriters_AProjectsFlagsAskForACommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	al, err := audit.Open(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	pstore, err := projects.Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	s := gitsync.New(dir, config.GitConfig{Enabled: true, Branch: "main", AuthorName: "gosidian", AuthorEmail: "bot@example.com", Debounce: 50 * time.Millisecond})
	s.SetProjects(pstore)
	wireGitWriters(s, al, &authorNames{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := os.MkdirAll(filepath.Join(dir, "p"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "p", "a.md"), []byte("# A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	s.TriggerCommit()
	s.Flush()
	if err := pstore.Set("p", projects.Flags{SkipGitSync: true}); err != nil {
		t.Fatal(err)
	}
	if err := al.Write(audit.Entry{Source: audit.SourceHTTP, Actor: "daniele", Action: audit.ActionProjectFlagsUpdate, Path: "p"}); err != nil {
		t.Fatal(err)
	}
	tracked := func() string {
		out, err := exec.Command("git", "-C", dir, "ls-files", "--", "p").Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && tracked() != "" {
		time.Sleep(20 * time.Millisecond)
	}
	if got := tracked(); got != "" {
		t.Errorf("p still in git after its git sync went off: %q", got)
	}
}

// A note written through the web UI is committed in the name of its
// account, as modified_by shows it, with the configured email (IMP-222).
func TestWireGitWriters(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	vault := t.TempDir()
	al, err := audit.Open(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	s := gitsync.New(vault, config.GitConfig{Enabled: true, Branch: "main", AuthorName: "gosidian", AuthorEmail: "bot@example.com", Debounce: time.Hour})
	wireGitWriters(s, al, &authorNames{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(vault, "p"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vault, "p", "a.md"), []byte("# A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := al.Write(audit.Entry{Source: audit.SourceHTTP, Actor: "daniele", Action: audit.ActionCreate, Path: "p/a.md"}); err != nil {
		t.Fatal(err)
	}
	s.TriggerCommit()
	s.Flush()
	h, err := s.History("p/a.md", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 1 || h[0].Author != "daniele <bot@example.com>" || h[0].Subject != "create p/a.md" {
		t.Errorf("history = %+v, want daniele's create p/a.md", h)
	}
}

// A note whose writer still holds its lock waits for the commit after its
// record: the git sync asks the vault's locks (BUG-147). Before, the commit
// took the note as nobody's, "auto:".
func TestWireGitWriters_AWriteOnItsWayWaits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	al, err := audit.Open(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	v := vault.New(dir)
	s := gitsync.New(dir, config.GitConfig{Enabled: true, Branch: "main", AuthorName: "gosidian", AuthorEmail: "bot@example.com", Debounce: time.Hour})
	wireGitWriters(s, al, &authorNames{}, v)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := s.Start(ctx); err != nil {
		t.Fatal(err)
	}
	unlock := v.LockPath("p/a.md")
	if err := os.MkdirAll(filepath.Join(dir, "p"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "p", "a.md"), []byte("# A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.TriggerCommit()
	s.Flush()
	if h, err := s.History("p/a.md", 5); err != nil || len(h) != 0 {
		t.Fatalf("history = %+v %v, want none while the write is on its way", h, err)
	}
	if err := al.Write(audit.Entry{Source: audit.SourceHTTP, Actor: "daniele", Action: audit.ActionCreate, Path: "p/a.md"}); err != nil {
		t.Fatal(err)
	}
	unlock()
	s.TriggerCommit()
	s.Flush()
	h, err := s.History("p/a.md", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 1 || h[0].Author != "daniele <bot@example.com>" || h[0].Subject != "create p/a.md" {
		t.Errorf("history = %+v, want daniele's create p/a.md", h)
	}
}
