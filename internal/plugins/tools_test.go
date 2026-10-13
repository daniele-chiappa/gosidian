package plugins

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// toolPlugin is a plugin with a node and two tools for the agents: search
// (read) answers with the arguments and the _meta it was called with.
func toolPlugin(t *testing.T, manifest map[string]any) *httptest.Server {
	t.Helper()
	s := server.NewMCPServer("vec", "1.0.0", server.WithToolCapabilities(false))
	s.AddTool(mcp.NewTool(ManifestTool), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		b, _ := json.Marshal(manifest)
		return mcp.NewToolResultText(string(b)), nil
	})
	echo := func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		meta := map[string]any{}
		if req.Params.Meta != nil {
			meta = req.Params.Meta.AdditionalFields
		}
		return mcp.NewToolResultStructured(map[string]any{"args": req.GetArguments(), "meta": meta}, "echo"), nil
	}
	s.AddTool(mcp.NewTool("search"), echo)
	s.AddTool(mcp.NewTool("put"), echo)
	ts := httptest.NewServer(server.NewStreamableHTTPServer(s))
	t.Cleanup(ts.Close)
	return ts
}

func vecManifest() map[string]any {
	return map[string]any{
		"name": "vec", "version": "1.0", "gosidian": 1,
		"nodes": []any{map[string]any{"name": "search", "tool": "search"}},
		"tools": []any{
			map[string]any{"name": "search", "access": "read", "description": "Similar chunks",
				"input_schema": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}}},
			map[string]any{"name": "put", "access": "write"},
		},
		"permissions": map[string]any{"files": false, "network": []string{}},
	}
}

func TestManifest_Tools(t *testing.T) {
	base := func(tools ...map[string]any) []byte {
		m := map[string]any{"name": "vec", "version": "1", "gosidian": 1,
			"nodes":       []any{map[string]any{"name": "convert", "tool": "convert", "files": []string{"file"}}},
			"permissions": map[string]any{"files": true, "network": []string{}}}
		list := []any{}
		for _, t := range tools {
			list = append(list, t)
		}
		m["tools"] = list
		b, _ := json.Marshal(m)
		return b
	}
	ok := map[string]any{"name": "search", "access": "read", "input_schema": map[string]any{"type": "object"}}
	if _, err := ParseManifest(base(ok), "vec"); err != nil {
		t.Errorf("a valid tool refused: %v", err)
	}
	only, _ := json.Marshal(map[string]any{"name": "vec", "version": "1", "gosidian": 1, "tools": []any{ok},
		"permissions": map[string]any{"files": false, "network": []string{}}})
	if _, err := ParseManifest(only, "vec"); err != nil {
		t.Errorf("a manifest with tools only refused: %v", err)
	}
	for why, tool := range map[string]map[string]any{
		"name":          {"name": "Search", "access": "read"},
		"access":        {"name": "search"},
		"gosidian_":     {"name": "gosidian_look", "access": "read"},
		"files":         {"name": "convert", "access": "read"},
		"too long":      {"name": strings.Repeat("a", MaxToolName), "access": "read"},
		"project":       {"name": "search", "access": "read", "input_schema": map[string]any{"type": "object", "properties": map[string]any{"project": map[string]any{"type": "string"}}}},
		"not an object": {"name": "search", "access": "read", "input_schema": map[string]any{"type": "string"}},
	} {
		if _, err := ParseManifest(base(tool), "vec"); err == nil {
			t.Errorf("%s: the tool %v was accepted", why, tool)
		}
	}
	if _, err := ParseManifest(base(ok, ok), "vec"); err == nil {
		t.Error("a tool twice was accepted")
	}
	// A tool's schema is in the sha: the owner accepts what the agents see.
	a, _ := ParseManifest(base(ok), "vec")
	b, _ := ParseManifest(base(map[string]any{"name": "search", "access": "write", "input_schema": map[string]any{"type": "object"}}), "vec")
	if a.SHA() == b.SHA() {
		t.Error("the access of a tool does not change the sha")
	}
	// A manifest without tools keeps the sha it had before tools existed.
	none, _ := json.Marshal(map[string]any{"name": "vec", "version": "1", "gosidian": 1,
		"nodes":       []any{map[string]any{"name": "convert", "tool": "convert"}},
		"permissions": map[string]any{"files": false, "network": []string{}}})
	m, _ := ParseManifest(none, "vec")
	if raw, _ := json.Marshal(m); strings.Contains(string(raw), "tools") {
		t.Errorf("a manifest without tools marshals them: %s", raw)
	}
}

