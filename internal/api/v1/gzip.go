package v1

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
)

// gzipMinBytes is the size from which a JSON answer goes compressed: below
// it the gzip header and the CPU cost more than they save.
const gzipMinBytes = 8 << 10

var gzipWriters = sync.Pool{New: func() any {
	w, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression)
	return w
}}

// gzipJSON compresses a JSON answer of gzipMinBytes or more for a client
// that takes gzip (IMP-225): the server compressed none, and the tree a web
// UI asks for at each write went out as 287 KB, 26 in gzip. It holds the
// first bytes of a JSON answer to decide, and lets anything else through as
// it is written: the events stream with its flushes, downloads and files,
// an answer with its own length or encoding, a 304.
func gzipJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead || !acceptsGzip(r.Header.Get("Accept-Encoding")) {
			next.ServeHTTP(w, r)
			return
		}
		g := &gzipWriter{ResponseWriter: w}
		defer g.finish()
		next.ServeHTTP(g, r)
	})
}

// acceptsGzip reports whether an Accept-Encoding header takes gzip, which
// "gzip;q=0" refuses.
func acceptsGzip(header string) bool {
	for _, part := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
			continue
		}
		q, ok := strings.CutPrefix(strings.ReplaceAll(params, " ", ""), "q=")
		return !ok || strings.Trim(q, "0.") != ""
	}
	return false
}

const (
	gzUndecided   = iota
	gzPlain       // passed through as written
	gzHolding     // a JSON answer still under gzipMinBytes
	gzCompressing // in gzip
)

type gzipWriter struct {
	http.ResponseWriter
	status int
	mode   int
	held   []byte
	gz     *gzip.Writer
}

func (g *gzipWriter) WriteHeader(code int) {
	if g.mode != gzUndecided {
		return
	}
	g.status = code
	h := g.Header()
	if code < http.StatusOK || code == http.StatusNoContent || code == http.StatusNotModified ||
		!strings.HasPrefix(h.Get("Content-Type"), "application/json") ||
		h.Get("Content-Encoding") != "" || h.Get("Content-Length") != "" || h.Get("Content-Range") != "" {
		g.mode = gzPlain
		g.ResponseWriter.WriteHeader(code)
		return
	}
	h.Add("Vary", "Accept-Encoding")
	g.mode = gzHolding
}

func (g *gzipWriter) Write(p []byte) (int, error) {
	if g.mode == gzUndecided {
		g.WriteHeader(http.StatusOK)
	}
	switch g.mode {
	case gzPlain:
		return g.ResponseWriter.Write(p)
	case gzCompressing:
		return g.gz.Write(p)
	}
	g.held = append(g.held, p...)
	if len(g.held) >= gzipMinBytes {
		if err := g.compress(); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func (g *gzipWriter) compress() error {
	h := g.Header()
	h.Set("Content-Encoding", "gzip")
	h.Del("Content-Length")
	g.ResponseWriter.WriteHeader(g.status)
	g.gz = gzipWriters.Get().(*gzip.Writer)
	g.gz.Reset(g.ResponseWriter)
	g.mode = gzCompressing
	_, err := g.gz.Write(g.held)
	g.held = nil
	return err
}

// sendHeld sends a held answer as it is.
func (g *gzipWriter) sendHeld() {
	g.mode = gzPlain
	g.ResponseWriter.WriteHeader(g.status)
	if len(g.held) > 0 {
		_, _ = g.ResponseWriter.Write(g.held)
	}
	g.held = nil
}

// Flush sends what is written so far: a handler that flushes wants it out.
func (g *gzipWriter) Flush() {
	switch g.mode {
	case gzHolding:
		g.sendHeld()
	case gzCompressing:
		_ = g.gz.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (g *gzipWriter) finish() {
	switch g.mode {
	case gzHolding:
		g.sendHeld()
	case gzCompressing:
		_ = g.gz.Close()
		g.gz.Reset(io.Discard)
		gzipWriters.Put(g.gz)
	}
}

// Unwrap lets http.ResponseController reach the connection.
func (g *gzipWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }
