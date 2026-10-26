package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/vault"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// A run token narrowed to its role's folders (IMP-195) writes there through
// every write tool, both ends of a rename and a move included, and is
// refused anywhere else in its project, which it still reads.
func TestMCP_RunTokenWritePaths(t *testing.T) {
	idx, err := index.Open(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	store, err := auth.Open(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Create("admin", nil, []string{auth.ScopeRead, auth.ScopeWrite}, 0, ""); err != nil {
		t.Fatal(err)
	}
	plain, _, err := store.CreateRun("run-r1-a", "p", true, time.Minute, "r1", []string{"p/kb"})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := store.Validate(plain)
	if err != nil {
		t.Fatal(err)
	}
	s := New(vault.New(t.TempDir()), idx, store)
	ctx := context.WithValue(context.Background(), tokenCtxKey, tok)
	writeVaultFile(t, s.vault.Root, "p/notes.md", "# Notes\n")
	writeVaultFile(t, s.vault.Root, "p/kb/a.md", "# A\n")

	refused := func(what string, res *mcplib.CallToolResult) {
		t.Helper()
		if msg := expectError(t, res); !strings.Contains(msg, "this token writes only in p/kb/") {
			t.Errorf("%s: %q", what, msg)
		}
	}
	res, _ := s.handleCreate(ctx, call(map[string]any{"path": "p/kb/new.md", "content": "# New"}))
	resultText(t, res)
	res, _ = s.handleCreate(ctx, call(map[string]any{"path": "p/new.md", "content": "# New"}))
	refused("create outside", res)
	res, _ = s.handleUpdate(ctx, call(map[string]any{"path": "p/notes.md", "content": "# Gone"}))
	refused("update outside", res)
	res, _ = s.handleRenameNote(ctx, call(map[string]any{"from": "p/kb/a.md", "to": "p/kb/b.md"}))
	resultText(t, res)
	res, _ = s.handleRenameNote(ctx, call(map[string]any{"from": "p/kb/b.md", "to": "p/b.md"}))
	refused("rename out of the folder", res)
	res, _ = s.handleRenameNote(ctx, call(map[string]any{"from": "p/notes.md", "to": "p/kb/notes.md"}))
	refused("rename into the folder", res)
	res, _ = s.handleMoveNote(ctx, call(map[string]any{"path": "p/kb/b.md", "project": "p"}))
	refused("move out of the folder", res)
	res, _ = s.handleDelete(ctx, call(map[string]any{"path": "p/notes.md"}))
	refused("delete outside", res)
	res, _ = s.handleDelete(ctx, call(map[string]any{"path": "p/kb/b.md"}))
	resultText(t, res)

	for rel, want := range map[string]bool{"p/kb/new.md": true, "p/new.md": false, "p/notes.md": true, "p/b.md": false, "p/kb/b.md": false} {
		if _, err := os.Stat(filepath.Join(s.vault.Root, rel)); (err == nil) != want {
			t.Errorf("%s exists = %v, want %v", rel, err == nil, want)
		}
	}
	res, _ = s.handleGet(ctx, call(map[string]any{"path": "p/notes.md"}))
	if !strings.Contains(resultText(t, res), "# Notes") {
		t.Error("the run token no longer reads outside its folders")
	}
}
