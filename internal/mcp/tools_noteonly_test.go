package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/auth"
)

// ADR-021 on the MCP surface: write tools only touch note files; attachment
// bytes go through memory_ingest / memory_upload_attachment and are removed
// with memory_delete_attachment.

func seedFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestMCP_NoteOnly_CreateRejectsNonNoteWithHint(t *testing.T) {
	s, _, root := newTestServer(t)
	ctx := ctxWithToken(&auth.Token{ID: "noteonly", Name: "unscoped", Scopes: []string{auth.ScopeRead, auth.ScopeWrite}})
	res, _ := s.handleCreate(ctx, call(map[string]any{"path": "proj/evil.sh", "content": "#!/bin/sh\n"}))
	msg := expectError(t, res)
	if !strings.Contains(msg, "memory_ingest") {
		t.Errorf("error should point to memory_ingest, got %q", msg)
	}
	if _, err := os.Stat(filepath.Join(root, "proj", "evil.sh")); err == nil {
		t.Errorf("non-note file was created")
	}
}

func TestMCP_NoteOnly_UpdateAndDeleteLeaveAttachmentsAlone(t *testing.T) {
	s, _, root := newTestServer(t)
	ctx := ctxWithToken(&auth.Token{ID: "noteonly", Name: "unscoped", Scopes: []string{auth.ScopeRead, auth.ScopeWrite}})
	abs := seedFile(t, root, "proj/attachments/data.csv", "a,b\n1,2\n")

	if res, _ := s.handleUpdate(ctx, call(map[string]any{"path": "proj/attachments/data.csv", "content": "pwned"})); res == nil || !res.IsError {
		t.Errorf("memory_update on an attachment succeeded")
	}
	if b, _ := os.ReadFile(abs); string(b) != "a,b\n1,2\n" {
		t.Errorf("attachment rewritten: %q", b)
	}
	if res, _ := s.handleDelete(ctx, call(map[string]any{"path": "proj/attachments/data.csv"})); res == nil || !res.IsError {
		t.Errorf("memory_delete on an attachment succeeded")
	}
	if _, err := os.Stat(abs); err != nil {
		t.Errorf("attachment removed through memory_delete: %v", err)
	}
	// The dedicated tool still works (positive control).
	if res, _ := s.handleDeleteAttachment(ctx, call(map[string]any{"path": "proj/attachments/data.csv"})); res == nil || res.IsError {
		t.Errorf("memory_delete_attachment failed: %v", res)
	}
	if _, err := os.Stat(abs); err == nil {
		t.Errorf("memory_delete_attachment did not remove the file")
	}
	// ...but refuses notes.
	seedFile(t, root, "proj/note.md", "# n\n")
	if res, _ := s.handleDeleteAttachment(ctx, call(map[string]any{"path": "proj/note.md"})); res == nil || !res.IsError {
		t.Errorf("memory_delete_attachment removed a note")
	}
}

func TestMCP_NoteOnly_RenameKeepsNoteExtension(t *testing.T) {
	s, _, root := newTestServer(t)
	ctx := ctxWithToken(&auth.Token{ID: "noteonly", Name: "unscoped", Scopes: []string{auth.ScopeRead, auth.ScopeWrite}})
	if res, _ := s.handleCreate(ctx, call(map[string]any{"path": "proj/a.md", "content": "# a\n"})); res == nil || res.IsError {
		t.Fatalf("create: %v", res)
	}
	if res, _ := s.handleRenameNote(ctx, call(map[string]any{"from": "proj/a.md", "to": "proj/a.svg"})); res == nil || !res.IsError {
		t.Errorf("rename to .svg succeeded")
	}
	if _, err := os.Stat(filepath.Join(root, "proj", "a.svg")); err == nil {
		t.Errorf("note renamed to a non-note extension")
	}
	if _, err := os.Stat(filepath.Join(root, "proj", "a.md")); err != nil {
		t.Errorf("source note lost: %v", err)
	}
}
