package server

import (
	"bytes"
	"compress/gzip"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"

	apiv1 "github.com/gosidian/gosidian/internal/api/v1"
	"github.com/gosidian/gosidian/internal/server/web"
)

// handleSPA serves the embedded Vue 3 build for any URL that does
// not belong to the API or static asset surface. Vue Router runs in
// history mode, so a refresh on /notes/foo or /admin/users must
// return index.html with a 200 — the client-side router then
// resolves the route and renders the matching view.
//
// Routes that already have a server-side handler (API, static, MCP,
// healthz, metrics, vault-files) match earlier in the mux and never
// reach this fallback. We only guard against accidentally serving
// the SPA shell for asset-style requests that fell through (e.g. a
// missing /static/foo.js): those return 404 instead of the HTML
// shell so DevTools shows the real failure rather than an opaque
// "module loaded but is HTML".
func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	// Don't capture /api/* paths that fell through (anything not
	// under /api/v1/). Returning the SPA shell on legacy /api/preview
	// etc. masks the migration breakage; 404 makes it visible.
	if strings.HasPrefix(r.URL.Path, "/api/") {
		http.NotFound(w, r)
		return
	}
	// /.well-known/* is machine discovery, never a router target. With
	// OAuth off (or any document we don't serve) the shell would answer
	// 200 text/html, and an MCP client probing discovery after a 401 would
	// hit a parse error instead of a clean "not supported".
	if strings.HasPrefix(r.URL.Path, "/.well-known/") {
		http.NotFound(w, r)
		return
	}
	if looksLikeAsset(r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(web.DistFS, "dist/index.html")
	if err != nil {
		http.Error(w, "SPA build missing — run `npm run build` in web/", http.StatusInternalServerError)
		return
	}
	// Per-request CSP nonce. The shell CSP carries it in script-src and the
	// SPA reads it from the injected <meta> to stamp HTML-note <script> tags,
	// so they pass the policy the sandboxed srcdoc iframe inherits (BUG-019).
	nonce, err := apiv1.NewCSPNonce()
	if err != nil {
		http.Error(w, "csp nonce: "+err.Error(), http.StatusInternalServerError)
		return
	}
	apiv1.SetSPAShellHeaders(w, nonce)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store") // shell rotates per release
	body := string(data)
	meta := `<meta name="csp-nonce" content="` + nonce + `">`
	if strings.Contains(body, "</head>") {
		body = strings.Replace(body, "</head>", meta+"</head>", 1)
	} else {
		// Fail loud rather than silently shipping a shell whose CSP carries a
		// nonce the SPA can't read — HTML-note scripts would break invisibly.
		slog.Warn("SPA shell: </head> not found; CSP nonce <meta> not injected, HTML-note scripts will be blocked")
	}
	_, _ = w.Write([]byte(body))
}

// handleSpaStatic serves /static/dist/* directly from the embedded
// FS. Vite's manifest fingerprints filenames so we can cache
// aggressively. A text asset goes gzipped to a client that takes it: the
// web UI's first load drops from 590 KB to 162 (docs/benchmark.md).
func (s *Server) handleSpaStatic(w http.ResponseWriter, r *http.Request) {
	sub, err := fs.Sub(web.DistFS, "dist")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	r2 := r.Clone(r.Context())
	r2.URL.Path = strings.TrimPrefix(r.URL.Path, "/static/dist")
	if r2.URL.Path == "" {
		r2.URL.Path = "/"
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	name := strings.TrimPrefix(r2.URL.Path, "/")
	if compressible(name) {
		w.Header().Add("Vary", "Accept-Encoding")
		if acceptsGzip(r) {
			// Whole, with its length: ServeContent leaves Content-Length
			// out of an encoded body, and no browser asks a range of one.
			if a := gzipped(sub, name); a.gz != nil {
				w.Header().Set("Content-Encoding", "gzip")
				w.Header().Set("Content-Type", a.ctype)
				w.Header().Set("Content-Length", strconv.Itoa(len(a.gz)))
				if r.Method != http.MethodHead {
					_, _ = w.Write(a.gz)
				}
				return
			}
		}
	}
	http.FileServerFS(sub).ServeHTTP(w, r2)
}

// textAssets are the extensions of the build's files that gzip shrinks;
// images and fonts come compressed already.
var textAssets = map[string]bool{
	".js": true, ".mjs": true, ".css": true, ".svg": true, ".json": true,
	".map": true, ".html": true, ".txt": true, ".wasm": true,
}

func compressible(name string) bool { return textAssets[path.Ext(name)] }

// acceptsGzip reads Accept-Encoding: gzip, or *, with a q above zero.
func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		coding, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if coding = strings.ToLower(strings.TrimSpace(coding)); coding != "gzip" && coding != "*" {
			continue
		}
		if q, ok := strings.CutPrefix(strings.ReplaceAll(params, " ", ""), "q="); ok {
			if v, err := strconv.ParseFloat(q, 64); err == nil && v <= 0 {
				continue
			}
		}
		return true
	}
	return false
}

// gzipCache holds each text asset gzipped, made at its first request: the
// embedded files do not change while the server runs, and only names in
// the build get in.
var gzipCache sync.Map // name → gzAsset

// gzAsset is an asset gzipped, nil when gzip does not shrink it, and its
// type: from its extension, else sniffed from the file as FileServerFS
// does (a .map is text/plain), never from the gzipped bytes.
type gzAsset struct {
	gz    []byte
	ctype string
}

// gzipped is the asset gzipped at the best level; its gz is nil when the
// name is not in the build or the file would not get smaller.
func gzipped(fsys fs.FS, name string) gzAsset {
	if v, ok := gzipCache.Load(name); ok {
		return v.(gzAsset)
	}
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return gzAsset{}
	}
	a := gzAsset{ctype: mime.TypeByExtension(path.Ext(name))}
	if a.ctype == "" {
		a.ctype = http.DetectContentType(data)
	}
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	zw.Write(data)
	zw.Close()
	if buf.Len() < len(data) {
		a.gz = buf.Bytes()
	}
	v, _ := gzipCache.LoadOrStore(name, a)
	return v.(gzAsset)
}

// looksLikeAsset returns true for path shapes the SPA shell should
// not capture. The list mirrors Vite output conventions: .js/.css/
// fonts/images requested outside /static/dist/ are misconfigured
// callers, not router targets.
func looksLikeAsset(p string) bool {
	if !strings.Contains(p, ".") {
		return false
	}
	p = strings.TrimSuffix(p, "/")
	for _, ext := range assetExtensions {
		if strings.HasSuffix(p, ext) {
			return true
		}
	}
	return false
}

var assetExtensions = []string{
	".js", ".mjs", ".css", ".map",
	".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".ico",
	".woff", ".woff2", ".ttf", ".eot",
	".json", ".wasm",
}
