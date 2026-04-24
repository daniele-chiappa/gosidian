package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

func seedImportanceVault(t *testing.T, s *Server) string {
	t.Helper()
	ctx := context.Background()
	notes := []struct{ path, content string }{
		{"proj/critical.md", "---\ntitle: critical\nimportance: 5\n---\n\n# critical"},
		{"proj/high.md", "---\ntitle: high\nimportance: 4\n---\n\n# high"},
		{"proj/default.md", "---\ntitle: default\n---\n\n# default"}, // implicit 3
		{"proj/low.md", "---\ntitle: low\nimportance: 2\n---\n\n# low"},
		{"proj/archival.md", "---\ntitle: archival\nimportance: 1\n---\n\n# archival"},
		{"proj/garbage.md", "---\ntitle: garbage\nimportance: abc\n---\n\n# garbage"}, // unparseable → default 3
		{"proj/too-high.md", "---\ntitle: too-high\nimportance: 99\n---\n\n# clamped"},  // clamped to 5
		{"other/foreign.md", "---\ntitle: foreign\nimportance: 5\n---\n\nnot mine"},
	}
	for _, n := range notes {
		res, err := s.handleCreate(ctx, call(map[string]any{"path": n.path, "content": n.content}))
		if err != nil || (res != nil && res.IsError) {
			t.Fatalf("seed %q: err=%v res=%+v", n.path, err, res)
		}
	}
	return "proj"
}

func TestMCP_Importance_DefaultFilter(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedImportanceVault(t, s)

	// Default min_level=3: exclude 2 and 1; include default (3), garbage (3→default), high (4), critical (5), too-high (clamped 5).
	res, _ := s.handleNotesByImportance(context.Background(), call(map[string]any{"project": "proj"}))
	body := resultText(t, res)
	var p struct {
		Notes []importanceEntry `json:"notes"`
	}
	_ = json.Unmarshal([]byte(body), &p)

	if len(p.Notes) != 5 {
		t.Fatalf("expected 5 notes at min_level=3, got %d: %+v", len(p.Notes), p.Notes)
	}
	// Sorted DESC by importance: critical(5), too-high(5), high(4), default(3), garbage(3). Tiebreak by path ASC.
	wantOrder := []struct {
		Path string
		Imp  int
	}{
		{"proj/critical.md", 5},
		{"proj/too-high.md", 5},
		{"proj/high.md", 4},
		{"proj/default.md", 3},
		{"proj/garbage.md", 3},
	}
	for i, w := range wantOrder {
		if p.Notes[i].Path != w.Path || p.Notes[i].Importance != w.Imp {
			t.Errorf("idx %d: got %+v, want %+v", i, p.Notes[i], w)
		}
	}
}

func TestMCP_Importance_HigherFilter(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedImportanceVault(t, s)

	res, _ := s.handleNotesByImportance(context.Background(), call(map[string]any{"project": "proj", "min_level": float64(4)}))
	body := resultText(t, res)
	var p struct {
		Notes []importanceEntry `json:"notes"`
	}
	_ = json.Unmarshal([]byte(body), &p)

	if len(p.Notes) != 3 {
		t.Fatalf("expected 3 notes at min_level=4, got %d: %+v", len(p.Notes), p.Notes)
	}
}

func TestMCP_Importance_ProjectIsolation(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedImportanceVault(t, s)

	res, _ := s.handleNotesByImportance(context.Background(), call(map[string]any{"project": "proj", "min_level": float64(5)}))
	body := resultText(t, res)
	var p struct {
		Notes []importanceEntry `json:"notes"`
	}
	_ = json.Unmarshal([]byte(body), &p)

	for _, n := range p.Notes {
		if n.Path == "other/foreign.md" {
			t.Errorf("cross-project leak: %+v", n)
		}
	}
}

func TestMCP_Importance_ClampsMinLevel(t *testing.T) {
	s, _, _ := newTestServer(t)
	seedImportanceVault(t, s)

	// min_level=99 should clamp to 5.
	res, _ := s.handleNotesByImportance(context.Background(), call(map[string]any{"project": "proj", "min_level": float64(99)}))
	body := resultText(t, res)
	var p struct {
		Notes []importanceEntry `json:"notes"`
	}
	_ = json.Unmarshal([]byte(body), &p)

	for _, n := range p.Notes {
		if n.Importance != 5 {
			t.Errorf("clamped min_level should only return 5s, got %+v", n)
		}
	}
}

func TestMCP_Search_IncludeOutline(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	content := `---
title: demo
---

# Top

## Section A

alpha

## Section B

beta`
	s.handleCreate(ctx, call(map[string]any{"path": "demo.md", "content": content}))

	res, _ := s.handleSearch(ctx, call(map[string]any{"query": "alpha", "include_outline": true}))
	body := resultText(t, res)
	var p struct {
		Hits []searchHit `json:"hits"`
	}
	_ = json.Unmarshal([]byte(body), &p)

	if len(p.Hits) != 1 {
		t.Fatalf("expected 1 hit, got %d: %+v", len(p.Hits), p.Hits)
	}
	if len(p.Hits[0].Outline) < 3 {
		t.Errorf("expected outline with 3+ headings, got %+v", p.Hits[0].Outline)
	}
	// Frontmatter not requested — must stay nil.
	if p.Hits[0].Frontmatter != nil {
		t.Errorf("frontmatter should be absent: %+v", p.Hits[0].Frontmatter)
	}
}

func TestMCP_Search_IncludeFrontmatter(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	s.handleCreate(ctx, call(map[string]any{
		"path":    "x.md",
		"content": "---\ntitle: X\nstatus: in-progress\ntags: [alpha, beta]\n---\n\n# X\n\nhello world",
	}))

	res, _ := s.handleSearch(ctx, call(map[string]any{"query": "hello", "include_frontmatter": true}))
	body := resultText(t, res)
	var p struct {
		Hits []searchHit `json:"hits"`
	}
	_ = json.Unmarshal([]byte(body), &p)

	if len(p.Hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(p.Hits))
	}
	if p.Hits[0].Frontmatter["status"] != "in-progress" {
		t.Errorf("status not captured: %+v", p.Hits[0].Frontmatter)
	}
	// Outline not requested — must stay nil.
	if p.Hits[0].Outline != nil {
		t.Errorf("outline should be absent: %+v", p.Hits[0].Outline)
	}
}

func TestMCP_Search_BackcompatDefault(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	s.handleCreate(ctx, call(map[string]any{
		"path":    "x.md",
		"content": "---\ntitle: X\n---\n\n# X\n\nhello world",
	}))

	// No flags — outline and frontmatter must NOT be populated.
	res, _ := s.handleSearch(ctx, call(map[string]any{"query": "hello"}))
	body := resultText(t, res)
	var p struct {
		Hits []searchHit `json:"hits"`
	}
	_ = json.Unmarshal([]byte(body), &p)

	if len(p.Hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(p.Hits))
	}
	if p.Hits[0].Outline != nil || p.Hits[0].Frontmatter != nil {
		t.Errorf("backcompat: outline/frontmatter should stay empty, got %+v", p.Hits[0])
	}
}
