// Package mcp exposes the gosidian vault as a Model Context Protocol server,
// letting agents like Claude Code read and write notes as persistent memory.
package mcp

import (
	"context"
	"crypto/rand"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/server/events"
	"github.com/gosidian/gosidian/internal/vault"
	"github.com/mark3labs/mcp-go/server"
)

type ctxKey int

const (
	tokenCtxKey       ctxKey = 1
	correlationCtxKey ctxKey = 2
	langCtxKey        ctxKey = 3
	basePathCtxKey    ctxKey = 4
)

// basePathFromContext returns the mount prefix of the transport the current
// MCP session arrived on ("/mcp" on the single-port mux, "" on the legacy
// listener). Per-session in ctx rather than a Server field because Handler()
// is called once per transport and a shared field would let the last mount
// win — tickets minted over /mcp/sse would advertise the wrong endpoint.
func basePathFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(basePathCtxKey).(string); ok {
		return v
	}
	return ""
}

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
	projects           *projects.Store
	audit              *audit.Log
	impl               *server.MCPServer
	limiter            *writeLimiter
	maxNoteBytes       int64
	allowedUploadRoots []string
	bridgeDir          string
	// events is optional. When wired, MCP write handlers (create,
	// update, delete) publish on the `note` and `tree` topics so the
	// SPA SSE stream can invalidate caches in real time. nil keeps
	// the legacy behaviour (no announcements, the SPA polls).
	events *events.Hub
	// lintExtraAllowedTags extends the closed vocabulary checked by
	// the frontmatter-tag-unknown rule, sourced from a vault's
	// .gosidian/config.toml [lint.frontmatter_tag_vocabulary]. Empty
	// or nil means "use built-in vocabulary only" — no behaviour
	// change for vaults that do not configure it. Wired by main at
	// startup via SetLintExtraAllowedTags.
	lintExtraAllowedTags []string
	// lintHotOversizeBytes overrides the hot-oversize rule threshold;
	// <= 0 keeps the lint package default (16 KiB). Wired by main from
	// [lint] hot_oversize_bytes.
	lintHotOversizeBytes int64
	// selfImprove* gate, target and tune the self-improvement loop, wired
	// by main from config. With enabled=false (default) the
	// memory_self_improve tool rejects every call and the nudge middleware
	// stays silent. See plan 20260608-self-improve-feedback-loop.
	selfImproveEnabled       bool
	selfImproveProject       string
	selfImproveEveryN        int           // nudge cadence (tool calls); 0 disables the nudge
	selfImproveMaxPerSession int           // hard cap on nudges per session; 0 disables
	selfImproveCooldown      time.Duration // min time between nudges per session
	nudges                   *nudgeTracker
	// global* enable and name the shared global projects (skills/agents/
	// templates other projects reference). Wired by main from [global];
	// with globalEnabled=false the bootstrap merge is a no-op. See plan
	// 20260608-global-project-shared-skills.
	globalEnabled bool
	globalPublic  string
	globalPrivate string
	// anchorsEnabled is the master switch for local agent-anchor materialisation
	// (plan 20260630-agent-anchors). With anchorsEnabled=false (default) the
	// bootstrap never surfaces an anchors payload — behaviour unchanged.
	// Per-project opt-in is projects.Flags.UseAnchors.
	anchorsEnabled bool
	// waiters tracks in-flight memory_wait_changes calls (one per MCP
	// session, keyed by correlation id — token id as fallback) so a client
	// cannot pile up blocking long-polls and exhaust connections.
	waiters sync.Map
	// ingestURLAllow is the URL-prefix allowlist for the memory_ingest `url`
	// source (ADR-018). Empty (default) keeps the channel disabled — the
	// allowlist is the SSRF boundary, so there is no implicit default.
	ingestURLAllow []ingestPrefix
	// ingestTickets holds the pending single-use upload tickets minted by
	// memory_ingest transfer:http. In-memory only: a restart voids pending
	// tickets, which is fine at their TTL. Guarded by ingestTicketsMu.
	ingestTickets   map[string]*ingestTicket
	ingestTicketsMu sync.Mutex
	// ingestTicketTTL overrides the ticket lifetime; <= 0 uses the default.
	ingestTicketTTL time.Duration
}

// SetEvents wires the SSE hub used to broadcast note/tree changes
// from MCP write handlers. The publisher path is best-effort — a nil
// hub no-ops, and a slow subscriber loses events under the hub's
// drop-oldest policy rather than back-pressuring the writer.
func (s *Server) SetEvents(h *events.Hub) {
	s.events = h
}

// SetLintExtraAllowedTags installs the per-vault extension to the
// frontmatter-tag-unknown rule's closed vocabulary. The list is the raw
// extra_allowed entries from the vault's config; the lint package
// validates and dedupes them. Pass nil to revert to built-in only.
func (s *Server) SetLintExtraAllowedTags(extra []string) {
	s.lintExtraAllowedTags = extra
}

// SetLintHotOversizeLimit overrides the hot-oversize lint threshold in
// bytes. Values <= 0 keep the lint package default.
func (s *Server) SetLintHotOversizeLimit(bytes int64) {
	s.lintHotOversizeBytes = bytes
}

