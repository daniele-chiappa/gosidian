package mcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gosidian/gosidian/internal/auth"
)

// Dot-directories under the vault root (.gosidian/ credential store, .git/
// metadata) must be unreachable through the MCP tools for every token,
// including unscoped ones (IMP-076 / follow-up to GHSA-45w4-74p9-cj5j).

func dotdirCtx(scopes ...string) context.Context {
	return ctxWithToken(&auth.Token{ID: "dotdir01", Name: "unscoped", Scopes: scopes})
}

func dotdirWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMCP_DotDirs_UnscopedTokenCannotReadCredentialStore(t *testing.T) {
	s, _, root := newTestServer(t)
	dotdirWrite(t, filepath.Join(root, ".gosidian", "auth.json"), `{"totp_secret":"MUSTNOTSEE"}`)
	dotdirWrite(t, filepath.Join(root, ".git", "config"), "url = https://x:MUSTNOTSEE@example.com\n")
	ctx := dotdirCtx(auth.ScopeRead)
	for _, p := range []string{".gosidian/auth.json", ".git/config"} {
		res, _ := s.handleGet(ctx, call(map[string]any{"path": p}))
		if res == nil || !res.IsError {
			t.Errorf("memory_get %q succeeded (want error)", p)
		}
	}
}

func TestMCP_DotDirs_UnscopedTokenCannotWriteCredentialStore(t *testing.T) {
	s, _, root := newTestServer(t)
	authPath := filepath.Join(root, ".gosidian", "auth.json")
	const original = `{"users":[]}`
	dotdirWrite(t, authPath, original)
	ctx := dotdirCtx(auth.ScopeRead, auth.ScopeWrite)

	if res, _ := s.handleUpdate(ctx, call(map[string]any{"path": ".gosidian/auth.json", "content": "pwned"})); res == nil || !res.IsError {
		t.Errorf("memory_update .gosidian/auth.json succeeded (want error)")
	}
	if b, _ := os.ReadFile(authPath); string(b) != original {
		t.Errorf("credential store rewritten through memory_update: %q", b)
	}
	if res, _ := s.handleCreate(ctx, call(map[string]any{"path": ".git/config", "content": "[core]\n\thooksPath = /tmp/evil\n"})); res == nil || !res.IsError {
		t.Errorf("memory_create .git/config succeeded (want error)")
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "config")); err == nil {
		t.Errorf(".git/config was created through memory_create")
	}
	if res, _ := s.handleDelete(ctx, call(map[string]any{"path": ".gosidian/auth.json"})); res == nil || !res.IsError {
		t.Errorf("memory_delete .gosidian/auth.json succeeded (want error)")
	}
	if _, err := os.Stat(authPath); err != nil {
		t.Errorf("credential store removed through memory_delete: %v", err)
	}
}
