// Package server wires the v2.0 HTTP surface: the Vue SPA shell at /,
// the embedded Vite assets at /static/dist/*, the REST API under
// /api/v1/*, and the MCP SSE bridge at /mcp/*. The legacy HTMX
// templates + per-page handlers were retired at the v2.0 cutover;
// see docs/migration-v2.md for the downgrade path.
package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/gitsync"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/metrics"
	"github.com/gosidian/gosidian/internal/vault"
)

type Server struct {
	vault   *vault.Vault
	index   *index.Index
	mux     *http.ServeMux
	gitSync *gitsync.Sync

	// Build + runtime info, populated by SetBuildInfo for /healthz.
	version   string
	gitSyncOn bool

	// vaultFileAuthz gates /vault-files/ (ADR-022, BUG-031). It returns 0 to
	// allow, or the HTTP status to answer with (401 unauthenticated, 404 when
	// the principal must not learn the file exists). Injected by main from the
	// api/v1 router — this package cannot import it. nil fails closed.
	vaultFileAuthz func(r *http.Request, rel string) int
}

// SetVaultFileAuthorizer installs the /vault-files/ gate. See vaultFileAuthz.
func (s *Server) SetVaultFileAuthorizer(fn func(r *http.Request, rel string) int) {
	s.vaultFileAuthz = fn
}

// New wires the minimal HTTP surface for v2.0. Only the SPA shell,
// Vite-fingerprinted assets, /healthz, /metrics, /vault-files/, and
// /api/download-vault are registered up front; /api/v1/* and /mcp/*
// arrive through MountAPIv1 / MountMCP after the dependencies are
// built in cmd/gosidian/main.go.
//
// Earlier signatures took (tokens, configPath, webauth) for the HTML
// flows; those parameters were dropped at the v2.0 cutover because
// the SPA carries Bearer-token auth on /api/v1/* and MCP runs its
// own auth. We keep the (vault, idx) pair so /healthz can report
// note counts and /vault-files/ can resolve attachment paths.
func New(v *vault.Vault, idx *index.Index) *Server {
	s := &Server{
		vault: v,
		index: idx,
		mux:   http.NewServeMux(),
	}

	s.mux.HandleFunc("/healthz", s.handleHealth)
	s.mux.Handle("/metrics", metrics.Handler())
	s.mux.HandleFunc("/static/dist/", s.handleSpaStatic)
	s.mux.HandleFunc("/vault-files/", s.handleVaultFile)
	// SPA catch-all on `/` — handles every unmatched route so Vue
	// Router (history mode) can take over: refreshing /notes/foo or
	// /admin/users returns the shell HTML, the client-side router
	// resolves the route, and the matching view paints.
	s.mux.HandleFunc("/", s.handleSPA)
	return s
}

// SetBuildInfo records metadata exposed through /healthz. Called by
// main after construction.
func (s *Server) SetBuildInfo(version string, gitSyncEnabled bool) {
	s.version = version
	s.gitSyncOn = gitSyncEnabled
}

// SetGitSync wires the git-sync helper used by /healthz to report
// pull/commit health.
func (s *Server) SetGitSync(g *gitsync.Sync) {
	s.gitSync = g
}

// MountAPIv1 wires the REST API under /api/v1/. Called once at
// startup with a fully-built apiv1.Router.
func (s *Server) MountAPIv1(handler http.Handler) {
	s.mux.Handle("/api/v1/", handler)
}

// MountMCP wires the MCP SSE bridge under /mcp/. The handler must
// be configured with a basePath that matches the mount prefix (see
// internal/mcp.Server.Handler) so the SSE handshake announces the
// correct /mcp/message URL — otherwise clients fall back to "SSE
// streaming not supported" because their POST 404s on the mux.
func (s *Server) MountMCP(handler http.Handler) {
	s.mux.Handle("/mcp/", handler)
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	rw := &statusRecorder{ResponseWriter: w, status: 200}
	defer func() {
		metrics.ObserveHTTP(r.Method, routeLabel(r.URL.Path), rw.status, started)
	}()
	w = rw
	s.mux.ServeHTTP(w, r)
}

