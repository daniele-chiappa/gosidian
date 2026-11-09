package mcp

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

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/authz"
	"github.com/gosidian/gosidian/internal/config"
	"github.com/gosidian/gosidian/internal/gitsync"
	"github.com/gosidian/gosidian/internal/pipeline"
	"github.com/gosidian/gosidian/internal/projects"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// startGit gives the server a git sync on its vault.
func startGit(t *testing.T, s *Server) *gitsync.Sync {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	g := gitsync.New(s.vault.Root, config.GitConfig{Enabled: true, Branch: "main", AuthorName: "Gosidian", AuthorEmail: "gosidian@localhost", Debounce: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}
	s.SetGit(g)
	s.index.SetSearchText(g.ValidText)
	return g
}

func commitFile(t *testing.T, s *Server, rel, body string) {
	t.Helper()
	writeVaultFile(t, s.vault.Root, rel, body)
	if err := s.git.CommitWrites(); err != nil {
		t.Fatal(err)
	}
}

type versionsResult struct {
	Tags []struct {
		Tag, Label, SHA, By, Path string
	} `json:"tags"`
	Valid   *noteValid `json:"valid"`
	MCP     string     `json:"mcp"`
	Commits []struct {
		SHA, Author, Subject string
	} `json:"commits"`
	CanAdmin bool `json:"can_admin"`
}

func listVersions(t *testing.T, s *Server, ctx context.Context, rel string) versionsResult {
	t.Helper()
	r, err := s.handleVersions(ctx, call(map[string]any{"path": rel}))
	if err != nil {
		t.Fatal(err)
	}
	var out versionsResult
	decodeResult(t, r, &out)
	return out
}

func readAs(t *testing.T, s *Server, ctx context.Context, args map[string]any) noteContent {
	t.Helper()
	r, err := s.handleGet(ctx, call(args))
	if err != nil {
		t.Fatal(err)
	}
	var out noteContent
	decodeResult(t, r, &out)
	return out
}

// Through the tool, the history that MCP did not have, a tag on the note as
// it is or on a version of the list, and the choice of the valid version,
// committed at once. memory_get stays on the draft, says so, and reads the
// valid version on request, with an etag no write takes.
func TestVersions_TagChooseAndRead(t *testing.T) {
	s, _, _ := newTestServer(t)
	startGit(t, s)
	ctx := context.Background()
	commitFile(t, s, "p/a.md", "---\ntitle: First\n---\nV1 body\n")
	commitFile(t, s, "p/a.md", "---\ntitle: Second\n---\nV2 body\n")

	list := listVersions(t, s, ctx, "p/a.md")
	if len(list.Commits) != 2 || len(list.Tags) != 0 || list.Valid != nil || !list.CanAdmin {
		t.Fatalf("list %+v", list)
	}
	r, err := s.handleVersions(ctx, call(map[string]any{"path": "p/a.md", "action": "tag", "label": "V1", "sha": list.Commits[1].SHA, "message": "first"}))
	if err != nil {
		t.Fatal(err)
	}
	var tagged struct{ Tag, Label, SHA string }
	decodeResult(t, r, &tagged)
	if tagged.Tag != "note/p/a.md/V1" || tagged.SHA != list.Commits[1].SHA {
		t.Fatalf("tag %+v", tagged)
	}

	r, err = s.handleVersions(ctx, call(map[string]any{"path": "p/a.md", "action": "valid", "tag": "V1"}))
	if err != nil {
		t.Fatal(err)
	}
	var chosen struct {
		ETag  string     `json:"etag"`
		Valid *noteValid `json:"valid"`
	}
	decodeResult(t, r, &chosen)
	if chosen.Valid == nil || chosen.Valid.Label != "V1" || chosen.Valid.Same || chosen.ETag == "" {
		t.Fatalf("valid %+v", chosen)
	}
	list = listVersions(t, s, ctx, "p/a.md")
	if len(list.Tags) != 1 || list.Tags[0].Label != "V1" || list.Valid == nil || len(list.Commits) != 3 {
		t.Fatalf("list after %+v", list)
	}

	draft := readAs(t, s, ctx, map[string]any{"path": "p/a.md"})
	if !strings.Contains(draft.Content, "V2 body") || draft.Valid == nil || draft.Valid.Label != "V1" || !strings.Contains(draft.Hint, "this is the draft") {
		t.Fatalf("draft %+v", draft)
	}
	at := readAs(t, s, ctx, map[string]any{"path": "p/a.md", "version": "valid"})
	if !strings.Contains(at.Content, "V1 body") || at.Title != "First" || strings.Contains(at.Hint, "draft") || at.ETag == draft.ETag {
		t.Fatalf("at the valid version %+v", at)
	}
	if bytag := readAs(t, s, ctx, map[string]any{"path": "p/a.md", "version": "note/p/a.md/V1"}); bytag.Content != at.Content {
		t.Errorf("by tag %+v", bytag)
	}
	r, err = s.handleUpdate(ctx, call(map[string]any{"path": "p/a.md", "content": at.Content, "if_match": at.ETag}))
	if err != nil {
		t.Fatal(err)
	}
	if !r.IsError {
		t.Error("a write took the etag of a version")
	}

	r, err = s.handleVersions(ctx, call(map[string]any{"path": "p/a.md", "action": "clear"}))
	if err != nil {
		t.Fatal(err)
	}
	resultText(t, r)
	if n := readAs(t, s, ctx, map[string]any{"path": "p/a.md"}); n.Valid != nil || n.Hint != "" || strings.Contains(n.Content, "valid_version") {
		t.Errorf("after clear %+v", n)
	}
}

// A note with a valid version whose tag is gone reads as it is, and says so.
func TestVersions_MissingAndNoGit(t *testing.T) {
	s, _, dir := newTestServer(t)
	startGit(t, s)
	ctx := context.Background()
	commitFile(t, s, "p/a.md", "---\ntitle: T\n---\nbody\n")
	if _, err := s.git.TagNote(ctx, "p/a.md", "", "V1", "", "t"); err != nil {
		t.Fatal(err)
	}
	r, err := s.handleVersions(ctx, call(map[string]any{"path": "p/a.md", "action": "valid", "tag": "V1"}))
	if err != nil {
		t.Fatal(err)
	}
	resultText(t, r)
	if out, err := exec.Command("git", "-C", dir, "tag", "-d", "note/p/a.md/V1").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	for _, args := range []map[string]any{{"path": "p/a.md"}, {"path": "p/a.md", "version": "valid"}} {
		n := readAs(t, s, ctx, args)
		if n.Valid == nil || !n.Valid.Missing || !strings.Contains(n.Content, "body") {
			t.Errorf("%v: %+v", args, n)
		}
	}
	if n := readAs(t, s, ctx, map[string]any{"path": "p/a.md"}); !strings.Contains(n.Hint, "cannot be read") {
		t.Errorf("hint %q", n.Hint)
	}
	r, err = s.handleGet(ctx, call(map[string]any{"path": "p/a.md", "version": "note/none/V1"}))
	if err != nil {
		t.Fatal(err)
	}
	expectError(t, r)
	r, err = s.handleGet(ctx, call(map[string]any{"path": "p/a.md", "version": "valid", "transfer": "http"}))
	if err != nil {
		t.Fatal(err)
	}
	expectError(t, r)

	s.SetGit(nil)
	if n := readAs(t, s, ctx, map[string]any{"path": "p/a.md"}); n.Valid == nil || !n.Valid.Missing {
		t.Errorf("no git sync: %+v", n)
	}
	r, err = s.handleVersions(ctx, call(map[string]any{"path": "p/a.md"}))
	if err != nil {
		t.Fatal(err)
	}
	if got := expectError(t, r); !strings.Contains(got, "git sync") {
		t.Errorf("no git sync: %s", got)
	}
	r, err = s.handleGet(ctx, call(map[string]any{"path": "p/a.md", "version": "valid"}))
	if err != nil {
		t.Fatal(err)
	}
	expectError(t, r)
}

// The versions are the project admin's. A token acts as the account behind
// it, whatever its projects and scopes; one of no account must be for the
// whole vault. Whoever cannot do it through the tool cannot do it with a
// write either.
func TestVersions_OnlyTheProjectAdmin(t *testing.T) {
	s, _, dir := newTestServer(t)
	startGit(t, s)
	store, err := projects.Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.SetProjects(store)
	for id, level := range map[string]string{"writer": "write", "admin": "admin"} {
		if err := store.SetMember("p", id, level); err != nil {
			t.Fatal(err)
		}
	}
	s.SetPrincipalResolver(func(id string) (authz.Principal, bool) {
		return authz.Principal{UserID: id, Role: "member"}, id != "gone"
	})
	bg := context.Background()
	body := "---\ntitle: T\n---\nbody\n"
	commitFile(t, s, "p/a.md", body)
	if _, err := s.git.TagNote(bg, "p/a.md", "", "V1", "", "t"); err != nil {
		t.Fatal(err)
	}

	token := func(owner string, projectList string, scopes ...string) context.Context {
		return ctxWithToken(&auth.Token{ID: "t-" + owner, Name: "t", OwnerUserID: owner, Project: projectList, Scopes: scopes})
	}
	rw := []string{auth.ScopeRead, auth.ScopeWrite}
	for i, c := range []struct {
		name string
		ctx  context.Context
		ok   bool
	}{
		{"the admin", token("admin", "p", rw...), true},
		{"the admin, every project", token("admin", "", rw...), true},
		{"who writes", token("writer", "p", rw...), false},
		{"a read-only token of the admin", token("admin", "p", auth.ScopeRead), false},
		{"an account that is gone", token("gone", "", rw...), false},
		{"no account, one project", token("", "p", rw...), false},
		{"no account, the whole vault", token("", "", rw...), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, err := s.handleVersions(c.ctx, call(map[string]any{"path": "p/a.md", "action": "valid", "tag": "V1"}))
			if err != nil {
				t.Fatal(err)
			}
			if r.IsError == c.ok {
				t.Fatalf("valid: error %v, %+v", r.IsError, r.Content)
			}
			r, err = s.handleVersions(c.ctx, call(map[string]any{"path": "p/a.md", "action": "tag", "label": fmt.Sprintf("L%d", i)}))
			if err != nil {
				t.Fatal(err)
			}
			if r.IsError == c.ok {
				t.Fatalf("tag: error %v, %+v", r.IsError, r.Content)
			}
			if !c.ok {
				if b, _ := os.ReadFile(filepath.Join(dir, "p/a.md")); strings.Contains(string(b), "valid_version") {
					t.Fatalf("the note changed: %q", b)
				}
				return
			}
			r, err = s.handleVersions(c.ctx, call(map[string]any{"path": "p/a.md", "action": "clear"}))
			if err != nil {
				t.Fatal(err)
			}
			resultText(t, r)
		})
	}

	// The writer's way around: any write leaves it as it was.
	if _, err := s.handleVersions(token("admin", "p", rw...), call(map[string]any{"path": "p/a.md", "action": "valid", "tag": "V1"})); err != nil {
		t.Fatal(err)
	}
	writer := token("writer", "p", rw...)
	if v := listVersions(t, s, writer, "p/a.md"); v.CanAdmin || v.Valid == nil {
		t.Errorf("the writer's list %+v", v)
	}
	r, err := s.handleEdit(writer, call(map[string]any{"path": "p/a.md", "old_string": "valid_version: note/p/a.md/V1", "new_string": "valid_version: note/p/a.md/V9"}))
	if err != nil {
		t.Fatal(err)
	}
	resultText(t, r)
	r, err = s.handleUpdate(writer, call(map[string]any{"path": "p/a.md", "content": body + "more\n"}))
	if err != nil {
		t.Fatal(err)
	}
	resultText(t, r)
	got, _ := os.ReadFile(filepath.Join(dir, "p/a.md"))
	if !strings.Contains(string(got), "valid_version: note/p/a.md/V1\n") || !strings.Contains(string(got), "more\n") {
		t.Errorf("after the writer's writes: %q", got)
	}
}

