package server

import "testing"

// Every label routeLabel emits must come from a fixed set: an anonymous
// client probing random paths must not be able to mint new Prometheus
// series (BUG-048).
func TestRouteLabel_Bounded(t *testing.T) {
	cases := map[string]string{
		"/":                                   "/",
		"/healthz":                            "/healthz",
		"/metrics":                            "/metrics",
		"/api/v1/notes":                       "/api/v1/notes",
		"/api/v1/notes/gosidian/plans/x.md":   "/api/v1/notes/*",
		"/api/v1/projects/foo":                "/api/v1/projects/*",
		"/api/v1/admin/users/abc123":          "/api/v1/admin/users/*",
		"/api/v1/admin/tokens":                "/api/v1/admin/tokens",
		"/api/v1/admin/whatever/1":            "/api/v1/admin/other",
		"/api/v1/does-not-exist/1234":         "/api/v1/other",
		"/api/v1/login":                       "/api/v1/login",
		"/mcp":                                "/mcp",
		"/mcp/sse":                            "/mcp/*",
		"/static/app.js":                      "/static/*",
		"/vault-files/proj/attachments/a.png": "/vault-files/*",
		"/notes/some/spa/route":               "/*",
		"/random-" + string(make([]byte, 40)): "/*",
	}
	for path, want := range cases {
		if got := routeLabel(path); got != want {
			t.Errorf("routeLabel(%q)=%q, want %q", path, got, want)
		}
	}
}