// handleVaultFile serves attachments under attachments/ subpaths from
// the vault. Restricted to those subpaths: the rest of the vault
// stays opaque from this endpoint to avoid accidental disclosure of
// arbitrary notes. Auth on this surface is handled by the same
// browser session that drives the SPA — attachments are referenced
// from inside the vault content the user already has access to, so
// the access bar is "you're on the page".
func (s *Server) handleVaultFile(w http.ResponseWriter, r *http.Request) {
	rel := strings.TrimPrefix(r.URL.Path, "/vault-files/")
	if rel == "" {
		http.NotFound(w, r)
		return
	}
	clean, err := s.vault.Rel(rel)
	if err != nil {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	if !strings.Contains("/"+clean, "/attachments/") {
		http.NotFound(w, r)
		return
	}
	// Attachments require the same authentication as the notes API
	// (ADR-022). Decided before any disk access; no authorizer = fail closed.
	status := http.StatusUnauthorized
	if s.vaultFileAuthz != nil {
		status = s.vaultFileAuthz(r, clean)
	}
	switch status {
	case 0:
	case http.StatusUnauthorized:
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	default:
		http.NotFound(w, r)
		return
	}
	abs, err := s.vault.Abs(clean)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// ?inline returns the file as an RFC 2397 data: URI (text/plain) so the
	// sandboxed HTML-note iframe (CSP img-src data:) can render it. The bytes
	// are content-addressed and immutable, so the data: URI is permanently
	// cacheable — the browser keeps it and never regenerates the base64.
	if r.URL.Query().Has("inline") {
		data, rerr := os.ReadFile(abs)
		if rerr != nil {
			http.NotFound(w, r)
			return
		}
		uri := attach.DataURI(data, filepath.Ext(clean))
		if uri == "" {
			// Extension outside the attachment allowlist: DataURI fails closed.
			http.NotFound(w, r)
			return
		}
		setAttachmentSecurityHeaders(w)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		_, _ = w.Write([]byte(uri))
		return
	}
	ext := strings.ToLower(filepath.Ext(clean))
	ct := "application/octet-stream"
	isImage := false
	if info, ok := attach.AllowedExt[ext]; ok {
		ct = info.MIME
		isImage = info.IsImage
	}
	setAttachmentSecurityHeaders(w)
	if !isImage {
		// Non-image attachments are downloads, never documents rendered in the
		// app origin. Images stay inline so <img> embedding keeps working.
		w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(clean)+`"`)
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeFile(w, r, abs)
}

// setAttachmentSecurityHeaders makes an attachment response inert (ADR-021,
// BUG-030): whatever the bytes are — an SVG carrying a <script>, an HTML-
// looking text file — nothing executes in the app origin when the URL is
// opened directly. The sandbox CSP does not affect <img> embedding.
func setAttachmentSecurityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'none'; style-src 'none'; sandbox")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
}

// statusRecorder lets the metrics middleware observe the final status
// code. It also forwards http.Flusher / Unwrap so handlers downstream
// (notably the MCP SSE transport, which type-asserts the writer to
// http.Flusher and returns 500 "Streaming unsupported" otherwise)
// keep working when wrapped. Without these, mounting the MCP handler
// on the web mux breaks SSE streaming because Go's interface
// promotion does not propagate methods through an embedded interface
// field — only the concrete *http.response provides Flush(), and the
// wrapper hides it.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}

// routeLabel collapses URL paths into bounded label values so metrics
// cardinality stays manageable.
func routeLabel(p string) string {
	switch {
	case p == "/" || p == "/healthz" || p == "/metrics":
		return p
	case strings.HasPrefix(p, "/api/v1/"):
		return p
	case strings.HasPrefix(p, "/mcp/"):
		return "/mcp/*"
	case strings.HasPrefix(p, "/static/"):
		return "/static/*"
	case strings.HasPrefix(p, "/vault-files/"):
		return "/vault-files/*"
	}
	return p
}
