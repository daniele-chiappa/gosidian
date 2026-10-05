package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/auth"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// memory_get reads an Obsidian canvas as text: its cards in their groups,
// the notes of its file cards as wikilinks within the token's scope, its
// connections; the JSON with raw:true only; no tool writes it (IMP-144).
func TestCanvas_MemoryGet(t *testing.T) {
	s, _, dir := newTestServer(t)
	ctx := context.Background()
	if res, err := s.handleCreate(ctx, call(map[string]any{"path": "p/plans/a.md", "content": "---\ntitle: Plan A\ntags: [p]\n---\n# Plan A\n"})); err != nil || res.IsError {
		t.Fatalf("create: %v %+v", err, res)
	}
	src := `{"nodes":[{"id":"g","type":"group","x":0,"y":0,"width":800,"height":400,"label":"Fase 1"},` +
		`{"id":"t","type":"text","x":10,"y":10,"width":200,"height":100,"text":"Scrivere i test"},` +
		`{"id":"f","type":"file","x":300,"y":10,"width":200,"height":100,"file":"p/plans/a.md"},` +
		`{"id":"q","type":"file","x":900,"y":10,"width":200,"height":100,"file":"q/secret.md"}],` +
		`"edges":[{"id":"e","fromNode":"t","toNode":"f","label":"poi"}]}`
	if err := os.MkdirAll(filepath.Join(dir, "q"), 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, data := range map[string]string{"p/roadmap.canvas": src, "q/secret.md": "# Secret\n"} {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var out struct {
		Kind, Content, Source, Hint string
	}
	get := func(ctx context.Context, args map[string]any) {
		t.Helper()
		out = struct{ Kind, Content, Source, Hint string }{}
		res, _ := s.handleGet(ctx, call(args))
		if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
			t.Fatal(err)
		}
	}
	pctx := context.WithValue(ctx, tokenCtxKey, &auth.Token{Name: "p-agent", Projects: []string{"p"}, Scopes: []string{auth.ScopeRead}})
	get(pctx, map[string]any{"path": "p/roadmap.canvas"})
	for _, want := range []string{"- **Group** Fase 1\n  - **Text**\n    > Scrivere i test\n  - **File** [[p/plans/a]]\n", "- **File** `q/secret.md`", "- «Scrivere i test» → [[p/plans/a]]: poi"} {
		if !strings.Contains(out.Content, want) {
			t.Errorf("content lacks %q:\n%s", want, out.Content)
		}
	}
	if out.Kind != "canvas" || out.Source != "" || !strings.Contains(out.Hint, "pass raw:true for its JSON") {
		t.Errorf("canvas = %+v", out)
	}
	get(ctx, map[string]any{"path": "p/roadmap.canvas", "raw": true})
	if out.Source != src || !strings.Contains(out.Content, "- **File** [[q/secret]]") {
		t.Errorf("raw, every project = %+v", out)
	}
	res, _ := s.handleEdit(ctx, call(map[string]any{"path": "p/roadmap.canvas", "old_string": "Fase", "new_string": "Phase"}))
	if !res.IsError || !strings.Contains(res.Content[0].(mcplib.TextContent).Text, "Obsidian canvas, read-only") {
		t.Errorf("edit = %+v", res)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "p/roadmap.canvas")); string(data) != src {
		t.Errorf("the canvas changed: %q", data)
	}
}
