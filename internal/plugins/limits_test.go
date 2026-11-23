package plugins

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"slices"
	"strings"
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

// workersPlugin serves the manifest of an agent with two workers: its node
// and tool run take two calls at once of their own, its pr and its usage
// none, and the plugin has no limit.
func workersPlugin(t *testing.T) *httptest.Server {
	t.Helper()
	s := server.NewMCPServer("pi", "1.0.0", server.WithToolCapabilities(false))
	s.AddTool(mcp.NewTool(ManifestTool), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		obj := map[string]any{"type": "object"}
		b, _ := json.Marshal(map[string]any{
			"name": "pi", "version": "1.0", "gosidian": 1,
			"nodes": []any{
				map[string]any{"name": "run", "tool": "run", "max_concurrent": 2, "input_schema": obj},
				map[string]any{"name": "pr", "tool": "pr", "input_schema": obj},
			},
			"tools": []any{
				map[string]any{"name": "run", "access": "write", "max_concurrent": 2, "input_schema": obj},
				map[string]any{"name": "usage", "access": "read", "input_schema": obj},
			},
			"permissions": map[string]any{"network": []string{}},
		})
		return mcp.NewToolResultText(string(b)), nil
	})
	ts := httptest.NewServer(server.NewStreamableHTTPServer(s))
	t.Cleanup(ts.Close)
	return ts
}

// TestRegistry_ToolLimits: a node's or a tool's max_concurrent is a place
// of its MCP tool's own, plugin:<name>/<tool>, beside the plugin's: an
// agent's run waits for one of its workers, its pr and its usage do not.
func TestRegistry_ToolLimits(t *testing.T) {
	ts := workersPlugin(t)
	ctx := context.Background()
	reg, err := New([]Config{{Name: "pi", URL: ts.URL + "/mcp"}}, t.TempDir(), nil, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	reg.Check(ctx)
	if got := reg.Resources("pi.run"); !slices.Equal(got, []string{"plugin:pi"}) {
		t.Errorf("before the accept: %v", got)
	}
	st, _ := reg.State("pi")
	if _, err := reg.Accept(ctx, "pi", st.SHA, "test"); err != nil {
		t.Fatal(err)
	}
	if got := reg.Resources("pi.run"); !slices.Equal(got, []string{"plugin:pi", "plugin:pi/run"}) {
		t.Errorf("the run's resources %v", got)
	}
	if got := reg.Resources("pi.pr"); !slices.Equal(got, []string{"plugin:pi"}) {
		t.Errorf("the pr's resources %v", got)
	}
	if got := reg.ToolResources("pi", "run"); !slices.Equal(got, []string{"plugin:pi", "plugin:pi/run"}) {
		t.Errorf("the run tool's resources %v", got)
	}
	if got := reg.ToolResources("pi", "usage"); !slices.Equal(got, []string{"plugin:pi"}) {
		t.Errorf("the usage tool's resources %v", got)
	}
	if reg.ToolMaxConcurrent("pi", "run") != 2 || reg.ToolMaxConcurrent("pi", "pr") != 0 || reg.MaxConcurrent("pi") != 0 {
		t.Errorf("limits: run %d, pr %d, plugin %d", reg.ToolMaxConcurrent("pi", "run"), reg.ToolMaxConcurrent("pi", "pr"), reg.MaxConcurrent("pi"))
	}
	if reg.ToolResources("other", "run") != nil || reg.ToolMaxConcurrent("other", "run") != 0 {
		t.Error("a plugin that is not there has a tool's place")
	}
}

// TestManifest_ToolLimit: the smallest max_concurrent of the nodes and
// the tool that call an MCP tool; a negative one is refused.
func TestManifest_ToolLimit(t *testing.T) {
	m := Manifest{
		Nodes: []NodeSpec{{Name: "run", Tool: "run", MaxConcurrent: 3}, {Name: "fast", Tool: "run", MaxConcurrent: 2}, {Name: "pr", Tool: "pr"}},
		Tools: []ToolSpec{{Name: "run", MaxConcurrent: 4}},
	}
	if m.ToolLimit("run") != 2 || m.ToolLimit("pr") != 0 || m.ToolLimit("nope") != 0 {
		t.Errorf("run %d, pr %d, nope %d", m.ToolLimit("run"), m.ToolLimit("pr"), m.ToolLimit("nope"))
	}
	for name, raw := range map[string]string{
		"node": `{"name":"pi","version":"1","gosidian":1,"nodes":[{"name":"run","tool":"run","max_concurrent":-1}],"permissions":{"network":[]}}`,
		"tool": `{"name":"pi","version":"1","gosidian":1,"tools":[{"name":"run","access":"read","max_concurrent":-1}],"permissions":{"network":[]}}`,
	} {
		if _, err := ParseManifest([]byte(raw), "pi"); err == nil || !strings.Contains(err.Error(), "max_concurrent") {
			t.Errorf("a %s's negative max_concurrent: %v", name, err)
		}
	}
}

// TestStoreFiles_Text: a text file of a type attachments do not take
// comes back as its text, within the text cap, without being stored; a
// stored file and a binary one go to the store as before.
func TestStoreFiles_Text(t *testing.T) {
	reg, err := New(nil, t.TempDir(), nil, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	reg.SetLimits(Limits{TextBytes: 15})
	blob := func(name, mime string, data []byte) mcp.Content {
		return mcp.NewEmbeddedResource(mcp.BlobResourceContents{URI: "file:///" + name, MIMEType: mime, Blob: base64.StdEncoding.EncodeToString(data)})
	}
	var stored []string
	files, skipped := reg.StoreFiles([]mcp.Content{
		blob("scheda.md", "text/markdown", []byte("# Scheda\n")),
		blob("notes.txt", "text/plain", []byte("stored")),
		blob("page.html", "", []byte("<p>a</p>")),
		blob("out.yaml", "", []byte("a: 1\n")),
		blob("core", "", []byte{'E', 0, 'F'}),
	}, func(name string, data []byte) (string, error) {
		if !strings.HasSuffix(name, ".txt") {
			return "", errors.New("unsupported file type")
		}
		stored = append(stored, name)
		return "p/attachments/" + name, nil
	})
	if len(files) != 3 || len(skipped) != 2 || !slices.Equal(stored, []string{"notes.txt"}) {
		t.Fatalf("files %v, skipped %v, stored %v", files, skipped, stored)
	}
	if f := files[0].(map[string]any); f["name"] != "scheda.md" || f["text"] != "# Scheda\n" || f["mime"] != "text/markdown" || f["path"] != nil {
		t.Errorf("the markdown = %v", f)
	}
	if f := files[1].(map[string]any); f["name"] != "notes.txt" || f["path"] != "p/attachments/notes.txt" || f["text"] != nil {
		t.Errorf("the stored one = %v", f)
	}
	if f := files[2].(map[string]any); f["name"] != "out.yaml" || f["text"] != "a: 1\n" || f["mime"] != "text/plain" {
		t.Errorf("the yaml = %v", f)
	}
	// page.html would take the text past 15 bytes, out.yaml fits after
	// it; core is no text.
	if s := skipped[0].(map[string]any); s["name"] != "page.html" || !strings.Contains(s["error"].(string), "max_output_bytes") {
		t.Errorf("first skipped = %v", s)
	}
	if s := skipped[1].(map[string]any); s["name"] != "core" || s["error"] != "unsupported file type" {
		t.Errorf("second skipped = %v", s)
	}
}
