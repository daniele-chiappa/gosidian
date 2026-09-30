package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// Shutdown stops srvs together (nil entries are skipped). Each one closes
// its listeners and stops keeping connections alive first; only then does
// endStreams run, once, to end the long-lived requests (live updates, MCP
// HTTP+SSE sessions and subscriptions/listen, memory_wait_changes). Ending
// them earlier let their clients come straight back to this process, on a
// connection kept alive or through a listener still open, and the shutdown
// waited its whole timeout on the new request with the port already closed
// (BUG-069). A server still busy when ctx ends is closed, and the log says
// what it was serving.
func Shutdown(ctx context.Context, endStreams func(), inflight *Inflight, srvs ...*http.Server) {
	var once sync.Once
	end := func() { once.Do(endStreams) }
	var wg sync.WaitGroup
	for _, srv := range srvs {
		if srv == nil {
			continue
		}
		// net/http runs these right after closing the listeners.
		srv.RegisterOnShutdown(end)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := srv.Shutdown(ctx); err != nil {
				slog.Default().Warn("shutdown: still busy, closing the connections",
					"addr", srv.Addr, "err", err, "inflight", inflight.String())
				_ = srv.Close()
			}
		}()
	}
	wg.Wait()
}

// Inflight tracks the requests being served, so a shutdown that runs out of
// time can say what it waited on. The zero value is not usable: NewInflight.
type Inflight struct {
	mu   sync.Mutex
	reqs map[*http.Request]time.Time
}

// NewInflight returns an empty tracker.
func NewInflight() *Inflight { return &Inflight{reqs: map[*http.Request]time.Time{}} }

// Wrap records every request h serves for as long as it runs.
func (f *Inflight) Wrap(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.reqs[r] = time.Now()
		f.mu.Unlock()
		defer func() {
			f.mu.Lock()
			delete(f.reqs, r)
			f.mu.Unlock()
		}()
		h.ServeHTTP(w, r)
	})
}

// String lists the requests in flight, oldest first and at most ten, as
// "GET /mcp/sse from 127.0.0.1:39932 (4.2s)"; "none" when there are none.
func (f *Inflight) String() string {
	if f == nil {
		return "unknown"
	}
	type entry struct {
		r     *http.Request
		since time.Time
	}
	f.mu.Lock()
	all := make([]entry, 0, len(f.reqs))
	for r, t := range f.reqs {
		all = append(all, entry{r, t})
	}
	f.mu.Unlock()
	if len(all) == 0 {
		return "none"
	}
	sort.Slice(all, func(i, j int) bool { return all[i].since.Before(all[j].since) })
	parts := make([]string, 0, min(len(all), 10))
	for _, e := range all[:min(len(all), 10)] {
		// The escaped form holds no raw control bytes; the newline strip is
		// the sanitizer CodeQL's go/log-injection model recognizes.
		path := strings.ReplaceAll(strings.ReplaceAll(e.r.URL.EscapedPath(), "\n", ""), "\r", "")
		parts = append(parts, fmt.Sprintf("%s %s from %s (%s)", e.r.Method, path, e.r.RemoteAddr,
			time.Since(e.since).Round(100*time.Millisecond)))
	}
	if len(all) > 10 {
		parts = append(parts, fmt.Sprintf("and %d more", len(all)-10))
	}
	return strings.Join(parts, "; ")
}
