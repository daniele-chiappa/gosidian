// Package mcp exposes the gosidian vault as a Model Context Protocol server,
// letting agents like Claude Code read and write notes as persistent memory.
package mcp

import (
	"context"
	"crypto/rand"
	"net/http"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/vault"
	"github.com/mark3labs/mcp-go/server"
)

type ctxKey int

const (
	tokenCtxKey       ctxKey = 1
	correlationCtxKey ctxKey = 2
	langCtxKey        ctxKey = 3
)

// LangFromContext returns the Accept-Language value (first tag) extracted
// from the SSE handshake headers, or empty when the caller did not supply
// one. Tool handlers pick this up to localise error messages.
func LangFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(langCtxKey).(string); ok {
		return v
	}
	return ""
}

// generateCorrelationID returns a short random hex identifier suitable for
// tagging a single MCP session's mutations in the audit log and in git
// auto-commit messages. 8 hex chars = 32 bits = enough to disambiguate
// concurrent agent sessions on a self-hosted instance.
func generateCorrelationID() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "noid"
	}
	const hex = "0123456789abcdef"
	out := make([]byte, 8)
	for i, b := range buf {
		out[i*2] = hex[b>>4]
		out[i*2+1] = hex[b&0x0f]
	}
	return string(out)
}

// Server wraps a mark3labs MCPServer wired against a gosidian vault + index.
type Server struct {
	vault              *vault.Vault
	index              *index.Index
	tokens             *auth.Store
	audit              *audit.Log
	impl               *server.MCPServer
	limiter            *writeLimiter
	maxNoteBytes       int64
	allowedUploadRoots []string
}

// SetWriteLimits configures the per-token write/minute cap and the per-note
// size cap. Pass zero values to keep the defaults already set in New().
func (s *Server) SetWriteLimits(perMinute int, maxNoteBytes int64) {
	if perMinute > 0 {
		s.limiter = newWriteLimiter(perMinute)
	}
	if maxNoteBytes > 0 {
		s.maxNoteBytes = maxNoteBytes
	}
}

// SetAllowedUploadRoots configures the filesystem roots from which the
// source_path upload parameter is allowed to read. The vault root is always
// implicitly allowed and does not need to be listed.
func (s *Server) SetAllowedUploadRoots(roots []string) {
	s.allowedUploadRoots = roots
}

// effectiveUploadRoots returns the allowed roots with the vault root prepended.
func (s *Server) effectiveUploadRoots() []string {
	return append([]string{s.vault.Root}, s.allowedUploadRoots...)
}

// SetAuditLog wires the audit sink for mutating tool handlers.
func (s *Server) SetAuditLog(a *audit.Log) { s.audit = a }

// auditWrite records a mutating MCP operation. Token id from ctx if any.
// The actor field carries the human-friendly token name plus a session
// correlation id so multiple operations from the same MCP session can be
// grouped at retrospect time.
func (s *Server) auditWrite(ctx context.Context, action audit.Action, path, to string, size int64) {
	if s.audit == nil {
		return
	}
	tok := s.tokenFromContext(ctx)
	tokenID := ""
	tokenName := ""
	if tok != nil {
		tokenID = tok.ID
		tokenName = tok.Name
	}
	if cid := correlationIDFromContext(ctx); cid != "" {
		if tokenName != "" {
			tokenName = tokenName + "@" + cid
		} else {
			tokenName = "@" + cid
		}
	}
	_ = s.audit.Write(audit.Entry{
		Source: audit.SourceMCP,
		Token:  tokenID,
		Actor:  tokenName,
		Action: action,
		Path:   path,
		To:     to,
		Size:   size,
	})
}

// correlationIDFromContext returns the per-session id, or empty string when
// the request didn't go through the SSE pipeline (tests).
func correlationIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(correlationCtxKey).(string); ok {
		return v
	}
	return ""
}

// New builds an MCP server exposing the given vault and index. Tools are
// registered immediately; the server is not yet listening. If tokens is non
// nil and non empty, Bearer-token auth is enforced on the SSE transport.
func New(v *vault.Vault, idx *index.Index, tokens *auth.Store) *Server {
	impl := server.NewMCPServer(
		"gosidian",
		"0.1.0",
		server.WithToolCapabilities(true),
		server.WithToolHandlerMiddleware(instrumentMiddleware),
	)
	s := &Server{
		vault:        v,
		index:        idx,
		tokens:       tokens,
		impl:         impl,
		limiter:      newWriteLimiter(60),
		maxNoteBytes: 1 << 20,
	}
	s.registerTools()
	s.registerResourcesAndPrompts()
	return s
}

// ServeSSE starts the HTTP+SSE transport on addr. Blocks until the listener
// stops or errors. When the token store is non-empty, the SSE handler wraps
// authentication: unknown bearers receive 401, valid ones are threaded
// through the per-session context so tool handlers can scope by token.
func (s *Server) ServeSSE(addr string) error {
	sse := server.NewSSEServer(
		s.impl,
		server.WithSSEContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			ctx = context.WithValue(ctx, correlationCtxKey, generateCorrelationID())
			if lang := r.Header.Get("Accept-Language"); lang != "" {
				ctx = context.WithValue(ctx, langCtxKey, lang)
			}
			tok := s.authenticate(r)
			if tok != nil {
				ctx = context.WithValue(ctx, tokenCtxKey, tok)
			}
			return ctx
		}),
	)

	handler := http.Handler(sse)
	if s.tokens != nil && !s.tokens.Empty() {
		handler = s.requireToken(sse)
	}

	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
	}
	return srv.ListenAndServe()
}

// authenticate extracts and validates a Bearer token, returning nil on any
// failure. Callers decide whether to enforce.
func (s *Server) authenticate(r *http.Request) *auth.Token {
	if s.tokens == nil || s.tokens.Empty() {
		return auth.AdminToken()
	}
	raw := auth.ExtractBearer(r.Header.Get("Authorization"))
	if raw == "" {
		return nil
	}
	tok, err := s.tokens.Validate(raw)
	if err != nil {
		return nil
	}
	return tok
}

// requireToken enforces Bearer auth at the HTTP layer before any SSE handshake.
func (s *Server) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.authenticate(r) == nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="gosidian"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// tokenFromContext returns the authenticated token from ctx, or an admin
// token when auth is disabled (empty store).
func (s *Server) tokenFromContext(ctx context.Context) *auth.Token {
	if tok, ok := ctx.Value(tokenCtxKey).(*auth.Token); ok && tok != nil {
		return tok
	}
	if s.tokens == nil || s.tokens.Empty() {
		return auth.AdminToken()
	}
	// Should not happen: auth middleware would have rejected the request.
	return nil
}

// MCPServer returns the underlying mcp-go server. Exposed for tests so they
// can invoke tool handlers in-process without opening a socket.
func (s *Server) MCPServer() *server.MCPServer { return s.impl }
