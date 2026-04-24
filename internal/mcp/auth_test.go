package mcp

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/vault"
)

// newScopedServer builds a server wired to a real token store and returns
// a context pre-loaded with the given token so handlers can be invoked
// directly (bypassing the SSE transport).
func newScopedServer(t *testing.T, project string, scopes []string) (*Server, context.Context) {
	t.Helper()
	dir := t.TempDir()
	idx, err := index.Open(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	v := vault.New(dir)

	storePath := filepath.Join(t.TempDir(), "tokens.json")
	store, err := auth.Open(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Create("test", project, scopes, 0, ""); err != nil {
		t.Fatal(err)
	}

	s := New(v, idx, store)
	tok := store.List()[0]
	ctx := context.WithValue(context.Background(), tokenCtxKey, &tok)
	return s, ctx
}

func TestMCP_ReadOnlyTokenRejectsWrites(t *testing.T) {
	s, ctx := newScopedServer(t, "", []string{auth.ScopeRead})

	res, _ := s.handleCreate(ctx, call(map[string]any{
		"path": "hello.md", "content": "nope",
	}))
	msg := expectError(t, res)
	if !strings.Contains(msg, "write scope") {
		t.Errorf("error = %q", msg)
	}
}

func TestMCP_ScopedTokenCannotLeaveProject(t *testing.T) {
	s, ctx := newScopedServer(t, "Work", []string{auth.ScopeRead, auth.ScopeWrite})
	// Pre-create the Work project via admin path (direct filesystem).
	if _, err := s.vault.CreateProject("Work"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.vault.CreateProject("Personal"); err != nil {
		t.Fatal(err)
	}

	// Create inside scope → ok
	res, _ := s.handleCreate(ctx, call(map[string]any{
		"path": "Work/note.md", "content": "# inside",
	}))
	resultText(t, res)

	// Create outside scope → rejected
	res, _ = s.handleCreate(ctx, call(map[string]any{
		"path": "Personal/note.md", "content": "# outside",
	}))
	msg := expectError(t, res)
	if !strings.Contains(msg, "project scope") {
		t.Errorf("expected scope error, got: %q", msg)
	}

	// Get outside scope → rejected
	res, _ = s.handleGet(ctx, call(map[string]any{"path": "Personal/anything.md"}))
	msg = expectError(t, res)
	if !strings.Contains(msg, "scope") {
		t.Errorf("expected scope error, got: %q", msg)
	}
}

func TestMCP_ScopedTokenListingFiltered(t *testing.T) {
	s, ctx := newScopedServer(t, "Work", []string{auth.ScopeRead, auth.ScopeWrite})

	// Create notes in both projects using an admin context.
	admin := context.WithValue(context.Background(), tokenCtxKey, auth.AdminToken())
	_, _ = s.vault.CreateProject("Work")
	_, _ = s.vault.CreateProject("Personal")
	_, _ = s.handleCreate(admin, call(map[string]any{"path": "Work/a.md", "content": "a"}))
	_, _ = s.handleCreate(admin, call(map[string]any{"path": "Personal/b.md", "content": "b"}))

	// Scoped list: only Work visible
	res, _ := s.handleListNotes(ctx, call(map[string]any{}))
	body := resultText(t, res)
	if !strings.Contains(body, "Work/a.md") {
		t.Errorf("missing Work note: %s", body)
	}
	if strings.Contains(body, "Personal/b.md") {
		t.Errorf("scoped token saw out-of-scope note: %s", body)
	}

	// Search: Personal must be filtered out even if it matches the query.
	res, _ = s.handleSearch(ctx, call(map[string]any{"query": "b"}))
	body = resultText(t, res)
	if strings.Contains(body, "Personal/b.md") {
		t.Errorf("search leaked: %s", body)
	}

	// List projects: only Work appears.
	res, _ = s.handleListProjects(ctx, call(map[string]any{}))
	body = resultText(t, res)
	if !strings.Contains(body, "Work") || strings.Contains(body, "Personal") {
		t.Errorf("project list not filtered: %s", body)
	}
}

func TestMCP_ScopedTokenCannotCreateProject(t *testing.T) {
	s, ctx := newScopedServer(t, "Work", []string{auth.ScopeRead, auth.ScopeWrite})
	res, _ := s.handleCreateProject(ctx, call(map[string]any{"name": "Other"}))
	msg := expectError(t, res)
	if !strings.Contains(msg, "scoped tokens cannot") {
		t.Errorf("error = %q", msg)
	}
}

func TestMCP_ScopedTokenCannotDeleteProject(t *testing.T) {
	s, ctx := newScopedServer(t, "Work", []string{auth.ScopeRead, auth.ScopeWrite})
	res, _ := s.handleDeleteProject(ctx, call(map[string]any{"name": "Work"}))
	msg := expectError(t, res)
	if !strings.Contains(msg, "scoped tokens cannot") {
		t.Errorf("error = %q", msg)
	}
}

func TestMCP_ScopedTokenCannotRenameProject(t *testing.T) {
	s, ctx := newScopedServer(t, "Work", []string{auth.ScopeRead, auth.ScopeWrite})
	res, _ := s.handleRenameProject(ctx, call(map[string]any{"from": "Work", "to": "Other"}))
	msg := expectError(t, res)
	if !strings.Contains(msg, "scoped tokens cannot") {
		t.Errorf("error = %q", msg)
	}
}

func TestMCP_ScopedTokenRenameNoteWithinScope(t *testing.T) {
	s, ctx := newScopedServer(t, "Work", []string{auth.ScopeRead, auth.ScopeWrite})
	_, _ = s.vault.CreateProject("Work")
	_, _ = s.handleCreate(ctx, call(map[string]any{"path": "Work/a.md", "content": "x"}))

	// Renaming within the project scope: ok.
	res, _ := s.handleRenameNote(ctx, call(map[string]any{"from": "Work/a.md", "to": "Work/b.md"}))
	resultText(t, res)

	// Renaming OUT of the project scope: rejected.
	res, _ = s.handleRenameNote(ctx, call(map[string]any{"from": "Work/b.md", "to": "Other/b.md"}))
	msg := expectError(t, res)
	if !strings.Contains(msg, "scope") {
		t.Errorf("expected scope error, got: %q", msg)
	}
}
