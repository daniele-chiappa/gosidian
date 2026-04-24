package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/vault"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func newTestServer(t *testing.T) (*Server, *vault.Vault, string) {
	t.Helper()
	dir := t.TempDir()
	idx, err := index.Open(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	v := vault.New(dir)
	s := New(v, idx, nil) // nil token store → auth disabled, implicit admin
	return s, v, dir
}

func call(args map[string]any) mcplib.CallToolRequest {
	return mcplib.CallToolRequest{
		Params: mcplib.CallToolParams{Arguments: args},
	}
}

func resultText(t *testing.T, r *mcplib.CallToolResult) string {
	t.Helper()
	if r == nil {
		t.Fatal("nil result")
	}
	if r.IsError {
		var sb strings.Builder
		for _, c := range r.Content {
			if tc, ok := c.(mcplib.TextContent); ok {
				sb.WriteString(tc.Text)
			}
		}
		t.Fatalf("tool returned error: %s", sb.String())
	}
	var sb strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(mcplib.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func expectError(t *testing.T, r *mcplib.CallToolResult) string {
	t.Helper()
	if r == nil {
		t.Fatal("nil result")
	}
	if !r.IsError {
		t.Fatalf("expected error, got success: %+v", r)
	}
	var sb strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(mcplib.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func TestMCP_BatchGet(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()

	// Seed 2 notes.
	_, _ = s.handleCreate(ctx, call(map[string]any{"path": "a.md", "content": "# A\nalpha"}))
	_, _ = s.handleCreate(ctx, call(map[string]any{"path": "b.md", "content": "# B\nbeta"}))

	res, err := s.handleBatchGet(ctx, call(map[string]any{
		"paths": []any{"a.md", "b.md", "missing.md"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	body := resultText(t, res)

	var payload struct {
		Results []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
			Error   string `json:"error"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("parse: %v body=%s", err, body)
	}
	if len(payload.Results) != 3 {
		t.Fatalf("results len = %d, want 3", len(payload.Results))
	}
	if payload.Results[0].Path != "a.md" || !strings.Contains(payload.Results[0].Content, "alpha") {
		t.Errorf("a.md entry wrong: %+v", payload.Results[0])
	}
	if payload.Results[2].Path != "missing.md" || payload.Results[2].Error == "" {
		t.Errorf("missing.md should have error: %+v", payload.Results[2])
	}

	// Empty paths → error.
	res, _ = s.handleBatchGet(ctx, call(map[string]any{"paths": []any{}}))
	expectError(t, res)
}

func TestMCP_Recent(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()

	// Seed. Ordering of writes determines ordering of mtime (later = newer).
	_, _ = s.handleCreate(ctx, call(map[string]any{"path": "old.md", "content": "old"}))
	_, _ = s.handleCreate(ctx, call(map[string]any{"path": "proj/new.md", "content": "new"}))

	// No project filter.
	res, err := s.handleRecent(ctx, call(map[string]any{"limit": float64(5)}))
	if err != nil {
		t.Fatal(err)
	}
	body := resultText(t, res)
	var payload struct {
		Notes []struct {
			Path  string `json:"path"`
			Mtime int64  `json:"mtime"`
		} `json:"notes"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("parse: %v body=%s", err, body)
	}
	if len(payload.Notes) < 2 {
		t.Fatalf("recent len = %d, want >= 2", len(payload.Notes))
	}

	// Project filter.
	res, _ = s.handleRecent(ctx, call(map[string]any{"project": "proj"}))
	body = resultText(t, res)
	_ = json.Unmarshal([]byte(body), &payload)
	for _, n := range payload.Notes {
		if !strings.HasPrefix(n.Path, "proj/") {
			t.Errorf("path %q should start with proj/", n.Path)
		}
	}

	// Invalid since → error.
	res, _ = s.handleRecent(ctx, call(map[string]any{"since": "not-a-duration"}))
	expectError(t, res)

	// Relative duration form (7d).
	res, _ = s.handleRecent(ctx, call(map[string]any{"since": "7d"}))
	resultText(t, res) // must not error
}

func TestMCP_GetFrontmatter(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()

	content := "---\ntitle: Hello\ntype: plan\nstatus: draft\ntags: [a, b]\n---\n\nbody\n"
	_, _ = s.handleCreate(ctx, call(map[string]any{"path": "p.md", "content": content}))

	res, err := s.handleGetFrontmatter(ctx, call(map[string]any{"path": "p.md"}))
	if err != nil {
		t.Fatal(err)
	}
	body := resultText(t, res)

	var payload struct {
		Path   string                 `json:"path"`
		Raw    string                 `json:"raw"`
		Parsed map[string]interface{} `json:"parsed"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("parse: %v body=%s", err, body)
	}
	if payload.Parsed["title"] != "Hello" {
		t.Errorf("title = %v", payload.Parsed["title"])
	}
	if payload.Parsed["status"] != "draft" {
		t.Errorf("status = %v", payload.Parsed["status"])
	}
	// Raw must not include --- markers nor body.
	if strings.Contains(payload.Raw, "---") || strings.Contains(payload.Raw, "body") {
		t.Errorf("raw leaked markers or body: %q", payload.Raw)
	}

	// Note without frontmatter → empty raw + empty parsed.
	_, _ = s.handleCreate(ctx, call(map[string]any{"path": "plain.md", "content": "# Plain\nbody"}))
	res, _ = s.handleGetFrontmatter(ctx, call(map[string]any{"path": "plain.md"}))
	body = resultText(t, res)
	_ = json.Unmarshal([]byte(body), &payload)
	if payload.Raw != "" {
		t.Errorf("plain note raw = %q, want empty", payload.Raw)
	}
}

func TestMCP_GetOutline(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()

	content := "# H1\n\n## H2a\n\n### H3\n\n## H2b\n"
	_, _ = s.handleCreate(ctx, call(map[string]any{"path": "o.md", "content": content}))

	res, err := s.handleGetOutline(ctx, call(map[string]any{"path": "o.md"}))
	if err != nil {
		t.Fatal(err)
	}
	body := resultText(t, res)

	var payload struct {
		Path     string `json:"path"`
		Headings []struct {
			Level int    `json:"level"`
			Text  string `json:"text"`
			ID    string `json:"id"`
		} `json:"headings"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("parse: %v body=%s", err, body)
	}
	if len(payload.Headings) != 4 {
		t.Fatalf("headings len = %d, want 4", len(payload.Headings))
	}
	if payload.Headings[0].Level != 1 || payload.Headings[0].Text != "H1" {
		t.Errorf("h[0] = %+v", payload.Headings[0])
	}
	if payload.Headings[2].Level != 3 || payload.Headings[2].Text != "H3" {
		t.Errorf("h[2] = %+v", payload.Headings[2])
	}
}

func TestMCP_ETagGetAndUpdate(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()

	// Create + read → get etag
	_, _ = s.handleCreate(ctx, call(map[string]any{"path": "e.md", "content": "v1"}))
	res, _ := s.handleGet(ctx, call(map[string]any{"path": "e.md"}))
	body := resultText(t, res)
	var get struct {
		Content string `json:"content"`
		ETag    string `json:"etag"`
	}
	if err := json.Unmarshal([]byte(body), &get); err != nil {
		t.Fatal(err)
	}
	if get.ETag == "" {
		t.Fatal("etag missing from memory_get response")
	}
	if get.Content != "v1" {
		t.Errorf("content = %q, want v1", get.Content)
	}

	// Update with matching if_match → success, returns new etag
	res, _ = s.handleUpdate(ctx, call(map[string]any{
		"path":     "e.md",
		"content":  "v2",
		"if_match": get.ETag,
	}))
	body = resultText(t, res)
	var upd struct {
		ETag string `json:"etag"`
	}
	_ = json.Unmarshal([]byte(body), &upd)
	if upd.ETag == "" || upd.ETag == get.ETag {
		t.Errorf("new etag should differ from old: old=%q new=%q", get.ETag, upd.ETag)
	}

	// Update with stale if_match → error
	res, _ = s.handleUpdate(ctx, call(map[string]any{
		"path":     "e.md",
		"content":  "v3",
		"if_match": get.ETag, // stale now
	}))
	errMsg := expectError(t, res)
	if !strings.Contains(errMsg, "etag mismatch") {
		t.Errorf("expected etag mismatch error, got: %s", errMsg)
	}

	// Update with no if_match → always succeeds (backward compat)
	res, _ = s.handleUpdate(ctx, call(map[string]any{
		"path":    "e.md",
		"content": "v4",
	}))
	resultText(t, res) // must not error
}

func TestMCP_ETagEdit(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()

	_, _ = s.handleCreate(ctx, call(map[string]any{"path": "e.md", "content": "alpha"}))
	res, _ := s.handleGet(ctx, call(map[string]any{"path": "e.md"}))
	var get struct {
		ETag string `json:"etag"`
	}
	_ = json.Unmarshal([]byte(resultText(t, res)), &get)

	// Matching edit
	res, _ = s.handleEdit(ctx, call(map[string]any{
		"path":       "e.md",
		"old_string": "alpha",
		"new_string": "beta",
		"if_match":   get.ETag,
	}))
	resultText(t, res)

	// Stale edit
	res, _ = s.handleEdit(ctx, call(map[string]any{
		"path":       "e.md",
		"old_string": "beta",
		"new_string": "gamma",
		"if_match":   get.ETag, // stale
	}))
	errMsg := expectError(t, res)
	if !strings.Contains(errMsg, "etag mismatch") {
		t.Errorf("expected etag mismatch, got: %s", errMsg)
	}
}

func TestMCP_CreateAndGet(t *testing.T) {
	s, v, dir := newTestServer(t)
	ctx := context.Background()

	// create
	res, err := s.handleCreate(ctx, call(map[string]any{
		"path":    "hello.md",
		"content": "# Hello\n\nBody here.",
	}))
	if err != nil {
		t.Fatal(err)
	}
	resultText(t, res)

	// file exists on disk
	if _, err := os.Stat(filepath.Join(dir, "hello.md")); err != nil {
		t.Errorf("file not created: %v", err)
	}
	// and via vault Load
	if _, err := v.Load("hello.md"); err != nil {
		t.Errorf("vault can't load: %v", err)
	}

	// get returns the content
	res, _ = s.handleGet(ctx, call(map[string]any{"path": "hello.md"}))
	body := resultText(t, res)
	var got noteContent
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("unmarshal: %v (body=%s)", err, body)
	}
	if got.Content != "# Hello\n\nBody here." {
		t.Errorf("content mismatch: %q", got.Content)
	}
}

func TestMCP_CreateDuplicate(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	_, _ = s.handleCreate(ctx, call(map[string]any{"path": "a.md", "content": "one"}))
	res, _ := s.handleCreate(ctx, call(map[string]any{"path": "a.md", "content": "two"}))
	msg := expectError(t, res)
	if !strings.Contains(msg, "already exists") {
		t.Errorf("error message = %q", msg)
	}
}

func TestMCP_InvalidPath(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	res, _ := s.handleCreate(ctx, call(map[string]any{"path": "../evil.md", "content": "x"}))
	msg := expectError(t, res)
	if !strings.Contains(strings.ToLower(msg), "invalid") {
		t.Errorf("error message = %q", msg)
	}
}

func TestMCP_UpdateMissing(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	res, _ := s.handleUpdate(ctx, call(map[string]any{"path": "nope.md", "content": "x"}))
	msg := expectError(t, res)
	if !strings.Contains(msg, "does not exist") {
		t.Errorf("error message = %q", msg)
	}
}

func TestMCP_GetSection(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	body := "# Top\n\n## Alpha\nfirst\n\n## Beta\nsecond\n"
	_, _ = s.handleCreate(ctx, call(map[string]any{"path": "n.md", "content": body}))

	res, _ := s.handleGetSection(ctx, call(map[string]any{
		"path": "n.md", "heading": "Alpha",
	}))
	got := resultText(t, res)
	if !strings.Contains(got, "first") {
		t.Errorf("missing alpha content: %s", got)
	}
	if strings.Contains(got, "second") {
		t.Errorf("alpha should not include beta: %s", got)
	}

	// missing heading → error
	res, _ = s.handleGetSection(ctx, call(map[string]any{
		"path": "n.md", "heading": "Gamma",
	}))
	if !strings.Contains(expectError(t, res), "not found") {
		t.Errorf("expected not-found error")
	}
}

func TestMCP_Edit(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	_, _ = s.handleCreate(ctx, call(map[string]any{
		"path":    "n.md",
		"content": "hello world\nhello again\n",
	}))

	// Unique replace
	res, _ := s.handleEdit(ctx, call(map[string]any{
		"path": "n.md", "old_string": "world", "new_string": "earth",
	}))
	body := resultText(t, res)
	if !strings.Contains(body, `"replacements":1`) {
		t.Errorf("expected 1 replacement, got %s", body)
	}
	res, _ = s.handleGet(ctx, call(map[string]any{"path": "n.md"}))
	if !strings.Contains(resultText(t, res), "hello earth") {
		t.Errorf("edit not applied")
	}

	// Ambiguous match without replace_all → error
	res, _ = s.handleEdit(ctx, call(map[string]any{
		"path": "n.md", "old_string": "hello", "new_string": "ciao",
	}))
	msg := expectError(t, res)
	if !strings.Contains(msg, "matches 2 occurrences") {
		t.Errorf("expected ambiguity error, got: %s", msg)
	}

	// Ambiguous with replace_all → ok
	res, _ = s.handleEdit(ctx, call(map[string]any{
		"path": "n.md", "old_string": "hello", "new_string": "ciao",
		"replace_all": true,
	}))
	body = resultText(t, res)
	if !strings.Contains(body, `"replacements":2`) {
		t.Errorf("expected 2 replacements, got: %s", body)
	}

	// Missing string → error
	res, _ = s.handleEdit(ctx, call(map[string]any{
		"path": "n.md", "old_string": "missing", "new_string": "x",
	}))
	if !strings.Contains(expectError(t, res), "not found") {
		t.Errorf("expected not-found error")
	}
}

func TestMCP_Append(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()

	// append to a non-existent file → creates it
	res, _ := s.handleAppend(ctx, call(map[string]any{"path": "log.md", "content": "first"}))
	resultText(t, res)

	// append again
	res, _ = s.handleAppend(ctx, call(map[string]any{"path": "log.md", "content": "second"}))
	resultText(t, res)

	// verify content
	res, _ = s.handleGet(ctx, call(map[string]any{"path": "log.md"}))
	body := resultText(t, res)
	var got noteContent
	_ = json.Unmarshal([]byte(body), &got)
	if !strings.Contains(got.Content, "first") || !strings.Contains(got.Content, "second") {
		t.Errorf("append content = %q", got.Content)
	}
}

func TestMCP_SearchAndDelete(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()

	_, _ = s.handleCreate(ctx, call(map[string]any{
		"path":    "note.md",
		"content": "The gopher language is fun #go",
	}))

	res, _ := s.handleSearch(ctx, call(map[string]any{"query": "gopher"}))
	body := resultText(t, res)
	if !strings.Contains(body, "note.md") {
		t.Errorf("search should find note: %s", body)
	}

	// Delete
	res, _ = s.handleDelete(ctx, call(map[string]any{"path": "note.md"}))
	resultText(t, res)

	// Search should be empty now
	res, _ = s.handleSearch(ctx, call(map[string]any{"query": "gopher"}))
	body = resultText(t, res)
	if strings.Contains(body, "note.md") {
		t.Errorf("note should be gone from index: %s", body)
	}
}

func TestMCP_Backlinks(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()

	_, _ = s.handleCreate(ctx, call(map[string]any{
		"path":    "target.md",
		"content": "# Target",
	}))
	_, _ = s.handleCreate(ctx, call(map[string]any{
		"path":    "source.md",
		"content": "Refers to [[target]] here.",
	}))

	res, _ := s.handleBacklinks(ctx, call(map[string]any{"path": "target.md"}))
	body := resultText(t, res)
	if !strings.Contains(body, "source.md") {
		t.Errorf("backlinks should include source.md: %s", body)
	}
}

func TestMCP_RenameNote(t *testing.T) {
	s, _, dir := newTestServer(t)
	ctx := context.Background()

	_, _ = s.handleCreate(ctx, call(map[string]any{
		"path": "Foo.md", "content": "# Foo",
	}))
	_, _ = s.handleCreate(ctx, call(map[string]any{
		"path": "ref.md", "content": "See [[Foo]] here.",
	}))

	res, _ := s.handleRenameNote(ctx, call(map[string]any{
		"from": "Foo.md", "to": "Bar.md",
	}))
	body := resultText(t, res)
	if !strings.Contains(body, `"to":"Bar.md"`) {
		t.Errorf("missing to in response: %s", body)
	}
	if !strings.Contains(body, "ref.md") {
		t.Errorf("rewritten list missing ref.md: %s", body)
	}
	if _, err := os.Stat(filepath.Join(dir, "Bar.md")); err != nil {
		t.Errorf("new file missing: %v", err)
	}

	// Auto .md extension
	res, _ = s.handleRenameNote(ctx, call(map[string]any{
		"from": "Bar.md", "to": "Baz",
	}))
	body = resultText(t, res)
	if !strings.Contains(body, `"to":"Baz.md"`) {
		t.Errorf("missing canonical to: %s", body)
	}
}

func TestMCP_RenameProject(t *testing.T) {
	s, v, dir := newTestServer(t)
	ctx := context.Background()
	_, _ = v.CreateProject("Old")
	_, _ = s.handleCreate(ctx, call(map[string]any{
		"path": "Old/note.md", "content": "# note",
	}))

	res, _ := s.handleRenameProject(ctx, call(map[string]any{
		"from": "Old", "to": "New",
	}))
	resultText(t, res)
	if _, err := os.Stat(filepath.Join(dir, "New", "note.md")); err != nil {
		t.Errorf("note not under new project: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Old")); err == nil {
		t.Errorf("old dir should be gone")
	}
}

// Bug #1 regression: collection-returning tools must wrap their array
// inside a top-level object so the MCP client (Claude Code) doesn't blow
// up with 'expected record, received array' from its zod validator on
// structuredContent.
func TestMCP_CollectionResultsAreWrapped(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	_, _ = s.handleCreate(ctx, call(map[string]any{"path": "a.md", "content": "alpha"}))
	_, _ = s.handleCreate(ctx, call(map[string]any{"path": "b.md", "content": "[[a]] bravo #demo"}))

	cases := []struct {
		name    string
		fn      func() (*mcplib.CallToolResult, error)
		key     string
	}{
		{"search", func() (*mcplib.CallToolResult, error) {
			return s.handleSearch(ctx, call(map[string]any{"query": "alpha"}))
		}, `"hits":`},
		{"list_notes", func() (*mcplib.CallToolResult, error) {
			return s.handleListNotes(ctx, call(map[string]any{}))
		}, `"notes":`},
		{"list_projects", func() (*mcplib.CallToolResult, error) {
			return s.handleListProjects(ctx, call(map[string]any{}))
		}, `"projects":`},
		{"list_tags", func() (*mcplib.CallToolResult, error) {
			return s.handleListTags(ctx, call(map[string]any{}))
		}, `"tags":`},
		{"notes_by_tag", func() (*mcplib.CallToolResult, error) {
			return s.handleNotesByTag(ctx, call(map[string]any{"tag": "demo"}))
		}, `"notes":`},
		{"backlinks", func() (*mcplib.CallToolResult, error) {
			return s.handleBacklinks(ctx, call(map[string]any{"path": "a.md"}))
		}, `"backlinks":`},
		{"outlinks", func() (*mcplib.CallToolResult, error) {
			return s.handleOutlinks(ctx, call(map[string]any{"path": "b.md"}))
		}, `"outlinks":`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := c.fn()
			if err != nil {
				t.Fatal(err)
			}
			body := resultText(t, res)
			if !strings.HasPrefix(strings.TrimSpace(body), "{") {
				t.Errorf("response is not an object: %s", body)
			}
			if !strings.Contains(body, c.key) {
				t.Errorf("missing wrapper key %q in %s", c.key, body)
			}
		})
	}
}

func TestMCP_DeleteProject(t *testing.T) {
	s, v, dir := newTestServer(t)
	ctx := context.Background()

	// create project + note via vault directly (admin path)
	if _, err := v.CreateProject("Work"); err != nil {
		t.Fatal(err)
	}
	_, _ = s.handleCreate(ctx, call(map[string]any{
		"path": "Work/task.md", "content": "# task",
	}))

	res, _ := s.handleDeleteProject(ctx, call(map[string]any{"name": "Work"}))
	body := resultText(t, res)
	if !strings.Contains(body, "Work/task.md") {
		t.Errorf("expected removed note in response: %s", body)
	}
	if _, err := os.Stat(filepath.Join(dir, "Work")); err == nil {
		t.Errorf("dir still exists")
	}

	// non-existent
	res, _ = s.handleDeleteProject(ctx, call(map[string]any{"name": "Ghost"}))
	expectError(t, res)
}

func TestMCP_ListProjectsAndNotes(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()

	// create a project then a note in it
	res, _ := s.handleCreateProject(ctx, call(map[string]any{"name": "Work"}))
	resultText(t, res)
	_, _ = s.handleCreate(ctx, call(map[string]any{
		"path":    "Work/task.md",
		"content": "# Task",
	}))

	res, _ = s.handleListProjects(ctx, call(map[string]any{}))
	body := resultText(t, res)
	if !strings.Contains(body, "Work") {
		t.Errorf("projects list missing Work: %s", body)
	}

	res, _ = s.handleListNotes(ctx, call(map[string]any{"project": "Work"}))
	body = resultText(t, res)
	if !strings.Contains(body, "Work/task.md") {
		t.Errorf("project listing missing note: %s", body)
	}
}
