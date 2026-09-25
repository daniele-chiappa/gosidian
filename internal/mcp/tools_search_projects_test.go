package mcp

import (
	"context"
	"encoding/json"
	"fmt"
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

// BUG-058: a filtered search returns the filtered project's matches even when
// other projects outrank them vault-wide.
func TestMCP_Search_ProjectsFilter_NotStarvedByOtherProjects(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	for k := 0; k < 50; k++ { // stays under the 60 writes/min limiter
		content := fmt.Sprintf("---\ntitle: Docker %d\n---\n\ndocker docker\n", k)
		if res, err := s.handleCreate(ctx, call(map[string]any{"path": fmt.Sprintf("big/n%02d.md", k), "content": content})); err != nil || res.IsError {
			t.Fatalf("seed big: %v %+v", err, res)
		}
	}
	for k := 0; k < 3; k++ {
		content := fmt.Sprintf("# Small %d\n\nsome prose that mentions docker once among other words\n", k)
		if res, err := s.handleCreate(ctx, call(map[string]any{"path": fmt.Sprintf("small/s%d.md", k), "content": content})); err != nil || res.IsError {
			t.Fatalf("seed small: %v %+v", err, res)
		}
	}
	res, _ := s.handleSearch(ctx, call(map[string]any{"query": "docker", "limit": 5, "projects": []any{"small"}}))
	var r struct {
		Hits []searchHit `json:"hits"`
	}
	if err := json.Unmarshal([]byte(resultText(t, res)), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Hits) != 3 {
		t.Fatalf("hits = %+v, want the 3 small notes", r.Hits)
	}
}

func TestMCP_Search_AnyOfAndWhy(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	for path, content := range map[string]string{
		"p/secrets.md": "# Segreti\n\ndove stanno i segreti\n",
		"p/creds.md":   "# Credenziali\n\nle credenziali del server\n",
	} {
		if res, err := s.handleCreate(ctx, call(map[string]any{"path": path, "content": content})); err != nil || res.IsError {
			t.Fatalf("seed %s: %v %+v", path, err, res)
		}
	}
	res, _ := s.handleSearch(ctx, call(map[string]any{"query": "segreti", "any_of": []any{"credenziali"}}))
	var r struct {
		Hits []searchHit `json:"hits"`
	}
	if err := json.Unmarshal([]byte(resultText(t, res)), &r); err != nil {
		t.Fatal(err)
	}
	if len(r.Hits) != 2 {
		t.Fatalf("any_of hits = %+v, want both notes", r.Hits)
	}
	for _, h := range r.Hits {
		if h.Score <= 0 || len(h.Why) == 0 || !strings.HasPrefix(h.Why[len(h.Why)-1], "matched: ") {
			t.Errorf("hit %s lacks score/why: %+v", h.Path, h)
		}
	}

	tooMany := make([]any, 9)
	for k := range tooMany {
		tooMany[k] = fmt.Sprintf("v%d", k)
	}
	if res, _ := s.handleSearch(ctx, call(map[string]any{"query": "x", "any_of": tooMany})); res == nil || !res.IsError {
		t.Errorf("9 variants must be rejected, got %+v", res)
	}
}
