package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/gosidian/gosidian/internal/plugins"
)

// mapPlugin is a plugin with a graph, chunks: three nodes, two edges, one
// edge to a node it does not have, a link to a note and one to a file
// that is no note; it counts its calls and answers with the project of
// the call's _meta as a node's group.
func mapPlugin(t *testing.T, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	s := server.NewMCPServer("vec", "1.0.0", server.WithToolCapabilities(false))
	s.AddTool(mcp.NewTool(plugins.ManifestTool), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		b, _ := json.Marshal(map[string]any{
			"name": "vec", "version": "1.0", "gosidian": 1,
			"graphs": []any{map[string]any{"name": "chunks", "description": "The chunks and their neighbours",
				"input_schema": map[string]any{"type": "object", "properties": map[string]any{"collection": map[string]any{"type": "string"}}}}},
			"permissions": map[string]any{"files": false, "network": []string{}},
		})
		return mcp.NewToolResultText(string(b)), nil
	})
	s.AddTool(mcp.NewTool(plugins.GraphTool), func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		project, _ := req.Params.Meta.AdditionalFields[plugins.MetaProject].(string)
		with, _ := req.GetArguments()["with"].(map[string]any)
		b, _ := json.Marshal(map[string]any{
			"nodes": []any{
				map[string]any{"id": "a", "label": "A", "group": project, "link": "rc/a.md", "text": "first"},
				map[string]any{"id": "b", "group": with["collection"], "link": "rc/a.pdf"},
				map[string]any{"id": "c", "x": 1.5, "y": -2},
			},
			"edges": []any{
				map[string]any{"source": "a", "target": "b", "weight": 0.9},
				map[string]any{"source": "b", "target": "a", "weight": 0.9},
				map[string]any{"source": "b", "target": "c", "weight": 0.4},
				map[string]any{"source": "c", "target": "z", "weight": 0.1},
			},
		})
		return mcp.NewToolResultStructured(json.RawMessage(b), string(b)), nil
	})
	ts := httptest.NewServer(server.NewStreamableHTTPServer(s))
	t.Cleanup(ts.Close)
	return ts
}

func TestGraph_PluginSource(t *testing.T) {
	f := graphFixture(t)
	var calls atomic.Int32
	ts := mapPlugin(t, &calls)
	reg, err := plugins.New([]plugins.Config{{Name: "vec", URL: ts.URL + "/mcp"}}, t.TempDir(), nil, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	reg.Check(ctx)
	st, _ := reg.State("vec")
	if _, err := reg.Accept(ctx, "vec", st.SHA, "test"); err != nil {
		t.Fatal(err)
	}
	f.router.deps.Plugins = reg
	get := func(q string) *recorder {
		return f.doAuthRecorder(http.MethodGet, "/api/v1/graph"+q, "", nil)
	}
	with := url.QueryEscape(`{"collection":"procedure"}`)

	// Not exposed in rc yet: neither listed nor served.
	if w := f.doAuthRecorder(http.MethodGet, "/api/v1/graph/sources?project=rc", "", nil); w.code != http.StatusOK || strings.Contains(w.body, "vec/chunks") {
		t.Fatalf("sources before expose: %d %s", w.code, w.body)
	}
	if w := get("?source=vec/chunks&project=rc&with=" + with); w.code != http.StatusNotFound || !strings.Contains(w.body, "not exposed") {
		t.Fatalf("before expose: %d %s", w.code, w.body)
	}
	if _, err := reg.Expose("vec", "rc", "test"); err != nil {
		t.Fatal(err)
	}
	w := f.doAuthRecorder(http.MethodGet, "/api/v1/graph/sources?project=rc", "", nil)
	if w.code != http.StatusOK || !strings.Contains(w.body, `"source":"links"`) || !strings.Contains(w.body, `"source":"vec/chunks"`) || !strings.Contains(w.body, `"collection"`) {
		t.Fatalf("sources: %d %s", w.code, w.body)
	}

	w = get("?source=vec/chunks&project=rc&with=" + with)
	if w.code != http.StatusOK {
		t.Fatalf("graph: %d %s", w.code, w.body)
	}
	var resp pluginGraphResponse
	if err := json.Unmarshal([]byte(w.body), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Stats.NodeCount != 3 || resp.Stats.EdgeCount != 2 || resp.Source != "vec/chunks" || resp.Cached {
		t.Fatalf("graph: %+v", resp)
	}
	byID := map[string]pluginGraphNode{}
	for _, n := range resp.Nodes {
		byID[n.ID] = n
	}
	// The project comes from gosidian, the arguments from the view; a
	// link to no note is dropped; edges twice or to nowhere are dropped.
	if a := byID["a"]; a.Group != "rc" || a.Link != "rc/a.md" || a.Degree != 1 || a.Text != "first" {
		t.Errorf("a: %+v", a)
	}
	if b := byID["b"]; b.Group != "procedure" || b.Link != "" || b.Label != "b" || b.Degree != 2 {
		t.Errorf("b: %+v", b)
	}
	if c := byID["c"]; c.X == nil || *c.X != 1.5 || c.Z != nil {
		t.Errorf("c: %+v", c)
	}
	if resp.Edges[0].Weight != 0.9 || resp.Edges[0].Count != 1 {
		t.Errorf("edges: %+v", resp.Edges)
	}

	// Kept a while; refresh asks the plugin again.
	if w := get("?source=vec/chunks&project=rc&with=" + with); !strings.Contains(w.body, `"cached":true`) || calls.Load() != 1 {
		t.Errorf("cache: %d calls, %s", calls.Load(), w.body)
	}
	if get("?source=vec/chunks&project=rc&refresh=1&with=" + with); calls.Load() != 2 {
		t.Errorf("refresh: %d calls", calls.Load())
	}
	for q, code := range map[string]int{
		"?source=vec/chunks":                              http.StatusBadRequest,
		"?source=vec/chunks&project=rc&with=%5B1%5D":      http.StatusBadRequest,
		"?source=Vec&project=rc":                          http.StatusBadRequest,
		"?source=vec/maps&project=rc":                     http.StatusBadGateway,
		"?source=vec/chunks&project=dockers&with=" + with: http.StatusNotFound,
	} {
		if w := get(q); w.code != code {
			t.Errorf("%s: %d, want %d (%s)", q, w.code, code, w.body)
		}
	}
	// The links stay the default.
	if w := get("?source=links"); w.code != http.StatusOK || !strings.Contains(w.body, `"node_count":5`) {
		t.Errorf("links: %d %s", w.code, w.body)
	}
}