// A commit from a project the reader cannot see does not say its name, nor
// does a tag on a version there come along.
func TestVersions_ListLeavesOutWhatTheReaderCannotSee(t *testing.T) {
	s, _, dir := newTestServer(t)
	startGit(t, s)
	bg := context.Background()
	body := "first version of the note\nwith some lines\nto follow\n"
	commitFile(t, s, "secret/x.md", body)
	if _, err := s.git.TagNote(bg, "secret/x.md", "", "V1", "", "t"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "pub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "secret/x.md"), filepath.Join(dir, "pub/x.md")); err != nil {
		t.Fatal(err)
	}
	s.git.RecordWrite("rename", "secret/x.md", "pub/x.md", "owner")
	if err := s.git.CommitWrites(); err != nil {
		t.Fatal(err)
	}
	ctx := ctxWithToken(&auth.Token{ID: "t", Name: "t", Project: "pub", Scopes: []string{auth.ScopeRead}})
	list := listVersions(t, s, ctx, "pub/x.md")
	if len(list.Tags) != 0 || len(list.Commits) != 1 || strings.Contains(list.Commits[0].Subject, "secret") {
		t.Errorf("list %+v", list)
	}
	r, err := s.handleVersions(ctx, call(map[string]any{"path": "secret/x.md"}))
	if err != nil {
		t.Fatal(err)
	}
	expectError(t, r)
	all := listVersions(t, s, bg, "pub/x.md")
	if len(all.Tags) != 1 || len(all.Commits) != 2 {
		t.Errorf("the admin's list %+v", all)
	}

	// Chosen as the valid version it is what the note shows, to who reads
	// the note; by its name it stays out of the reader's sight.
	r, err = s.handleVersions(bg, call(map[string]any{"path": "pub/x.md", "action": "valid", "tag": "V1"}))
	if err != nil {
		t.Fatal(err)
	}
	resultText(t, r)
	r, err = s.handleUpdate(bg, call(map[string]any{"path": "pub/x.md", "content": "draft\n"}))
	if err != nil {
		t.Fatal(err)
	}
	resultText(t, r)
	if n := readAs(t, s, ctx, map[string]any{"path": "pub/x.md"}); n.Valid == nil || n.Valid.Missing || n.Valid.Same {
		t.Errorf("the reader's note %+v", n)
	}
	if n := readAs(t, s, ctx, map[string]any{"path": "pub/x.md", "version": "valid"}); n.Content != body {
		t.Errorf("the reader's version %+v", n)
	}
	r, err = s.handleGet(ctx, call(map[string]any{"path": "pub/x.md", "version": "note/secret/x.md/V1"}))
	if err != nil {
		t.Fatal(err)
	}
	expectError(t, r)
}

