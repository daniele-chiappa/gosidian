package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func TestBootstrapAndGet_RenderViews(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	write := func(path, content string) {
		t.Helper()
		if res, err := s.handleCreate(ctx, call(map[string]any{"path": path, "content": content})); err != nil || res.IsError {
			t.Fatalf("create %s: %v %+v", path, err, res)
		}
	}
	write("p/docs/improvements/IMP-001.md", "---\ntitle: \"IMP-001 — open one\"\nid: IMP-001\nstatus: open\n---\n")
	write("p/docs/improvements/IMP-002.md", "---\ntitle: \"IMP-002 — done one\"\nid: IMP-002\nstatus: done\n---\n")
	hot := "---\ntitle: Hot\ntags: [p]\n---\n# Hot\n\n## Aperti\n\n```view\nfrom: p/docs/improvements\nwhere:\n  - status = open\ncolumns: [id, title]\n```\n"
	write("p/hot.md", hot)

	type file struct {
		Content   string `json:"content"`
		ETag      string `json:"etag"`
		ViewsETag string `json:"views_etag"`
		Unchanged bool   `json:"unchanged"`
	}
	boot := func(known map[string]any) file {
		t.Helper()
		args := map[string]any{"project": "p", "mode": "full"}
		if known != nil {
			args["known_etags"] = known
		}
		res, err := s.handleBootstrap(ctx, call(args))
		if err != nil || res.IsError {
			t.Fatalf("bootstrap: %v %+v", err, res)
		}
		var out struct {
			Hot file `json:"hot_md"`
		}
		if err := json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &out); err != nil {
			t.Fatal(err)
		}
		return out.Hot
	}

	f := boot(nil)
	if !strings.Contains(f.Content, "```view\nfrom: p/docs/improvements") || !strings.Contains(f.Content, "gosidian:view-result") ||
		!strings.Contains(f.Content, `IMP-001 — open one`) || strings.Contains(f.Content, "IMP-002 — done one") {
		t.Errorf("hot_md should keep the spec and show only the open row:\n%s", f.Content)
	}
	if f.ViewsETag == "" || f.ViewsETag == f.ETag || !strings.HasPrefix(f.ViewsETag, f.ETag+"+v") {
		t.Errorf("etag %q views_etag %q", f.ETag, f.ViewsETag)
	}
	if again := boot(map[string]any{"p/hot.md": f.ETag}); again.Unchanged {
		t.Error("the plain etag must not prove a note with views unchanged")
	}
	if again := boot(map[string]any{"p/hot.md": f.ViewsETag}); !again.Unchanged {
		t.Error("an unchanged views_etag should report unchanged")
	}
	// The data under the view changes, the file does not: not unchanged.
	if res, err := s.handleEdit(ctx, call(map[string]any{"path": "p/docs/improvements/IMP-002.md", "old_string": "status: done", "new_string": "status: open"})); err != nil || res.IsError {
		t.Fatalf("edit: %v %+v", err, res)
	}
	if again := boot(map[string]any{"p/hot.md": f.ViewsETag}); again.Unchanged || !strings.Contains(again.Content, "IMP-002") {
		t.Errorf("a changed view must come back with the new row: %+v", again)
	}

	get := func(args map[string]any) (string, bool) {
		t.Helper()
		res, err := s.handleGet(ctx, call(args))
		if err != nil || res.IsError {
			t.Fatalf("get: %v %+v", err, res)
		}
		var out struct {
			Content       string `json:"content"`
			ViewsRendered bool   `json:"views_rendered"`
		}
		_ = json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &out)
		return out.Content, out.ViewsRendered
	}
	if c, r := get(map[string]any{"path": "p/hot.md"}); c != hot || r {
		t.Errorf("memory_get must return the file as stored by default (rendered=%v):\n%s", r, c)
	}
	if c, r := get(map[string]any{"path": "p/hot.md", "render_views": true}); !r || !strings.Contains(c, "gosidian:view-result") {
		t.Errorf("render_views: rendered=%v\n%s", r, c)
	}
}

