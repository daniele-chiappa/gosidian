package server

import (
	"net/http"
	"strings"
	"sync/atomic"
)

// warmupRetryAfter is the Retry-After (seconds) served while the boot scan
// runs: a warm restart finishes well within it, a cold one (new vault,
// ContentVersion bump) takes a few rounds.
const warmupRetryAfter = "2"

// Switch is the handler a listener is opened with before the boot scan
// (IMP-104). Until Ready it answers every request with a 503 "starting";
// Ready swaps in the real handler for all later requests.
//
// The real handlers do not exist during the scan, and that is the point:
// the scan drops from the index every note missing from the list it took at
// the start, so a note written through MCP or REST while it runs would
// vanish from the index. Serving only 503s until the scan is over keeps
// clients answered (a 503 with Retry-After instead of a refused connection
// or a proxy 502) without that race.
type Switch struct {
	h atomic.Pointer[http.Handler]
}

// NewSwitch returns a Switch in the starting state.
func NewSwitch() *Switch { return &Switch{} }

// Ready makes h serve every request from now on.
func (s *Switch) Ready(h http.Handler) { s.h.Store(&h) }

func (s *Switch) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h := s.h.Load(); h != nil {
		(*h).ServeHTTP(w, r)
		return
	}
	serveStarting(w, r)
}

// serveStarting answers a request that arrives before Ready. /healthz keeps
// its JSON shape with status "starting" (gosidian healthcheck accepts only
// "ok", and the container start_period covers the scan); a browser gets a
// page that reloads itself; anything else gets the REST error shape, which
// MCP clients read as a plain HTTP 503.
func serveStarting(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Retry-After", warmupRetryAfter)
	h.Set("Cache-Control", "no-store")
	switch {
	case r.URL.Path == "/healthz":
		h.Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"starting"}` + "\n"))
	case strings.Contains(r.Header.Get("Accept"), "text/html"):
		h.Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(startingPage))
	default:
		h.Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":"server.unavailable","message":"gosidian is starting, retry shortly"}}` + "\n"))
	}
}

// startingPage is self-contained (no scripts, no external assets) so it
// passes any CSP and needs nothing the server is still loading.
const startingPage = `<!doctype html>
<html><head><meta charset="utf-8"><meta http-equiv="refresh" content="` + warmupRetryAfter + `">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>gosidian is starting</title></head>
<body style="font-family:system-ui,sans-serif;margin:3rem auto;max-width:32rem;padding:0 1rem">
<p>gosidian is starting. This page reloads by itself.</p>
</body></html>
`
