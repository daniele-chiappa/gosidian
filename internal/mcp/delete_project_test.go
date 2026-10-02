package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/projectops"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/trash"
	"github.com/gosidian/gosidian/internal/vault"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// mustCall drops a handler's Go error, always nil by convention (failures
// are tool results); resultText fails the test on a nil or error result.
func mustCall(r *mcplib.CallToolResult, _ error) *mcplib.CallToolResult { return r }

// memory_delete_project takes the web UI's path: the project goes to the
// trash with its access, its access entry goes, and the MCP tokens scoped
// to it are revoked or narrowed; a project created afterwards with the name
// starts from a fresh entry (IMP-124). It used to remove the folder from
// disk and leave the access entry and the token scopes behind.
func TestMCP_DeleteProject_TrashAccessAndTokens(t *testing.T) {
	dir := t.TempDir()
	idx, err := index.Open(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	tokens, err := auth.Open(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	v := vault.New(dir)
	s := New(v, idx, tokens)
	ps, err := projects.Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.SetProjects(ps)
	bin := trash.New(dir, -1)
	s.SetTrash(bin)
	ctx := context.WithValue(context.Background(), tokenCtxKey, auth.AdminToken())

	resultText(t, mustCall(s.handleCreateProject(ctx, call(map[string]any{"name": "Work"}))))
	resultText(t, mustCall(s.handleCreate(ctx, call(map[string]any{"path": "Work/n.md", "content": "x"}))))
	for name, scope := range map[string][]string{"only-work": {"Work"}, "work-and-home": {"Work", "Home"}} {
		if _, _, err := tokens.Create(name, scope, []string{auth.ScopeRead}, 0, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := ps.SetAccess("Work", projects.Access{
		Flags:   projects.Flags{Visibility: projects.VisibilityPrivate},
		Members: []projects.ProjectMember{{UserID: "u1", Level: projects.LevelWrite}},
	}); err != nil {
		t.Fatal(err)
	}

	var out struct {
		TrashID        string `json:"trash_id"`
		TokensRevoked  int    `json:"tokens_revoked"`
		TokensNarrowed int    `json:"tokens_narrowed"`
	}
	text := resultText(t, mustCall(s.handleDeleteProject(ctx, call(map[string]any{"name": "Work"}))))
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("%v: %s", err, text)
	}
	if out.TrashID == "" || out.TokensRevoked != 1 || out.TokensNarrowed != 1 {
		t.Errorf("delete result = %s", text)
	}
	saved, ok, err := projectops.TrashedAccess(bin, out.TrashID)
	if err != nil || !ok || saved.Flags.Visibility != projects.VisibilityPrivate || len(saved.Members) != 1 {
		t.Errorf("access stored with the trashed project = %+v, %v, %v", saved, ok, err)
	}
	if ps.Get("Work") != (projects.Flags{}) {
		t.Errorf("access entry left behind: %+v", ps.Get("Work"))
	}

	resultText(t, mustCall(s.handleCreateProject(ctx, call(map[string]any{"name": "Work"}))))
	if _, ok := ps.MemberLevel("Work", "u1"); ok {
		t.Error("the deleted project's grant reached the new one")
	}
}