// SetSelfImprove configures the agent-sourced self-improvement loop: the
// master switch and the target project for raw insights. With enabled=false
// (default) memory_self_improve rejects every call. An empty project keeps
// the built-in default ("insights").
func (s *Server) SetSelfImprove(enabled bool, project string) {
	s.selfImproveEnabled = enabled
	if project != "" {
		s.selfImproveProject = project
	}
}

// SetSelfImproveNudge configures the periodic nudge (Phase 2): a nudge is
// appended to a tool result every everyN calls, at most maxPerSession times
// per session, throttled by cooldown. Zero everyN or maxPerSession disables
// the nudge while leaving the memory_self_improve tool usable.
func (s *Server) SetSelfImproveNudge(everyN, maxPerSession int, cooldown time.Duration) {
	s.selfImproveEveryN = everyN
	s.selfImproveMaxPerSession = maxPerSession
	s.selfImproveCooldown = cooldown
}

// SetGlobal configures the shared global projects: the master switch and the
// public/private project names. With enabled=false the bootstrap global merge
// is a no-op.
func (s *Server) SetGlobal(enabled bool, public, private string) {
	s.globalEnabled = enabled
	s.globalPublic = public
	s.globalPrivate = private
}

// SetAgentAnchors configures the master switch for local agent-anchor
// materialisation. With enabled=false (default) the bootstrap never returns an
// anchors payload. Per-project opt-in is projects.Flags.UseAnchors.
func (s *Server) SetAgentAnchors(enabled bool) {
	s.anchorsEnabled = enabled
}

// publishNoteChange broadcasts a note-level write (create/update/
// delete) on the `note` topic. For mutations that change the tree
// shape (create, delete, rename) the tree topic is published too so
// the SPA sidebar invalidates its cache. Best-effort: no error
// surfaced to the caller, and slow subscribers lose events under the
// hub's drop-oldest policy.
func (s *Server) publishNoteChange(action, path string, etag string, treeAffected bool) {
	if s.events == nil {
		return
	}
	payload := map[string]any{
		"action": action,
		"path":   path,
		"source": "mcp",
	}
	if etag != "" {
		payload["etag"] = etag
	}
	s.events.Publish(events.TopicNote, payload)
	if treeAffected {
		s.events.Publish(events.TopicTree, payload)
	}
}

// publishTreeChange broadcasts a project-level mutation (rename/delete of a
// whole project) on the tree topic only: there is no single note to point
// at, but the sidebar and memory_wait_changes still need to learn that the
// tree changed shape.
func (s *Server) publishTreeChange(action, path string, extra map[string]any) {
	if s.events == nil {
		return
	}
	payload := map[string]any{"action": action, "path": path, "source": "mcp"}
	for k, v := range extra {
		payload[k] = v
	}
	s.events.Publish(events.TopicTree, payload)
}

// publishRename broadcasts a note move as a delete of the old path plus a
// create of the new one — the two actions every subscriber already handles —
// followed by an update for each note whose links were rewritten.
func (s *Server) publishRename(from, to string, rewritten []string) {
	s.publishNoteChange("delete", from, "", true)
	etag := ""
	if fresh, err := s.vault.Load(to); err == nil {
		etag = fresh.ETag()
	}
	s.publishNoteChange("create", to, etag, true)
	for _, p := range rewritten {
		if p != to && p != from {
			s.publishNoteChange("update", p, "", false)
		}
	}
}