// What the tool refuses.
func TestVersions_Refusals(t *testing.T) {
	s, _, _ := newTestServer(t)
	startGit(t, s)
	ctx := context.Background()
	commitFile(t, s, "p/a.md", "one\n")
	commitFile(t, s, "p/b.md", "other\n")
	if _, err := s.git.TagNote(ctx, "p/a.md", "", "V1", "", "t"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.git.TagNote(ctx, "p/b.md", "", "B1", "", "t"); err != nil {
		t.Fatal(err)
	}
	commitFile(t, s, "p/a.md", "two\n")
	for _, args := range []map[string]any{
		{"path": "p/a.md", "action": "nope"},
		{"path": "p/a.md", "action": "tag"},
		{"path": "p/a.md", "action": "tag", "label": "a b"},
		{"path": "p/a.md", "action": "tag", "label": "V1"},
		{"path": "p/a.md", "action": "tag", "label": "V9", "sha": "0123456789"},
		{"path": "p/a.md", "action": "valid"},
		{"path": "p/a.md", "action": "valid", "tag": "B1"},
		{"path": "p/a.md", "action": "valid", "tag": "V1", "if_match": "stale"},
		{"path": "p/missing.md"},
	} {
		r, err := s.handleVersions(ctx, call(args))
		if err != nil {
			t.Fatal(err)
		}
		if !r.IsError {
			t.Errorf("%v accepted", args)
		}
	}
}

// After a GC, the images of the valid version are still there, though the
// draft names them no more, and what is free goes. A version chosen after
// its images went through the GC brings them back (IMP-201).
func TestVersions_GCKeepsTheValidVersion(t *testing.T) {
	s, _, dir := newTestServer(t)
	startGit(t, s)
	ctx := context.Background()
	png := "\x89PNG fake"
	writeVaultFile(t, dir, "p/attachments/kept.png", png)
	writeVaultFile(t, dir, "p/attachments/free.png", png+"2")
	writeVaultFile(t, dir, "p/attachments/older.png", png+"3")
	commitFile(t, s, "p/a.md", "---\ntitle: T\n---\n![x](attachments/older.png)\n")
	if _, err := s.git.TagNote(ctx, "p/a.md", "", "V0", "", "t"); err != nil {
		t.Fatal(err)
	}
	commitFile(t, s, "p/a.md", "---\ntitle: T\n---\n![x](attachments/kept.png)\n")
	if _, err := s.git.TagNote(ctx, "p/a.md", "", "V1", "", "t"); err != nil {
		t.Fatal(err)
	}
	r, err := s.handleVersions(ctx, call(map[string]any{"path": "p/a.md", "action": "valid", "tag": "V1"}))
	if err != nil {
		t.Fatal(err)
	}
	resultText(t, r)
	r, err = s.handleUpdate(ctx, call(map[string]any{"path": "p/a.md", "content": "---\ntitle: T\n---\nno image in the draft\n"}))
	if err != nil {
		t.Fatal(err)
	}
	resultText(t, r)

	gone := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, "p/attachments", name))
		return os.IsNotExist(err)
	}
	r, err = s.handleGCAttachments(ctx, call(map[string]any{"project": "p", "dry_run": false, "min_age_hours": float64(0)}))
	if err != nil {
		t.Fatal(err)
	}
	resultText(t, r)
	if gone("kept.png") || !gone("free.png") || !gone("older.png") {
		t.Fatalf("after the GC: kept gone %v, free gone %v, older gone %v", gone("kept.png"), gone("free.png"), gone("older.png"))
	}

	// V0 lost its image to the GC: choosing it brings the image back.
	r, err = s.handleVersions(ctx, call(map[string]any{"path": "p/a.md", "action": "valid", "tag": "V0"}))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Restored []string `json:"restored"`
	}
	decodeResult(t, r, &out)
	if len(out.Restored) != 1 || out.Restored[0] != "p/attachments/older.png" || gone("older.png") {
		t.Fatalf("restored %+v", out)
	}
}

