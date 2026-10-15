package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"path"
	"path/filepath"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/plugins"
)

// vecPlugin is a plugin with a read tool (search, which answers with the
// arguments and _meta it got), a write tool (put) and one whose result is
// too large (dump).
func vecPlugin(t *testing.T) *httptest.Server {
	t.Helper()
	s := server.NewMCPServer("vec", "1.0.0", server.WithToolCapabilities(false))
	s.AddTool(mcplib.NewTool(plugins.ManifestTool), func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		b, _ := json.Marshal(map[string]any{
			"name": "vec", "version": "1.0", "gosidian": 1,
			"tools": []any{
				map[string]any{"name": "search", "access": "read", "description": "Chunks similar to a text",
					"input_schema": map[string]any{"type": "object", "required": []string{"query"},
						"properties": map[string]any{"query": map[string]any{"type": "string"}}}},
				map[string]any{"name": "put", "access": "write"},
				map[string]any{"name": "dump", "access": "read"},
			},
			"permissions": map[string]any{"files": false, "network": []string{}},
		})
		return mcplib.NewToolResultText(string(b)), nil
	})
	echo := func(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		meta := map[string]any{}
		if req.Params.Meta != nil {
			meta = req.Params.Meta.AdditionalFields
		}
		return mcplib.NewToolResultStructured(map[string]any{"args": req.GetArguments(), "meta": meta}, "echo"), nil
	}
	s.AddTool(mcplib.NewTool("search"), echo)
	s.AddTool(mcplib.NewTool("put"), echo)
	s.AddTool(mcplib.NewTool("dump"), func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		return mcplib.NewToolResultText(strings.Repeat("x", defaultMaxPluginResult+1)), nil
	})
	ts := httptest.NewServer(server.NewStreamableHTTPServer(s))
	t.Cleanup(ts.Close)
	return ts
}

// vecServer is a test server with the plugin vec accepted, exposed in p.
func vecServer(t *testing.T) (*Server, *plugins.Registry) {
	t.Helper()
	s, _, _ := newTestServer(t)
	ts := vecPlugin(t)
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
	s.SetPlugins(reg)
	return s, reg
}

func has(names map[string]struct{}, name string) bool {
	_, ok := names[name]
	return ok
}