// SetProjects wires the per-project flag store. When non-nil, projects with
// HiddenFromMCP=true are filtered out of list-style tools and rejected with
// an explicit "hidden by config" error when a tool receives the project name
// directly. nil = no per-project visibility filter (current behaviour).
func (s *Server) SetProjects(p *projects.Store) {
	s.projects = p
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

// SetBridgeDir configures the staging directory for bridge_filename uploads
// (IMP-059): an agent stages a file there cheaply (via a shared mount) and
// references it by bare filename; the server reads and consumes it. Empty
// disables the feature. The dir is automatically an allowed source_path root.
func (s *Server) SetBridgeDir(dir string) { s.bridgeDir = dir }

// SetIngestURLAllowlist configures the URL prefixes the memory_ingest `url`
// source may fetch from (ADR-018). Empty keeps the channel disabled: the
// allowlist is the SSRF boundary, so every prefix — including private-network
// ones like an internal screenshot service — must be an explicit choice.
// Entries are parsed up front; a malformed one is an error rather than a
// silently-ignored (or silently-permissive) prefix.
func (s *Server) SetIngestURLAllowlist(prefixes []string) error {
	parsed := make([]ingestPrefix, 0, len(prefixes))
	for _, raw := range prefixes {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		p, err := parseIngestPrefix(raw)
		if err != nil {
			return fmt.Errorf("ingest url allowlist: %w", err)
		}
		parsed = append(parsed, p)
	}
	s.ingestURLAllow = parsed
	return nil
}

// BridgeDir returns the configured staging directory (empty when unset).
func (s *Server) BridgeDir() string { return s.bridgeDir }

// effectiveUploadRoots returns the allowed roots with the vault root prepended
// and the bridge dir (when set) appended.
func (s *Server) effectiveUploadRoots() []string {
	roots := append([]string{s.vault.Root}, s.allowedUploadRoots...)
	if s.bridgeDir != "" {
		roots = append(roots, s.bridgeDir)
	}
	return roots
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
	s := &Server{
		vault:        v,
		index:        idx,
		tokens:       tokens,
		limiter:      newWriteLimiter(60),
		maxNoteBytes: 1 << 20,
		nudges:       newNudgeTracker(),
	}
	// The self-improve nudge middleware is bound to s, so s must exist
	// before NewMCPServer captures it. s.impl is assigned right after and
	// is only dereferenced at tool-call time, by which point it is set.
	s.impl = server.NewMCPServer(
		"gosidian",
		"0.1.0",
		server.WithToolCapabilities(true),
		server.WithToolHandlerMiddleware(instrumentMiddleware),
		server.WithToolHandlerMiddleware(s.selfImproveNudgeMiddleware),
		// Per-token tool profile: applied to tools/list and enforced on
		// tools/call by mcp-go (access-control boundary, not cosmetic).
		server.WithToolFilter(s.filterToolsByProfile),
	)
	s.registerTools()
	s.registerResourcesAndPrompts()
	return s
}

// Handler returns an http.Handler exposing the MCP SSE transport, ready to
// be mounted on any mux. basePath sets the URL prefix the SSE server
// announces to clients during the initial handshake (it ships back the
// "endpoint" event with the URL the client must POST messages to). Pass
// "" to mount at the root of an http.Server (legacy standalone shape);
// pass e.g. "/mcp" when mounting on a shared mux under that prefix —
// otherwise the announced message URL is a path the public mux won't
// route, and the client reports "SSE streaming not supported" because
// its POST to /message fails.
//
// The handler is wrapped with bearer-token auth when the underlying
// token store is non-empty (unknown bearers → 401; valid ones threaded
// through the per-session context for tool handlers).
//
// Each invocation constructs a fresh SSEServer instance — callers should
// obtain one handler per mount point. Internal session state is per
// SSEServer so this is correct: clients connect to one endpoint at a time.
//
// Path semantics with basePath="/mcp": the SSEServer matches /mcp/sse for
// the event stream and /mcp/message for client→server messages, using
// exact path matching against r.URL.Path. The mux must therefore route
// the prefix /mcp/ to this handler WITHOUT StripPrefix, so the SSE
// server still sees the full path.
func (s *Server) Handler(basePath string) http.Handler {
	opts := []server.SSEOption{
		server.WithSSEContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			ctx = context.WithValue(ctx, correlationCtxKey, generateCorrelationID())
			ctx = context.WithValue(ctx, basePathCtxKey, basePath)
			if lang := r.Header.Get("Accept-Language"); lang != "" {
				ctx = context.WithValue(ctx, langCtxKey, lang)
			}
			tok := s.authenticate(r)
			if tok != nil {
				ctx = context.WithValue(ctx, tokenCtxKey, tok)
			}
			return ctx
		}),
	}
	if basePath != "" {
		opts = append(opts, server.WithStaticBasePath(basePath))
	}
	sse := server.NewSSEServer(s.impl, opts...)

	handler := http.Handler(sse)
	if s.tokens != nil && !s.tokens.Empty() {
		handler = s.requireToken(sse)
	}

	// Mount a sibling HTTP upload endpoint at <basePath>/upload, sharing the
	// same MCP bearer auth (IMP-059). It lets agents POST file bytes over HTTP
	// instead of pushing them through the model context as base64 — the primary
	// cheap-ingestion path. The SSE handler keeps exact-matching
	// <basePath>/sse and /message under the catch-all.
	mux := http.NewServeMux()
	mux.HandleFunc(basePath+"/upload", s.handleHTTPUpload)
	// Read-side twin: GET the raw bytes of a note with the same bearer, so a
	// large note reaches the agent's disk without crossing the model context
	// (IMP-081).
	mux.HandleFunc(basePath+"/download", s.handleHTTPDownload)
	// Single-use ticket redemption for memory_ingest transfer:http (ADR-018).
	// No bearer here: the unguessable ticket id, bound to the minting token,
	// is the credential.
	mux.HandleFunc(basePath+"/ingest/", s.handleIngestTicketRedeem)
	mux.Handle("/", handler)
	return mux
}

// ServeSSE starts a standalone HTTP listener serving the MCP SSE endpoint
// at the root of addr. Kept for backward compatibility with the pre-v1.12
// deployment pattern (env GOSIDIAN_MCP_ADDR / --mcp-addr). New deployments
// should mount Handler() on the main web server (single-port mode) — see
// *internal/server.Server.MountMCP.
//
// Blocks until the listener stops or errors.
func (s *Server) ServeSSE(addr string) error {
	srv := &http.Server{
		Addr:    addr,
		Handler: s.Handler(""),
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