// Through MCP too, the search finds the valid version and not the draft,
// and a rename with no commit yet changes nothing (IMP-201).
func TestVersions_SearchReadsTheValidVersion(t *testing.T) {
	s, _, _ := newTestServer(t)
	startGit(t, s)
	ctx := context.Background()
	r, err := s.handleCreate(ctx, call(map[string]any{"path": "p/a.md", "content": "---\ntitle: T\n---\nvalidated wording\n"}))
	if err != nil {
		t.Fatal(err)
	}
	resultText(t, r)
	for _, a := range []map[string]any{{"action": "tag", "label": "V1"}, {"action": "valid", "tag": "V1"}} {
		a["path"] = "p/a.md"
		if r, err = s.handleVersions(ctx, call(a)); err != nil {
			t.Fatal(err)
		}
		resultText(t, r)
	}
	r, err = s.handleUpdate(ctx, call(map[string]any{"path": "p/a.md", "content": "---\ntitle: T\n---\nunapproved wording\n"}))
	if err != nil {
		t.Fatal(err)
	}
	resultText(t, r)
	check := func(path string) {
		t.Helper()
		for q, found := range map[string]bool{"validated": true, "unapproved": false} {
			r, err := s.handleSearch(ctx, call(map[string]any{"query": q}))
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(resultText(t, r), path); got != found {
				t.Errorf("%q found at %s: %v, want %v", q, path, got, found)
			}
		}
	}
	check("p/a.md")
	r, err = s.handleRenameNote(ctx, call(map[string]any{"from": "p/a.md", "to": "p/b.md"}))
	if err != nil {
		t.Fatal(err)
	}
	resultText(t, r)
	check("p/b.md")
	if n := readAs(t, s, ctx, map[string]any{"path": "p/b.md"}); n.Valid == nil || n.Valid.Missing {
		t.Errorf("the note after the rename: %+v", n.Valid)
	}
}

