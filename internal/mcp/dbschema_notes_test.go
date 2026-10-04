package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

const testDBNote = "---\ntitle: Backlog\ntags: [p, type:index]\ntype: database\nsource: p/docs/improvements\nfields:\n" +
	"  id: {type: text, required: true}\n  status: {type: select, required: true, options: [open, done]}\n" +
	"  closed: {type: date}\n---\n\n# Backlog\n"

func TestWrite_NoticeOnSchemaProblems(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	create := func(path, content string) *mcplib.CallToolResult {
		t.Helper()
		req := call(map[string]any{"path": path, "content": content})
		req.Params.Name = "memory_create"
		res, err := callNotesMiddleware(s.handleCreate)(ctx, req)
		if err != nil || res == nil || res.IsError {
			t.Fatalf("create %s: %v %+v", path, err, res)
		}
		return res
	}
	notices := func(res *mcplib.CallToolResult) string {
		if obj, ok := structuredObject(res.StructuredContent); !ok || obj[noticesField] == nil {
			return ""
		}
		return strings.Join(resultNotices(t, res), " ")
	}

	if n := notices(create("p/docs/improvements.md", testDBNote)); n != "" {
		t.Errorf("a valid database note got a notice: %q", n)
	}
	if n := notices(create("p/docs/improvements/IMP-001.md",
		"---\ntitle: ok\nid: IMP-001\nstatus: open\ntags: [p, type:doc]\n---\n")); n != "" {
		t.Errorf("a valid row got a notice: %q", n)
	}
	n := notices(create("p/docs/improvements/IMP-002.md",
		"---\ntitle: drift\nid: IMP-002\nstatus: done\nresolved: 2026-10-02\ntags: [p, type:doc]\n---\n"))
	for _, want := range []string{"breaks its schema", `"resolved"`, "id, status, closed"} {
		if !strings.Contains(n, want) {
			t.Errorf("drifted row: notice %q lacks %q", n, want)
		}
	}
	if n := notices(create("p/docs/notes.md", "---\ntitle: x\nresolved: yes\n---\n")); n != "" {
		t.Errorf("a note outside the database got a notice: %q", n)
	}

	res, err := s.handleBootstrap(ctx, call(map[string]any{"project": "p", "mode": "lite"}))
	if err != nil || res.IsError {
		t.Fatalf("bootstrap: %v %+v", err, res)
	}
	var out struct {
		Databases []struct {
			Path     string `json:"path"`
			Source   string `json:"source"`
			Template string `json:"template"`
			Rows     int    `json:"rows"`
			Fields   []struct {
				Name string `json:"name"`
			} `json:"fields"`
		} `json:"databases"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Databases) != 1 || out.Databases[0].Path != "p/docs/improvements.md" ||
		out.Databases[0].Rows != 2 || len(out.Databases[0].Fields) != 3 || out.Databases[0].Template != "" {
		t.Errorf("bootstrap databases = %+v", out.Databases)
	}

	// A template the database names shows in databases[].
	if res, _ := s.handleUpdate(ctx, call(map[string]any{"path": "p/docs/improvements.md",
		"content": strings.Replace(testDBNote, "source: p/docs/improvements\n", "source: p/docs/improvements\ntemplate: p/templates/imp\n", 1)})); res.IsError {
		t.Fatalf("update: %s", expectError(t, res))
	}
	res, _ = s.handleBootstrap(ctx, call(map[string]any{"project": "p", "mode": "lite"}))
	if err := json.Unmarshal([]byte(res.Content[0].(mcplib.TextContent).Text), &out); err != nil {
		t.Fatal(err)
	}
	if out.Databases[0].Template != "p/templates/imp.md" {
		t.Errorf("template = %q", out.Databases[0].Template)
	}
}

// A write whose frontmatter is not valid YAML gets a notice; a valid one
// does not (IMP-138).
func TestNoticeInvalidYAML(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	create := func(path, content string) string {
		t.Helper()
		req := call(map[string]any{"path": path, "content": content})
		req.Params.Name = "memory_create"
		res, err := callNotesMiddleware(s.handleCreate)(ctx, req)
		if err != nil || res == nil || res.IsError {
			t.Fatalf("create %s: %v %+v", path, err, res)
		}
		obj, ok := structuredObject(res.StructuredContent)
		if !ok || obj[noticesField] == nil {
			return ""
		}
		b, _ := json.Marshal(obj[noticesField])
		return string(b)
	}
	if n := create("p/ok.md", "---\ntitle: \"Plan: one\"\ntags: [p]\n---\n"); n != "" {
		t.Errorf("valid YAML got a notice: %s", n)
	}
	n := create("p/bad.md", "---\ntitle: bad\ndescription: Plan: one\ntags: [p]\n---\n")
	if !strings.Contains(n, "not valid YAML") || !strings.Contains(n, "line 3 of the note") || strings.Contains(n, "database note") {
		t.Errorf("notice = %s", n)
	}
}