// BUG-087: a read that returns view blocks without their rows says so, and
// an outline marks the sections that hold them, the lite bootstrap included.
func TestReads_FlagUncomputedViews(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	hot := "---\ntitle: Hot\ntags: [p]\n---\n# Hot\n\n## Focus\n\ntext\n\n## Backlog\n\n```view\nfrom: p/docs/improvements\n```\n\nBugs:\n\n```view\nfrom: p/docs/bugs\n```\n\n## Hot files\n\n- `x`\n"
	if res, err := s.handleCreate(ctx, call(map[string]any{"path": "p/hot.md", "content": hot})); err != nil || res.IsError {
		t.Fatalf("create: %v %+v", err, res)
	}
	read := func(h func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error), args map[string]any, out any) {
		t.Helper()
		res, err := h(ctx, call(args))
		if err != nil || res.IsError {
			t.Fatalf("%v: %v %+v", args, err, res)
		}
		if err := json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), out); err != nil {
			t.Fatal(err)
		}
	}
	type heading struct {
		Text  string `json:"text"`
		Views int    `json:"views"`
	}
	wantViews := func(where string, hs []heading) {
		t.Helper()
		got := map[string]int{}
		for _, h := range hs {
			got[h.Text] = h.Views
		}
		if got["Backlog"] != 2 || got["Focus"] != 0 || got["Hot"] != 0 || got["Hot files"] != 0 {
			t.Errorf("%s: views per heading %v, want only Backlog: 2", where, got)
		}
	}

	var sec struct {
		Hint string `json:"hint"`
	}
	read(s.handleGetSection, map[string]any{"path": "p/hot.md", "heading": "Backlog"}, &sec)
	if !strings.Contains(sec.Hint, "2 ```view block(s)") || !strings.Contains(sec.Hint, "render_views:true") {
		t.Errorf("get_section without render_views: hint %q", sec.Hint)
	}
	sec.Hint = ""
	read(s.handleGetSection, map[string]any{"path": "p/hot.md", "heading": "Focus"}, &sec)
	if sec.Hint != "" {
		t.Errorf("a section without views has no hint: %q", sec.Hint)
	}
	read(s.handleGetSection, map[string]any{"path": "p/hot.md", "heading": "Backlog", "render_views": true}, &sec)
	if sec.Hint != "" {
		t.Errorf("computed views need no hint: %q", sec.Hint)
	}

	var get struct {
		Hint string `json:"hint"`
	}
	read(s.handleGet, map[string]any{"path": "p/hot.md"}, &get)
	if !strings.Contains(get.Hint, "2 ```view block(s)") {
		t.Errorf("memory_get without render_views: hint %q", get.Hint)
	}
	get.Hint = ""
	read(s.handleGet, map[string]any{"path": "p/hot.md", "render_views": true}, &get)
	if get.Hint != "" {
		t.Errorf("memory_get with render_views: hint %q", get.Hint)
	}

	var ol struct {
		Headings []heading `json:"headings"`
	}
	read(s.handleGetOutline, map[string]any{"path": "p/hot.md"}, &ol)
	wantViews("get_outline", ol.Headings)

	var batch struct {
		Results []struct {
			Headings []heading `json:"headings"`
			Hint     string    `json:"hint"`
		} `json:"results"`
	}
	read(s.handleBatchGet, map[string]any{"paths": []any{"p/hot.md"}}, &batch)
	if len(batch.Results) != 1 || !strings.Contains(batch.Results[0].Hint, "memory_get and render_views:true") {
		t.Errorf("batch_get content: %+v", batch.Results)
	}
	read(s.handleBatchGet, map[string]any{"paths": []any{"p/hot.md"}, "mode": "outline"}, &batch)
	wantViews("batch_get outline", batch.Results[0].Headings)

	var boot struct {
		Hot struct {
			Content  string    `json:"content"`
			Headings []heading `json:"headings"`
			Hint     string    `json:"hint"`
		} `json:"hot_md"`
	}
	read(s.handleBootstrap, map[string]any{"project": "p", "mode": "lite"}, &boot)
	if boot.Hot.Content != "" || !strings.Contains(boot.Hot.Hint, "render_views:true") {
		t.Errorf("lite hot_md: content %q hint %q", boot.Hot.Content, boot.Hot.Hint)
	}
	wantViews("lite bootstrap", boot.Hot.Headings)
}

// The bootstrap holds the views of hot.md to their budget and decides the
// lite shape on the prose: a short hot.md with a long view stays whole, its
// view cut, and maintenance says so (IMP-136).
func TestBootstrap_ViewsBudget(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	for i := 1; i <= 50; i++ {
		path := fmt.Sprintf("p/docs/improvements/IMP-%03d.md", i)
		content := fmt.Sprintf("---\ntitle: \"IMP-%03d — %s\"\nid: IMP-%03d\nstatus: open\n---\n", i, strings.Repeat("a fairly long title ", 9), i)
		if res, _ := s.handleCreate(ctx, call(map[string]any{"path": path, "content": content})); res.IsError {
			t.Fatalf("seed %s: %s", path, expectError(t, res))
		}
	}
	hot := "---\ntitle: Hot\ntags: [p]\n---\n# Hot\n\n## Aperti\n\n```view\nfrom: p/docs/improvements\nwhere:\n  - status = open\nsort: id asc\ncolumns: [title, status]\nlimit: 200\n```\n"
	if res, _ := s.handleCreate(ctx, call(map[string]any{"path": "p/hot.md", "content": hot})); res.IsError {
		t.Fatalf("seed hot: %s", expectError(t, res))
	}
	res, _ := s.handleBootstrap(ctx, call(map[string]any{"project": "p"}))
	var out struct {
		Hot         bootstrapFile        `json:"hot_md"`
		Maintenance bootstrapMaintenance `json:"maintenance"`
	}
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatal(err)
	}
	if out.Hot.AutoLite || out.Hot.Content == "" {
		t.Fatalf("a short hot.md stays whole: auto_lite=%v", out.Hot.AutoLite)
	}
	if n := len(out.Hot.Content); n > len(hot)+bootstrapViewsBudget+512 {
		t.Errorf("hot_md is %d bytes, past the views budget", n)
	}
	if !strings.Contains(out.Hot.Content, "of 50 notes, cut to fit: all of them with memory_query(") || !strings.Contains(out.Hot.Content, "IMP-001") {
		t.Errorf("the view should be cut with the query that returns all:\n%s", out.Hot.Content[len(out.Hot.Content)-600:])
	}
	m := out.Maintenance
	if !m.HotViewsCut || m.HotComputedSize < 8*1024 || !m.Attention {
		t.Errorf("maintenance = %+v", m)
	}
	// The query given in the tail line returns every row.
	q := runQuery(t, s, ctx, map[string]any{"project": "p", "from": []any{"p/docs/improvements"},
		"where": where(map[string]any{"field": "status", "op": "eq", "value": "open"}), "limit": float64(50)})
	if q.Total != 50 || len(q.Notes) != 50 {
		t.Errorf("memory_query from: total %d, notes %d", q.Total, len(q.Notes))
	}
}
