package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSwitch_StartingThenReady(t *testing.T) {
	sw := NewSwitch()
	get := func(path, accept string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		rec := httptest.NewRecorder()
		sw.ServeHTTP(rec, req)
		return rec
	}

	cases := []struct {
		name, path, accept, ctype, body string
	}{
		{"healthz", "/healthz", "", "application/json", `"status":"starting"`},
		{"browser", "/notes/x", "text/html,application/xhtml+xml", "text/html", `http-equiv="refresh"`},
		{"api", "/api/v1/tree", "application/json", "application/json", `"code":"server.unavailable"`},
		{"mcp", "/mcp", "application/json, text/event-stream", "application/json", `"code":"server.unavailable"`},
	}
	for _, c := range cases {
		rec := get(c.path, c.accept)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: status %d, want 503", c.name, rec.Code)
		}
		if rec.Header().Get("Retry-After") == "" {
			t.Errorf("%s: no Retry-After", c.name)
		}
		if !strings.HasPrefix(rec.Header().Get("Content-Type"), c.ctype) {
			t.Errorf("%s: Content-Type %q, want %s", c.name, rec.Header().Get("Content-Type"), c.ctype)
		}
		if !strings.Contains(rec.Body.String(), c.body) {
			t.Errorf("%s: body %q lacks %s", c.name, rec.Body.String(), c.body)
		}
	}

	sw.Ready(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	for _, c := range cases {
		if rec := get(c.path, c.accept); rec.Code != http.StatusTeapot || rec.Header().Get("Retry-After") != "" {
			t.Errorf("%s after Ready: status %d, Retry-After %q; want the real handler", c.name, rec.Code, rec.Header().Get("Retry-After"))
		}
	}
}
