package plugins

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// limitPlugin serves a manifest that suggests one call at once.
func limitPlugin(t *testing.T) *httptest.Server {
	t.Helper()
	s := server.NewMCPServer("ocr", "1.0.0", server.WithToolCapabilities(false))
	s.AddTool(mcp.NewTool(ManifestTool), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		b, _ := json.Marshal(map[string]any{
			"name": "ocr", "version": "1.0", "gosidian": 1, "max_concurrent": 1,
			"nodes":       []any{map[string]any{"name": "run", "tool": "run", "description": "OCR", "input_schema": map[string]any{"type": "object"}}},
			"permissions": map[string]any{"network": []string{}},
		})
		return mcp.NewToolResultText(string(b)), nil
	})
	ts := httptest.NewServer(server.NewStreamableHTTPServer(s))
	t.Cleanup(ts.Close)
	return ts
}

// TestRegistry_Limits: a plugin's calls at once are its config's, else
// its accepted manifest's suggestion (IMP-196); its nodes take the plugin
// and its shared resources.
func TestRegistry_Limits(t *testing.T) {
	ts := limitPlugin(t)
	ctx := context.Background()
	reg, err := New([]Config{{Name: "ocr", URL: ts.URL + "/mcp", Resources: []string{"gpu"}}}, t.TempDir(), nil, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	reg.Check(ctx)
	if n := reg.MaxConcurrent("ocr"); n != 0 {
		t.Errorf("before the accept: %d", n)
	}
	st, _ := reg.State("ocr")
	if _, err := reg.Accept(ctx, "ocr", st.SHA, "test"); err != nil {
		t.Fatal(err)
	}
	if n := reg.MaxConcurrent("ocr"); n != 1 {
		t.Errorf("the manifest's suggestion: %d", n)
	}
	if got := reg.Resources("ocr.run"); !slices.Equal(got, []string{"plugin:ocr", "res:gpu"}) {
		t.Errorf("resources %v", got)
	}
	if reg.Resources("other.run") != nil || reg.Resources("starlark") != nil || reg.MaxConcurrent("other") != 0 {
		t.Error("a plugin that is not there has resources")
	}

	cfg, err := New([]Config{{Name: "ocr", URL: ts.URL + "/mcp", MaxConcurrent: 3}}, t.TempDir(), nil, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Check(ctx)
	st, _ = cfg.State("ocr")
	if _, err := cfg.Accept(ctx, "ocr", st.SHA, "test"); err != nil {
		t.Fatal(err)
	}
	if n := cfg.MaxConcurrent("ocr"); n != 3 {
		t.Errorf("the config's limit: %d", n)
	}
}
