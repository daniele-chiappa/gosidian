package mcp

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/auth"
)

const initializeBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"streamable-test","version":"0"}}}`

// postMCP issues one Streamable HTTP request against h the way a client
// does: JSON-RPC body, both accepted media types, optional bearer and
// session id.
func postMCP(t *testing.T, h http.Handler, token, session, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func rpcToolCall(id int, tool string, args map[string]any) string {
	b, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": args},
	})
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestStreamable_InitializeAndToolsList(t *testing.T) {
	s, token := serverWithToken(t, "", []string{auth.ScopeRead})
	h := s.Handler("/mcp")

	rec := postMCP(t, h, token, "", initializeBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("initialize: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json (no SSE upgrade without notifications)", ct)
	}
	session := rec.Header().Get("Mcp-Session-Id")
	if session == "" {
		t.Fatal("initialize response carries no Mcp-Session-Id")
	}
	if rec.Header().Get("X-Accel-Buffering") != "no" {
		t.Error("X-Accel-Buffering: no missing on the transport response")
	}
	if !strings.Contains(rec.Body.String(), `"serverInfo"`) {
		t.Errorf("initialize result lacks serverInfo: %s", rec.Body.String())
	}

	rec = postMCP(t, h, token, session, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("tools/list: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"memory_bootstrap"`) {
		t.Errorf("tools/list does not expose memory_bootstrap: %s", rec.Body.String()[:200])
	}
}

func TestStreamable_ToolCallHonoursTokenScope(t *testing.T) {
	s, token := serverWithToken(t, "proj", []string{auth.ScopeRead, auth.ScopeWrite})
	h := s.Handler("/mcp")

	// A client always initialises first and echoes the session id it got
	// back; a non-initialize message without one is refused (404).
	rec := postMCP(t, h, token, "", initializeBody)
	session := rec.Header().Get("Mcp-Session-Id")
	if rec.Code != http.StatusOK || session == "" {
		t.Fatalf("initialize: status = %d, session = %q", rec.Code, session)
	}
	if rec := postMCP(t, h, token, "", rpcToolCall(0, "memory_list_projects", map[string]any{})); rec.Code != http.StatusNotFound {
		t.Errorf("tools/call without a session id: status = %d, want 404", rec.Code)
	}

	rec = postMCP(t, h, token, session, rpcToolCall(1, "memory_create", map[string]any{"path": "proj/a.md", "content": "# a\n"}))
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"isError":true`) {
		t.Fatalf("in-scope create: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if _, err := s.vault.Load("proj/a.md"); err != nil {
		t.Fatalf("note not written through the streamable transport: %v", err)
	}

	rec = postMCP(t, h, token, session, rpcToolCall(2, "memory_create", map[string]any{"path": "other/b.md", "content": "# b\n"}))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"isError":true`) {
		t.Fatalf("out-of-scope create should fail as a tool error: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if _, err := s.vault.Load("other/b.md"); err == nil {
		t.Error("out-of-scope note was written")
	}
}

func TestStreamable_RequiresBearer(t *testing.T) {
	s, _ := serverWithToken(t, "", []string{auth.ScopeRead})
	h := s.Handler("/mcp")

	for _, token := range []string{"", "gosidian_not-a-token"} {
		rec := postMCP(t, h, token, "", initializeBody)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("token %q: status = %d, want 401", token, rec.Code)
		}
		if rec.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("token %q: WWW-Authenticate missing", token)
		}
	}
}

// gosidian never pushes server-initiated messages, so the standalone GET
// stream is refused with 405 (allowed by the spec) rather than holding a
// connection open through a reverse proxy.
func TestStreamable_GetStreamRefused(t *testing.T) {
	s, token := serverWithToken(t, "", []string{auth.ScopeRead})
	h := s.Handler("/mcp")

	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /mcp: status = %d, want 405", rec.Code)
	}
}

// The legacy transport keeps working on the same mount: the SSE handshake
// still announces the /mcp/message endpoint, and the stream now opts out of
// proxy buffering.
func TestStreamable_SSEStillServed(t *testing.T) {
	s, token := serverWithToken(t, "", []string{auth.ScopeRead})
	h := s.Handler("/mcp")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/mcp/sse", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req) // returns once ctx expires
	body := rec.Body.String()
	if !strings.Contains(body, "event: endpoint") || !strings.Contains(body, "/mcp/message?sessionId=") {
		t.Fatalf("SSE handshake broken: %q", body)
	}
	if rec.Header().Get("X-Accel-Buffering") != "no" {
		t.Error("X-Accel-Buffering: no missing on the SSE stream")
	}
}

// Streamable HTTP lives on the exact prefix only; the subtree and the legacy
// root listener know nothing of it.
func TestStreamable_ExactPathOnly(t *testing.T) {
	s, token := serverWithToken(t, "", []string{auth.ScopeRead})

	for _, tc := range []struct{ base, path string }{
		{"/mcp", "/mcp/"},
		{"", "/"},
	} {
		h := s.Handler(tc.base)
		req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(initializeBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK {
			t.Errorf("Handler(%q): POST %s answered 200, the streamable endpoint leaked onto the subtree", tc.base, tc.path)
		}
	}
}

// mcp-go rejects a request that arrived on a loopback-bound connection with
// a non-localhost Host header (DNS-rebinding guard) on both transports.
// Docker and LAN listeners never trip it; a same-host reverse proxy does,
// hence one switch for both endpoints.
func TestStreamable_DNSRebindingGuard(t *testing.T) {
	s, token := serverWithToken(t, "", []string{auth.ScopeRead})
	send := func(h http.Handler, method, path string, local net.Addr) int {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		req := httptest.NewRequest(method, path, strings.NewReader(initializeBody)).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Host = "notes.example.com"
		req = req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, local))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req) // an accepted SSE GET streams until ctx expires
		return rec.Code
	}
	loopback := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8080}
	bridge := &net.TCPAddr{IP: net.IPv4(172, 18, 0, 5), Port: 8080}

	h := s.Handler("/mcp")
	if code := send(h, http.MethodPost, "/mcp", loopback); code != http.StatusForbidden {
		t.Errorf("streamable, loopback + public Host, guard on: status = %d, want 403", code)
	}
	if code := send(h, http.MethodGet, "/mcp/sse", loopback); code != http.StatusForbidden {
		t.Errorf("sse, loopback + public Host, guard on: status = %d, want 403", code)
	}
	if code := send(h, http.MethodPost, "/mcp", bridge); code != http.StatusOK {
		t.Errorf("non-loopback listener must never trip the guard: status = %d", code)
	}
	s.SetDNSRebindingProtection(false)
	h = s.Handler("/mcp")
	if code := send(h, http.MethodPost, "/mcp", loopback); code != http.StatusOK {
		t.Errorf("streamable, guard off: status = %d, want 200", code)
	}
	if code := send(h, http.MethodGet, "/mcp/sse", loopback); code != http.StatusOK {
		t.Errorf("sse, guard off: status = %d, want 200", code)
	}
}