// A pipeline reads the version it asks for: the valid one, or a note's text
// when it has none, or a tag of its project. Its etag writes nothing, and
// the images and the frontmatter are the version's (IMP-201).
func TestPipelineHost_ReadNoteAt(t *testing.T) {
	s, _, _ := newTestServer(t)
	startGit(t, s)
	ctx := context.Background()
	h := pipelineHost{s: s}
	commitFile(t, s, "p/a.md", "---\ntitle: First\n---\nvalidated text\n")
	if _, err := s.git.TagNote(ctx, "p/a.md", "", "V1", "", "t"); err != nil {
		t.Fatal(err)
	}
	r, err := s.handleVersions(ctx, call(map[string]any{"path": "p/a.md", "action": "valid", "tag": "V1"}))
	if err != nil {
		t.Fatal(err)
	}
	resultText(t, r)
	r, err = s.handleUpdate(ctx, call(map[string]any{"path": "p/a.md", "content": "---\ntitle: Second\n---\nunapproved text\n"}))
	if err != nil {
		t.Fatal(err)
	}
	resultText(t, r)
	commitFile(t, s, "p/plain.md", "plain text\n")

	draft, err := h.ReadNote("p", "p/a.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, at := range []string{"valid", "note/p/a.md/V1"} {
		n, err := h.ReadNoteAt("p", "p/a.md", at)
		if err != nil {
			t.Fatal(err)
		}
		if n.Title != "First" || !strings.Contains(n.Body, "validated") || n.ETag == draft.ETag || n.Path != "p/a.md" {
			t.Errorf("at %s: %+v", at, n)
		}
	}
	if n, err := h.ReadNoteAt("p", "p/plain.md", "valid"); err != nil || !strings.Contains(n.Body, "plain text") {
		t.Errorf("a note with no valid version: %+v %v", n, err)
	}
	if _, err := h.ReadNoteAt("p", "p/a.md", "note/none/V1"); err == nil {
		t.Error("a tag that is not there")
	}
	if _, err := h.ReadNoteAt("q", "p/a.md", "valid"); err == nil {
		t.Error("a note of another project")
	}
	if _, err := h.ReadNoteAt("p", "p/missing.md", "valid"); !errors.Is(err, pipeline.ErrNoNote) {
		t.Errorf("no such note: %v", err)
	}
	s.SetGit(nil)
	if n, err := h.ReadNoteAt("p", "p/a.md", "valid"); err != nil || !strings.Contains(n.Body, "unapproved") {
		t.Errorf("no git sync, valid: %+v %v", n, err)
	}
	if _, err := h.ReadNoteAt("p", "p/a.md", "note/p/a.md/V1"); err == nil {
		t.Error("no git sync, a tag")
	}
}

