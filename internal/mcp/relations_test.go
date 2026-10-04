package mcp

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// Relations for agents (IMP-127 iteration 2): frontmatter links in
// backlinks and outlinks with their field, link conditions in
// memory_query, and the row views of a row with render_views (IMP-139).
func TestRelations_AgentTools(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	write := func(path, content string) {
		t.Helper()
		if res, err := s.handleCreate(ctx, call(map[string]any{"path": path, "content": content})); err != nil || res.IsError {
			t.Fatalf("create %s: %v %+v", path, err, res)
		}
	}
	write("p/docs/bugs.md", "---\ntitle: Bugs\ntags: [p, type:index]\ntype: database\nsource: p/docs/bugs\nfields:\n  id: {type: text}\n  status: {type: select, options: [open, done]}\n"+
		"row_views:\n  - title: Plans that fix it\n    from: p/plans\n    where: [related contains this]\n    columns: [title, status]\n---\n# Bugs\n")
	write("p/docs/bugs/BUG-1.md", "---\ntitle: BUG-1\nid: BUG-1\nstatus: open\ntags: [p]\n---\n\n# BUG-1\n\nThe bug.\n")
	write("p/plans/fix.md", "---\ntitle: Fix\nstatus: done\nrelated: [\"[[BUG-1]]\"]\ntags: [p]\n---\n\nBody.\n")
	write("p/plans/mention.md", "---\ntitle: Mention\nstatus: draft\ntags: [p]\n---\n\nSee [[p/docs/bugs/BUG-1]].\n")

	decode := func(res *mcplib.CallToolResult, err error, v any) {
		t.Helper()
		if err != nil || res.IsError {
			t.Fatalf("call: %v %+v", err, res)
		}
		if err := json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), v); err != nil {
			t.Fatal(err)
		}
	}

	var bl struct {
		Backlinks []backlinkEntry `json:"backlinks"`
	}
	res, err := s.handleBacklinks(ctx, call(map[string]any{"path": "p/docs/bugs/BUG-1.md"}))
	decode(res, err, &bl)
	want := []backlinkEntry{{Path: "p/plans/fix.md", Title: "Fix", Fields: []string{"related"}}, {Path: "p/plans/mention.md", Title: "Mention"}}
	if !reflect.DeepEqual(bl.Backlinks, want) {
		t.Errorf("backlinks = %+v", bl.Backlinks)
	}
	var ol struct {
		Outlinks []outlinkEntry `json:"outlinks"`
	}
	res, err = s.handleOutlinks(ctx, call(map[string]any{"path": "p/plans/fix.md"}))
	decode(res, err, &ol)
	if len(ol.Outlinks) != 1 || ol.Outlinks[0].Field != "related" || ol.Outlinks[0].ResolvedPath != "p/docs/bugs/BUG-1.md" {
		t.Errorf("outlinks = %+v", ol.Outlinks)
	}

	var q struct {
		Notes []queryNote `json:"notes"`
	}
	res, err = s.handleQuery(ctx, call(map[string]any{"project": "p", "sort": "path",
		"where": []any{map[string]any{"field": "links", "op": "contains", "value": "[[BUG-1]]"}}}))
	decode(res, err, &q)
	if len(q.Notes) != 2 || q.Notes[0].Path != "p/plans/fix.md" || q.Notes[1].Path != "p/plans/mention.md" {
		t.Errorf("links contains = %+v", q.Notes)
	}
	res, err = s.handleQuery(ctx, call(map[string]any{"project": "p",
		"where": []any{map[string]any{"field": "related", "op": "contains", "value": "[[p/nothing]]"}}}))
	if err != nil || !res.IsError || !strings.Contains(res.Content[0].(mcplib.TextContent).Text, "names no note") {
		t.Errorf("a link to no note should be an error: %+v", res)
	}

	var got struct {
		Content       string `json:"content"`
		Hint          string `json:"hint"`
		ViewsRendered bool   `json:"views_rendered"`
	}
	res, err = s.handleGet(ctx, call(map[string]any{"path": "p/docs/bugs/BUG-1.md"}))
	decode(res, err, &got)
	if strings.Contains(got.Content, "row-views") || !strings.Contains(got.Hint, "1 row view(s) of p/docs/bugs.md not shown") {
		t.Errorf("without render_views: hint %q", got.Hint)
	}
	res, err = s.handleGet(ctx, call(map[string]any{"path": "p/docs/bugs/BUG-1.md", "render_views": true}))
	decode(res, err, &got)
	if !got.ViewsRendered || !strings.HasPrefix(got.Content, "---\ntitle: BUG-1") || !strings.Contains(got.Content, "**Plans that fix it**") ||
		!strings.Contains(got.Content, "[[p/plans/fix\\|Fix]] | done") || strings.Contains(got.Content, "Mention") {
		t.Errorf("with render_views:\n%s", got.Content)
	}
	var sec struct {
		Content string `json:"content"`
		Hint    string `json:"hint"`
	}
	res, err = s.handleGetSection(ctx, call(map[string]any{"path": "p/docs/bugs/BUG-1.md", "heading": "BUG-1", "render_views": true}))
	decode(res, err, &sec)
	if !strings.HasPrefix(sec.Content, "# BUG-1") || !strings.Contains(sec.Content, "**Plans that fix it**") {
		t.Errorf("get_section with render_views:\n%s", sec.Content)
	}
	// A note that is not a row has no row views and no hint about them.
	got.Hint = ""
	res, err = s.handleGet(ctx, call(map[string]any{"path": "p/plans/fix.md"}))
	decode(res, err, &got)
	if strings.Contains(got.Hint, "row view") {
		t.Errorf("not a row: hint %q", got.Hint)
	}
}
