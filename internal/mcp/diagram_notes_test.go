package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/gosidian/gosidian/internal/diagram"
)

// A diagram that does not read leaves a notice on the write, on the note's
// line, and the note is saved; memory_get says each diagram in words; the
// bootstrap tells the format (ADR-036).
func TestDiagrams_NoticeGetAndBootstrap(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	write := func(path, content string) *mcplib.CallToolResult {
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
	write("p/target.md", "# Target\n")
	good := "---\ntitle: Flow\ntype: diagram\n---\n\n```diagram\nkind: flow\nnodes:\n  - {id: a, label: Start, link: \"[[p/target]]\"}\n  - {id: b, label: Done}\nedges: [\"a -> b: go\"]\n```\n"
	if n := notices(write("p/flow.md", good)); n != "" {
		t.Errorf("a valid diagram got a notice: %q", n)
	}
	// Line 6 of the note opens the fence; the bad edge is line 4 of the block.
	bad := "# Bad\n\nText.\n\nMore.\n```diagram\nkind: flow\nnodes: [a]\nedges:\n  - \"a -> nobody\"\n```\n"
	n := notices(write("p/bad.md", bad))
	for _, want := range []string{"block at line 6", "line 10:", `"nobody"`, "saved as written"} {
		if !strings.Contains(n, want) {
			t.Errorf("notice %q lacks %q", n, want)
		}
	}
	if _, err := s.vault.Load("p/bad.md"); err != nil {
		t.Errorf("the note with a bad diagram was not saved: %v", err)
	}

	res, err := s.handleGet(ctx, call(map[string]any{"path": "p/flow.md"}))
	if err != nil || res.IsError {
		t.Fatalf("get: %v %+v", err, res)
	}
	var got struct {
		Diagrams []struct {
			Kind string `json:"kind"`
			Line int    `json:"line"`
			Text string `json:"text"`
		} `json:"diagrams"`
	}
	if err := json.Unmarshal([]byte(resultText(t, res)), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Diagrams) != 1 || got.Diagrams[0].Kind != "flow" || got.Diagrams[0].Line != 6 || got.Diagrams[0].Text != "Flow (TB), 2 nodes, 1 edge: Start → Done (go)" {
		t.Errorf("diagrams = %+v", got.Diagrams)
	}
	plain, _ := s.handleGet(ctx, call(map[string]any{"path": "p/bad.md"}))
	if !strings.Contains(resultText(t, plain), `"problems"`) {
		t.Errorf("a bad diagram's problems are not in memory_get: %s", resultText(t, plain))
	}

	// The node's link is a backlink of the target.
	if res, _ := s.handleBacklinks(ctx, call(map[string]any{"path": "p/target.md"})); !strings.Contains(resultText(t, res), "p/flow.md") {
		t.Errorf("backlinks of p/target.md: %s", resultText(t, res))
	}

	boot, err := s.handleBootstrap(ctx, call(map[string]any{"project": "p", "mode": "lite"}))
	if err != nil || boot.IsError {
		t.Fatalf("bootstrap: %v %+v", err, boot)
	}
	text := resultText(t, boot)
	for _, want := range []string{`"diagrams":{"format":1`, `"kinds":["flow","tree","mindmap","fishbone","venn","matrix","gantt"]`} {
		if !strings.Contains(text, want) {
			t.Errorf("bootstrap lacks %s", want)
		}
	}
}

// The example the bootstrap gives agents is a diagram that reads.
func TestDiagrams_BootstrapExampleReads(t *testing.T) {
	if _, problems := diagram.Parse(diagramCapability().Example); len(problems) > 0 {
		t.Errorf("the bootstrap's example does not read: %v", problems)
	}
}

// A twin left older than its diagram by a write gets a notice, and
// memory_get says so.
func TestDiagrams_StaleTwinNotice(t *testing.T) {
	s, _, _ := newTestServer(t)
	ctx := context.Background()
	yaml := "kind: tree\nroot: A\n"
	twin := "```mermaid\n" + diagram.TwinMark + " " + diagram.Hash(yaml) + " — made from the diagram above\nflowchart TB\n```\n"
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
	if res := create("p/current.md", "# C\n\n```diagram\n"+yaml+"```\n\n"+twin); res.StructuredContent != nil {
		if obj, ok := structuredObject(res.StructuredContent); ok && obj[noticesField] != nil {
			t.Errorf("a current twin got a notice: %v", resultNotices(t, res))
		}
	}
	res := create("p/stale.md", "# S\n\n```diagram\nkind: tree\nroot: B\n```\n\n"+twin)
	n := strings.Join(resultNotices(t, res), " ")
	for _, want := range []string{"line 8 is the twin of the diagram at line 3", "writes the twin again"} {
		if !strings.Contains(n, want) {
			t.Errorf("notice %q lacks %q", n, want)
		}
	}
	got, _ := s.handleGet(ctx, call(map[string]any{"path": "p/stale.md"}))
	if !strings.Contains(resultText(t, got), `"twin":"stale"`) {
		t.Errorf("memory_get: %s", resultText(t, got))
	}
}
