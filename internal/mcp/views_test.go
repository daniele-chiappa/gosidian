package mcp

import (
	"context"
	"encoding/json"
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
