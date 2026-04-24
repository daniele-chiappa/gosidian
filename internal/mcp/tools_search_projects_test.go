package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/auth"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// seedCrossProjectVault writes notes into 3 distinct projects so the
// projects[] filter has something to discriminate against.
func seedCrossProjectVault(t *testing.T, s *Server) {
	t.Helper()
	ctx := context.Background()
	notes := []struct{ path, content string }{
		{"alpha/plans/release.md", "---\ntitle: alpha release\ntags: [alpha, type:plan]\n---\n\n# alpha release\n\nRelease alpha checklist.\n"},
		{"beta/plans/release.md", "---\ntitle: beta release\ntags: [beta, type:plan]\n---\n\n# beta release\n\nRelease beta checklist.\n"},
		{"gamma/notes/release.md", "---\ntitle: gamma release\ntags: [gamma]\n---\n\n# gamma release\n\nRelease gamma notes.\n"},
	}
	for _, n := range notes {
		res, err := s.handleCreate(ctx, call(map[string]any{"path": n.path, "content": n.content}))
		if err != nil || (res != nil && res.IsError) {
			t.Fatalf("seed %q: err=%v res=%+v", n.path, err, res)
		}
	}
}

func TestMCP_Search_NoProjectsFilter_ReturnsAll(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedCrossProjectVault(t, s)

	res, _ := s.handleSearch(context.Background(), call(map[string]any{"query": "release"}))
	body := resultText(t, res)
	var r struct {
		Hits []searchHit `json:"hits"`
	}
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Hits) != 3 {
		t.Fatalf("expected 3 hits across all projects, got %d: %+v", len(r.Hits), r.Hits)
	}
}

func TestMCP_Search_ProjectsFilter_RestrictsHits(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedCrossProjectVault(t, s)

	res, _ := s.handleSearch(context.Background(), call(map[string]any{
		"query":    "release",
		"projects": []any{"alpha", "beta"},
	}))
	body := resultText(t, res)
	var r struct {
		Hits []searchHit `json:"hits"`
	}
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Hits) != 2 {
		t.Fatalf("expected 2 hits (alpha+beta), got %d: %+v", len(r.Hits), r.Hits)
	}
	for _, h := range r.Hits {
		if !strings.HasPrefix(h.Path, "alpha/") && !strings.HasPrefix(h.Path, "beta/") {
			t.Errorf("unexpected project in hit %q", h.Path)
		}
	}
}

func TestMCP_Search_ProjectsFilter_Dedupe(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedCrossProjectVault(t, s)

	// Same project listed twice with spurious whitespace: must still return 1 match.
	res, _ := s.handleSearch(context.Background(), call(map[string]any{
		"query":    "release",
		"projects": []any{" alpha ", "alpha"},
	}))
	body := resultText(t, res)
	var r struct {
		Hits []searchHit `json:"hits"`
	}
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Hits) != 1 || !strings.HasPrefix(r.Hits[0].Path, "alpha/") {
		t.Fatalf("expected 1 alpha hit after dedupe, got %+v", r.Hits)
	}
}

// TestMCP_Search_ScopedToken_SilentlyIntersects constructs a scoped token
// context and verifies that asking for a different project yields zero
// results — no error, silent intersection.
func TestMCP_Search_ScopedToken_SilentlyIntersects(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedCrossProjectVault(t, s)

	// Build a context with a token scoped to "alpha".
	tok := &auth.Token{
		ID:      "scoped-alpha",
		Name:    "scoped-alpha",
		Project: "alpha",
		Scopes:  []string{auth.ScopeRead, auth.ScopeWrite},
	}
	ctx := context.WithValue(context.Background(), tokenCtxKey, tok)

	// Asking for beta should silently yield no results (scope intersection
	// makes the effective filter = ["alpha"], which doesn't match beta/*
	// paths).
	res, _ := s.handleSearch(ctx, call(map[string]any{
		"query":    "release",
		"projects": []any{"beta"},
	}))
	if res == nil {
		t.Fatal("nil result")
	}
	if res.IsError {
		var sb strings.Builder
		for _, c := range res.Content {
			if tc, ok := c.(mcplib.TextContent); ok {
				sb.WriteString(tc.Text)
			}
		}
		t.Fatalf("expected silent zero-hit, got error: %s", sb.String())
	}
	body := resultText(t, res)
	var r struct {
		Hits []searchHit `json:"hits"`
	}
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	// The token's AllowsPath already rejects non-alpha paths, so we get 0
	// hits regardless — the important thing is no error.
	if len(r.Hits) != 0 {
		t.Errorf("expected 0 hits (scope intersection), got %+v", r.Hits)
	}

	// And asking for alpha (inside scope) still works.
	res2, _ := s.handleSearch(ctx, call(map[string]any{
		"query":    "release",
		"projects": []any{"alpha"},
	}))
	body2 := resultText(t, res2)
	var r2 struct {
		Hits []searchHit `json:"hits"`
	}
	if err := json.Unmarshal([]byte(body2), &r2); err != nil {
		t.Fatal(err)
	}
	if len(r2.Hits) != 1 || !strings.HasPrefix(r2.Hits[0].Path, "alpha/") {
		t.Errorf("expected single alpha hit, got %+v", r2.Hits)
	}
}