func TestPluginTools_OnlyWhereExposed(t *testing.T) {
	s, reg := vecServer(t)
	admin := context.Background()
	if names := listToolNames(t, s, admin); has(names, "vec_search") {
		t.Fatal("a tool of a plugin exposed nowhere is listed")
	}
	if _, err := reg.Expose("vec", "p", "test"); err != nil {
		t.Fatal(err)
	}
	reader := &auth.Token{ID: "r1", Name: "reader", Project: "p", Scopes: []string{auth.ScopeRead}}
	writer := &auth.Token{ID: "w1", Name: "writer", Project: "p", Scopes: []string{auth.ScopeRead, auth.ScopeWrite}}
	worker := &auth.Token{ID: "c1", Name: "worker", Project: "p", Scopes: []string{auth.ScopeRead, auth.ScopeWrite}, ToolProfile: auth.ToolProfileCore}
	other := &auth.Token{ID: "o1", Name: "other", Project: "q", Scopes: []string{auth.ScopeRead, auth.ScopeWrite}}

	for _, c := range []struct {
		tok         *auth.Token
		search, put bool
	}{
		{reader, true, false},
		{writer, true, true},
		{worker, true, true},
		{other, false, false},
	} {
		names := listToolNames(t, s, ctxWithToken(c.tok))
		if has(names, "vec_search") != c.search || has(names, "vec_put") != c.put {
			t.Errorf("%s sees search %v, put %v (want %v, %v)", c.tok.Name, has(names, "vec_search"), has(names, "vec_put"), c.search, c.put)
		}
	}
	if names := listToolNames(t, s, ctxWithToken(worker)); has(names, "memory_audit_tail") {
		t.Error("the core profile lets a full tool through with the plugin's")
	}

	// The agent's project goes to the plugin in _meta, not in the arguments.
	out := rpcMessage(t, s, ctxWithToken(reader), "tools/call", `{"name":"vec_search","arguments":{"query":"pulsante fine bobina"}}`)
	var resp struct {
		Result struct {
			IsError           bool `json:"isError"`
			StructuredContent struct {
				Args map[string]any `json:"args"`
				Meta map[string]any `json:"meta"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil || resp.Result.IsError {
		t.Fatalf("call: %v %s", err, out)
	}
	got := resp.Result.StructuredContent
	if got.Args["query"] != "pulsante fine bobina" || got.Args["project"] != nil ||
		got.Meta[plugins.MetaProject] != "p" || got.Meta[plugins.MetaCaller] != "r1" || got.Meta[plugins.MetaAccess] != "read" {
		t.Errorf("the plugin got %+v", got)
	}

	// Filtered out at call time too, and refused for a project it is not
	// exposed in.
	if out := rpcMessage(t, s, ctxWithToken(other), "tools/call", `{"name":"vec_search","arguments":{"query":"x"}}`); !strings.Contains(out, "not found") {
		t.Errorf("a token of another project called the tool: %s", out)
	}
	if out := rpcMessage(t, s, ctxWithToken(reader), "tools/call", `{"name":"vec_put","arguments":{}}`); !strings.Contains(out, "not found") {
		t.Errorf("a read-only token called a write tool: %s", out)
	}
	if out := rpcMessage(t, s, admin, "tools/call", `{"name":"vec_search","arguments":{"query":"x","project":"q"}}`); !strings.Contains(out, "not exposed in project") {
		t.Errorf("a call for a project the plugin is not exposed in: %s", out)
	}
	if out := rpcMessage(t, s, ctxWithToken(writer), "tools/call", `{"name":"vec_put","arguments":{"id":"a"}}`); strings.Contains(out, "isError\":true") || !strings.Contains(out, `"gosidian/access":"write"`) {
		t.Errorf("the write tool for a writer: %s", out)
	}
	if out := rpcMessage(t, s, ctxWithToken(reader), "tools/call", `{"name":"vec_dump","arguments":{}}`); !strings.Contains(out, "more than") {
		t.Errorf("a result over the cap came back: %.200s", out)
	}
	// The installer may raise the cap: the same result then passes.
	s.SetPluginResultLimit(4 << 20)
	if out := rpcMessage(t, s, ctxWithToken(reader), "tools/call", `{"name":"vec_dump","arguments":{}}`); strings.Contains(out, "more than") {
		t.Errorf("a result under the raised cap was refused: %.200s", out)
	}

	// Unexposed, the tools go away; forgotten, they are no tools at all.
	if err := reg.Unexpose("vec", "p"); err != nil {
		t.Fatal(err)
	}
	if names := listToolNames(t, s, ctxWithToken(writer)); has(names, "vec_search") {
		t.Error("an unexposed plugin's tool is listed")
	}
	if err := reg.Forget("vec"); err != nil {
		t.Fatal(err)
	}
	listToolNames(t, s, admin)
	if s.impl.GetTool("vec_search") != nil {
		t.Error("a forgotten manifest's tool is still registered")
	}
}

func TestPluginTools_Schema(t *testing.T) {
	s, reg := vecServer(t)
	if _, err := reg.Expose("vec", "p", "test"); err != nil {
		t.Fatal(err)
	}
	out := rpcMessage(t, s, context.Background(), "tools/list", `{}`)
	var resp struct {
		Result struct {
			Tools []struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				InputSchema json.RawMessage `json:"inputSchema"`
				Annotations struct {
					ReadOnly *bool `json:"readOnlyHint"`
				} `json:"annotations"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, tl := range resp.Result.Tools {
		switch tl.Name {
		case "vec_search":
			found++
			var schema struct {
				Required   []string                   `json:"required"`
				Properties map[string]json.RawMessage `json:"properties"`
			}
			_ = json.Unmarshal(tl.InputSchema, &schema)
			if schema.Properties["query"] == nil || schema.Properties["project"] == nil || strings.Join(schema.Required, ",") != "query" {
				t.Errorf("vec_search's schema: %s", tl.InputSchema)
			}
			if !strings.HasPrefix(tl.Description, "[plugin vec] Chunks similar to a text.") || tl.Annotations.ReadOnly == nil || !*tl.Annotations.ReadOnly {
				t.Errorf("vec_search: %q, read-only %v", tl.Description, tl.Annotations.ReadOnly)
			}
		case "vec_put":
			found++
			if tl.Annotations.ReadOnly == nil || *tl.Annotations.ReadOnly || !strings.Contains(tl.Description, "needs write access") {
				t.Errorf("vec_put: %q", tl.Description)
			}
		}
	}
	if found != 2 {
		t.Errorf("found %d of the plugin's tools", found)
	}
}

// imgPlugin is a plugin whose write tool convert takes files by reference
// (IMP-184): it reads each file through its ticket while the call lasts,
// with open, and gives back one file per input, named <stem>.webp.
func imgPlugin(t *testing.T, open func(ticket string) error) *httptest.Server {
	t.Helper()
	s := server.NewMCPServer("img", "1.0.0", server.WithToolCapabilities(false))
	s.AddTool(mcplib.NewTool(plugins.ManifestTool), func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		b, _ := json.Marshal(map[string]any{
			"name": "img", "version": "1.0", "gosidian": 1,
			"tools": []any{map[string]any{"name": "convert", "access": "write", "files": []string{"files"}, "file_refs": true,
				"input_schema": map[string]any{"type": "object", "properties": map[string]any{"files": map[string]any{}}}}},
			"permissions": map[string]any{"files": true, "network": []string{}},
		})
		return mcplib.NewToolResultText(string(b)), nil
	})
	s.AddTool(mcplib.NewTool("convert"), func(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		var refs []map[string]any
		raw, _ := json.Marshal(req.GetArguments()["files"])
		if json.Unmarshal(raw, &refs) != nil {
			var one map[string]any
			_ = json.Unmarshal(raw, &one)
			refs = []map[string]any{one}
		}
		res := mcplib.NewToolResultStructured(map[string]any{"refs": refs, "note": "![x](a.webp)"}, "converted")
		for _, r := range refs {
			url, _ := r["url"].(string)
			if err := open(url[strings.LastIndex(url, "/")+1:]); err != nil {
				return mcplib.NewToolResultError("the ticket: " + err.Error()), nil
			}
			name := strings.TrimSuffix(r["name"].(string), ".png") + ".webp"
			res.Content = append(res.Content, mcplib.NewEmbeddedResource(mcplib.BlobResourceContents{
				URI: "file:///" + name, MIMEType: "image/webp", Blob: base64.StdEncoding.EncodeToString([]byte("RIFF\x00\x00\x00\x00WEBPVP8L" + name))}))
		}
		return res, nil
	})
	ts := httptest.NewServer(server.NewStreamableHTTPServer(s))
	t.Cleanup(ts.Close)
	return ts
}

// A plugin's tool that takes files (IMP-184): the agent names vault paths
// of the project, the plugin gets tickets that work while the call lasts,
// and the files it gives back are attachments of the project, which the
// agent gets as paths, not bytes.
func TestPluginTools_Files(t *testing.T) {
	s, e := pipelineServer(t)
	al, err := audit.Open(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	s.SetAuditLog(al)
	var tickets []string
	ts := imgPlugin(t, func(ticket string) error {
		tickets = append(tickets, ticket)
		f, _, _, err := e.OpenTicketFile(ticket)
		if err == nil {
			f.Close()
		}
		return err
	})
	reg, err := plugins.New([]plugins.Config{{Name: "img", URL: ts.URL + "/mcp"}}, t.TempDir(), nil, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	reg.Check(ctx)
	st, _ := reg.State("img")
	if st.Error != "" {
		t.Fatalf("the manifest: %s", st.Error)
	}
	if _, err := reg.Accept(ctx, "img", st.SHA, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Expose("img", "p", "test"); err != nil {
		t.Fatal(err)
	}
	s.SetPlugins(reg)
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N\x00\x00\x00\x00IEND\xaeB`\x82")
	a, err := attach.Store(s.vault, png, "fig.png", "p")
	if err != nil {
		t.Fatal(err)
	}
	writer := &auth.Token{ID: "w1", Name: "writer", Project: "p", Scopes: []string{auth.ScopeRead, auth.ScopeWrite}}

	out := rpcMessage(t, s, ctxWithToken(writer), "tools/call", `{"name":"img_convert","arguments":{"files":["`+a.Path+`"]}}`)
	var resp struct {
		Result struct {
			IsError           bool              `json:"isError"`
			Content           []json.RawMessage `json:"content"`
			StructuredContent struct {
				Refs  []map[string]any `json:"refs"`
				Note  string           `json:"note"`
				Files []map[string]any `json:"files"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil || resp.Result.IsError {
		t.Fatalf("call: %v %s", err, out)
	}
	got := resp.Result.StructuredContent
	if len(got.Refs) != 1 || got.Refs[0]["name"] != path.Base(a.Path) || got.Refs[0]["size"] != float64(len(png)) || got.Refs[0]["sha256"] == "" {
		t.Errorf("the plugin got %+v", got.Refs)
	}
	if len(got.Files) != 1 || !strings.HasPrefix(got.Files[0]["path"].(string), "p/attachments/") || !strings.HasSuffix(got.Files[0]["path"].(string), ".webp") {
		t.Fatalf("files %+v in %.600s", got.Files, out)
	}
	if b, err := s.vault.ReadAttachment(got.Files[0]["path"].(string), 1<<20); err != nil || !strings.HasPrefix(string(b), "RIFF") {
		t.Errorf("the file given back: %v", err)
	}
	if strings.Contains(out, "base64") || strings.Contains(out, `"blob"`) || len(resp.Result.Content) != 1 {
		t.Errorf("the bytes came back to the agent: %.300s", out)
	}
	if _, _, _, err := e.OpenTicketFile(tickets[0]); err == nil {
		t.Error("the ticket works after the call")
	}
	stored := false
	for _, en := range auditEntries(t, al) {
		if en.Action == audit.ActionUploadAttachment && en.Path == got.Files[0]["path"] {
			stored = true
		}
	}
	if !stored {
		t.Error("no audit entry for the file stored")
	}

	// Another project's path is read inside the call's project, where it
	// is not; the error does not say where the server keeps the vault.
	for args, want := range map[string]string{
		`{"files":["q/attachments/x.png"]}`:     `no file \"q/attachments/x.png\" in project \"p\"`,
		`{"files":"p/attachments/nothere.png"}`: "no file",
		`{"files":["../q/a.png"]}`:              "plain path inside the project",
		`{"files":[]}`:                          "from 1 to",
	} {
		out := rpcMessage(t, s, ctxWithToken(writer), "tools/call", `{"name":"img_convert","arguments":`+args+`}`)
		if !strings.Contains(out, `"isError":true`) || !strings.Contains(out, want) || strings.Contains(out, "/tmp/") {
			t.Errorf("%s: %.300s", args, out)
		}
	}
}

// A tool may take files only by reference, with permissions.files; a
// node's tool that takes files is a tool only when it lists them.
func TestPluginManifest_ToolFiles(t *testing.T) {
	base := func(tool map[string]any, files bool, nodes ...any) []byte {
		b, _ := json.Marshal(map[string]any{"name": "img", "version": "1", "gosidian": 1, "tools": []any{tool}, "nodes": nodes,
			"permissions": map[string]any{"files": files, "network": []string{}}})
		return b
	}
	node := map[string]any{"name": "convert", "tool": "convert", "files": []string{"files"}, "file_refs": true}
	for name, c := range map[string]struct {
		raw  []byte
		want string
	}{
		"ok":            {base(map[string]any{"name": "convert", "access": "write", "files": []string{"files"}, "file_refs": true}, true, node), ""},
		"no file_refs":  {base(map[string]any{"name": "convert", "access": "write", "files": []string{"files"}}, true), "by reference"},
		"no permission": {base(map[string]any{"name": "convert", "access": "write", "files": []string{"files"}, "file_refs": true}, false), "permissions.files"},
		"node's files":  {base(map[string]any{"name": "convert", "access": "write"}, true, node), "cannot hand it"},
	} {
		_, err := plugins.ParseManifest(c.raw, "img")
		if (err == nil) != (c.want == "") || err != nil && !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", name, err, c.want)
		}
	}
}