// A note chooses what who calls over MCP reads, while it changes (IMP-228):
// every tool that reads it gives that version and says so, the draft is a
// parameter away, and no whole text goes over the draft by mistake.
func TestVersions_WhatTheNoteGivesMCP(t *testing.T) {
	s, _, _ := newTestServer(t)
	startGit(t, s)
	ctx := context.Background()
	commitFile(t, s, "p/a.md", "---\ntitle: First\n---\n# H\n\nV1 body\n")
	tagged, err := s.handleVersions(ctx, call(map[string]any{"path": "p/a.md", "action": "tag", "label": "V1"}))
	if err != nil {
		t.Fatal(err)
	}
	resultText(t, tagged)
	commitFile(t, s, "p/a.md", "---\ntitle: Second\n---\n# H\n\nthe draft\n# Only here\n")

	choose := func(tag string) {
		t.Helper()
		r, err := s.handleVersions(ctx, call(map[string]any{"path": "p/a.md", "action": "mcp", "tag": tag}))
		if err != nil {
			t.Fatal(err)
		}
		resultText(t, r)
	}
	body := func(r map[string]any) string { return fmt.Sprint(r["content"]) }
	tool := func(h func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error), args map[string]any) map[string]any {
		t.Helper()
		r, err := h(ctx, call(args))
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		decodeResult(t, r, &out)
		return out
	}

	if n := readAs(t, s, ctx, map[string]any{"path": "p/a.md"}); !strings.Contains(n.Content, "the draft") || n.Version != "" {
		t.Fatalf("without a choice %+v", n)
	}
	choose("V1")
	if got := listVersions(t, s, ctx, "p/a.md"); got.MCP != "note/p/a.md/V1" {
		t.Errorf("list %+v", got)
	}
	n := readAs(t, s, ctx, map[string]any{"path": "p/a.md"})
	if !strings.Contains(n.Content, "V1 body") || n.Version != "V1" || n.Title != "First" || !strings.Contains(n.Hint, "version:\"draft\"") || strings.Contains(n.Hint, "this is the draft") {
		t.Fatalf("the version %+v", n)
	}
	sec := tool(s.handleGetSection, map[string]any{"path": "p/a.md", "heading": "H"})
	if !strings.Contains(body(sec), "V1 body") || sec["version"] != "V1" {
		t.Errorf("section %+v", sec)
	}
	if out := tool(s.handleGetOutline, map[string]any{"path": "p/a.md"}); strings.Contains(fmt.Sprint(out["headings"]), "Only here") || out["version"] != "V1" {
		t.Errorf("outline %+v", out)
	}
	if out := tool(s.handleGetFrontmatter, map[string]any{"path": "p/a.md"}); !strings.Contains(fmt.Sprint(out["raw"]), "First") || out["version"] != "V1" {
		t.Errorf("frontmatter %+v", out)
	}
	batch := tool(s.handleBatchGet, map[string]any{"paths": []any{"p/a.md"}})
	if got := fmt.Sprint(batch["results"]); !strings.Contains(got, "V1 body") || !strings.Contains(got, "version:V1") {
		t.Errorf("batch %s", got)
	}

	draft := readAs(t, s, ctx, map[string]any{"path": "p/a.md", "version": "draft"})
	if !strings.Contains(draft.Content, "the draft") || draft.Version != "" {
		t.Fatalf("draft %+v", draft)
	}

	// The etag of the version does not write, nor does a whole text without one.
	r, err := s.handleUpdate(ctx, call(map[string]any{"path": "p/a.md", "content": n.Content, "if_match": n.ETag}))
	if err != nil {
		t.Fatal(err)
	}
	if !r.IsError {
		t.Error("a write took the etag of a version")
	}
	r, err = s.handleUpdate(ctx, call(map[string]any{"path": "p/a.md", "content": n.Content}))
	if err != nil {
		t.Fatal(err)
	}
	if !r.IsError || !strings.Contains(callToolResultText(r), "version:\"draft\"") {
		t.Errorf("a whole text without if_match %+v", r)
	}
	if got := readAs(t, s, ctx, map[string]any{"path": "p/a.md", "version": "draft"}); !strings.Contains(got.Content, "the draft") {
		t.Fatalf("the draft was written over: %+v", got)
	}
	r, err = s.handleUpdate(ctx, call(map[string]any{"path": "p/a.md", "content": strings.Replace(draft.Content, "the draft", "a new draft", 1), "if_match": draft.ETag}))
	if err != nil {
		t.Fatal(err)
	}
	resultText(t, r)
	// A write of a part goes on the draft, where the old text is.
	r, err = s.handleEdit(ctx, call(map[string]any{"path": "p/a.md", "old_string": "a new draft", "new_string": "a third draft"}))
	if err != nil {
		t.Fatal(err)
	}
	resultText(t, r)
	if got := readAs(t, s, ctx, map[string]any{"path": "p/a.md", "version": "draft"}); !strings.Contains(got.Content, "a third draft") || !strings.Contains(got.Content, "mcp_version") {
		t.Fatalf("after the writes %+v", got)
	}
	if got := readAs(t, s, ctx, map[string]any{"path": "p/a.md"}); !strings.Contains(got.Content, "V1 body") {
		t.Fatalf("the version moved %+v", got)
	}

	// A transfer takes the file as it is: asked for, not by surprise.
	r, err = s.handleGet(ctx, call(map[string]any{"path": "p/a.md", "transfer": "http"}))
	if err != nil {
		t.Fatal(err)
	}
	if !r.IsError {
		t.Error("a transfer gave the draft to who reads the version")
	}

	// Following the valid version: the draft while there is none.
	choose("valid")
	if got := readAs(t, s, ctx, map[string]any{"path": "p/a.md"}); !strings.Contains(got.Content, "a third draft") || got.Version != "" {
		t.Fatalf("following no version %+v", got)
	}
	r, err = s.handleVersions(ctx, call(map[string]any{"path": "p/a.md", "action": "valid", "tag": "V1"}))
	if err != nil {
		t.Fatal(err)
	}
	resultText(t, r)
	if got := readAs(t, s, ctx, map[string]any{"path": "p/a.md"}); !strings.Contains(got.Content, "V1 body") || got.Version != "V1" {
		t.Fatalf("following the valid version %+v", got)
	}

	choose("draft")
	if got := readAs(t, s, ctx, map[string]any{"path": "p/a.md"}); !strings.Contains(got.Content, "a third draft") || strings.Contains(got.Content, "mcp_version") {
		t.Fatalf("no choice left %+v", got)
	}
}

