package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/gosidian/gosidian/internal/plugins"
)

// appPlugin serves a manifest with one app, the editor of the notes
// type: layout (IMP-226).
func appPlugin(t *testing.T) *httptest.Server {
	t.Helper()
	s := server.NewMCPServer("layout", "0.1.0", server.WithToolCapabilities(false))
	s.AddTool(mcp.NewTool(plugins.ManifestTool), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		b, _ := json.Marshal(map[string]any{
			"name": "layout", "version": "0.1.0", "gosidian": 1,
			"apps": []any{map[string]any{"name": "layout", "title": "Impaginazione", "url": "http://127.0.0.1:8097/app/",
				"alt_urls": []string{"http://127.0.0.1:8097/app/"}, "note_type": "layout", "bridge": "layout/1", "engine": "designcraft v0.17.0"}},
			"permissions": map[string]any{"files": false, "network": []string{}},
		})
		return mcp.NewToolResultText(string(b)), nil
	})
	ts := httptest.NewServer(server.NewStreamableHTTPServer(s))
	t.Cleanup(ts.Close)
	return ts
}

// GET /api/v1/plugins/apps lists the apps of the accepted manifests to
// every signed-in account, for the windows that open them.
func TestPluginAppsREST(t *testing.T) {
	f := newPipelineFixture(t)
	if w := f.doAuthRecorder(http.MethodGet, "/api/v1/plugins/apps", "", nil); w.code != http.StatusOK || !strings.Contains(w.body, `"apps":[]`) {
		t.Fatalf("no plugins: %d %s", w.code, w.body)
	}
	ts := appPlugin(t)
	reg, err := plugins.New([]plugins.Config{{Name: "layout", URL: ts.URL + "/mcp"}}, t.TempDir(), nil, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	reg.Check(ctx)
	f.router.deps.Plugins = reg
	if w := f.doAuthRecorder(http.MethodGet, "/api/v1/plugins/apps", "", nil); w.code != http.StatusOK || !strings.Contains(w.body, `"apps":[]`) {
		t.Fatalf("a manifest not accepted shows its app: %d %s", w.code, w.body)
	}
	st, _ := reg.State("layout")
	if _, err := reg.Accept(ctx, "layout", st.SHA, "test"); err != nil {
		t.Fatal(err)
	}
	m := f.req(t, http.MethodGet, "/api/v1/plugins/apps", "", f.memberToken(t))
	var got struct {
		Apps []struct {
			Plugin, Name, Title, URL, Origin, NoteType, Bridge, Engine, Status string
		}
	}
	_ = json.Unmarshal([]byte(m.body), &got)
	if m.code != http.StatusOK || len(got.Apps) != 1 {
		t.Fatalf("member: %d %s", m.code, m.body)
	}
	for _, want := range []string{`"plugin":"layout"`, `"name":"layout"`, `"url":"http://127.0.0.1:8097/app/"`, `"origin":"http://127.0.0.1:8097"`,
		`"alt_urls":["http://127.0.0.1:8097/app/"]`, `"note_type":"layout"`, `"bridge":"layout/1"`, `"engine":"designcraft v0.17.0"`, `"status":"ok"`} {
		if !strings.Contains(m.body, want) {
			t.Errorf("member: %s lacks %s", m.body, want)
		}
	}
	if w := f.doAuthRecorder(http.MethodPost, "/api/v1/plugins/apps", "{}", nil); w.code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", w.code)
	}
	if w := f.req(t, http.MethodGet, "/api/v1/plugins/apps", "", ""); w.code != http.StatusUnauthorized {
		t.Errorf("without a token: %d", w.code)
	}
}