func TestNew_RefusesMemory(t *testing.T) {
	if _, err := New([]Config{{Name: "memory", URL: "http://x.test/mcp"}}, t.TempDir(), nil, "test", nil); err == nil {
		t.Error("a plugin named memory was registered: its tools would be memory_*")
	}
}

func TestRegistry_ExposeAndCallTool(t *testing.T) {
	ts := toolPlugin(t, vecManifest())
	reg, err := New([]Config{{Name: "vec", URL: ts.URL + "/mcp"}}, t.TempDir(), nil, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := reg.Expose("vec", "p", "test"); err == nil || !strings.Contains(err.Error(), "not accepted") {
		t.Errorf("expose before accept: %v", err)
	}
	reg.Check(ctx)
	st, _ := reg.State("vec")
	if _, err := reg.Accept(ctx, "vec", st.SHA, "test"); err != nil {
		t.Fatal(err)
	}
	tools := reg.Tools()
	if len(tools) != 2 || tools[0].Name != "vec_search" || tools[1].Name != "vec_put" || tools[1].Spec.Access != AccessWrite {
		t.Fatalf("tools = %+v", tools)
	}
	if _, err := reg.Expose("vec", "p", "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Expose("vec", "p", "test"); err == nil {
		t.Error("exposed twice in the same project")
	}
	if _, err := reg.Expose("vec", "q", "test"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(reg.ExposedIn("vec"), ","); got != "p,q" {
		t.Errorf("exposed in %s", got)
	}

	res, err := reg.CallTool(ctx, "vec", "search", map[string]any{"query": "pulsante"}, map[string]any{MetaProject: "p", MetaCaller: "t1"})
	if err != nil || res.IsError {
		t.Fatalf("call: %v %+v", err, res)
	}
	var out struct {
		Args map[string]any `json:"args"`
		Meta map[string]any `json:"meta"`
	}
	if err := json.Unmarshal(resultJSON(res), &out); err != nil {
		t.Fatal(err)
	}
	if out.Args["query"] != "pulsante" || out.Meta[MetaProject] != "p" || out.Meta[MetaCaller] != "t1" {
		t.Errorf("the plugin got %+v", out)
	}
	if _, err := reg.CallTool(ctx, "vec", "nope", nil, nil); err == nil {
		t.Error("a tool the manifest does not list was called")
	}

	// The exposures follow the projects.
	if err := reg.ProjectRenamed("p", "p2"); err != nil {
		t.Fatal(err)
	}
	if err := reg.ProjectRemoved("q"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(reg.ExposedIn("vec"), ","); got != "p2" {
		t.Errorf("after rename and removal: %s", got)
	}
	if err := reg.Unexpose("vec", "p2"); err != nil {
		t.Fatal(err)
	}
	if err := reg.Unexpose("vec", "p2"); err == nil {
		t.Error("unexposed twice")
	}
	if len(reg.ExposedIn("vec")) != 0 {
		t.Error("still exposed")
	}
}

func TestRegistry_ExposeNeedsTools(t *testing.T) {
	m := vecManifest()
	delete(m, "tools")
	ts := toolPlugin(t, m)
	reg, err := New([]Config{{Name: "vec", URL: ts.URL + "/mcp"}}, t.TempDir(), nil, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	reg.Check(ctx)
	st, _ := reg.State("vec")
	if _, err := reg.Accept(ctx, "vec", st.SHA, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Expose("vec", "p", "test"); err == nil || !strings.Contains(err.Error(), "no tools") {
		t.Errorf("expose without tools: %v", err)
	}
}