// A choice that gives nothing to read leaves the note readable, and one that
// is the note as it is leaves its etag good to write with.
func TestVersions_WhatTheNoteGivesMCPCannotBeRead(t *testing.T) {
	s, _, _ := newTestServer(t)
	startGit(t, s)
	ctx := context.Background()
	commitFile(t, s, "p/a.md", "---\nmcp_version: note/p/a.md/none\n---\nbody\n")
	if n := readAs(t, s, ctx, map[string]any{"path": "p/a.md"}); !strings.Contains(n.Content, "body") || n.Version != "" {
		t.Fatalf("%+v", n)
	}
	r, err := s.handleUpdate(ctx, call(map[string]any{"path": "p/a.md", "content": "---\nmcp_version: note/p/a.md/none\n---\nnew\n"}))
	if err != nil {
		t.Fatal(err)
	}
	resultText(t, r)

	commitFile(t, s, "p/b.md", "b body\n")
	r, err = s.handleVersions(ctx, call(map[string]any{"path": "p/b.md", "action": "tag", "label": "V1"}))
	if err != nil {
		t.Fatal(err)
	}
	resultText(t, r)
	r, err = s.handleVersions(ctx, call(map[string]any{"path": "p/b.md", "action": "mcp", "tag": "V1"}))
	if err != nil {
		t.Fatal(err)
	}
	resultText(t, r)
	n := readAs(t, s, ctx, map[string]any{"path": "p/b.md"})
	if n.Version != "" || !strings.Contains(n.Content, "mcp_version") {
		t.Fatalf("the note is the version %+v", n)
	}
	r, err = s.handleUpdate(ctx, call(map[string]any{"path": "p/b.md", "content": "new\n", "if_match": n.ETag}))
	if err != nil {
		t.Fatal(err)
	}
	resultText(t, r)
}
