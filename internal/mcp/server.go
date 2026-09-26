// Package mcp exposes the gosidian vault as a Model Context Protocol server,
// letting agents like Claude Code read and write notes as persistent memory.
package mcp

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/authz"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/server/events"
	"github.com/gosidian/gosidian/internal/vault"
	"github.com/gosidian/gosidian/internal/webauth"
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
// from the request headers, or empty when the caller did not supply one.
// Tool handlers pick this up to localise error messages.
func LangFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(langCtxKey).(string); ok {
		return v
	}
	return ""
}

// generateCorrelationID returns a short random hex identifier for a message
// that carries no MCP session (protocol 2026-07-28 clients, tests). 8 hex
// chars = 32 bits = enough to disambiguate concurrent agent sessions on a
// self-hosted instance.
func generateCorrelationID() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "noid"
	}
	return hex.EncodeToString(buf[:])
}

// correlationIDFor returns the id that tags one MCP session's tool calls in
// the audit log, in git auto-commit messages, in the self-improve nudge
// budget and in the memory_wait_changes one-waiter guard. Both transports
// run the context func once per JSON-RPC message, after mcp-go attached the
// client session, so minting a random id there tagged nothing (BUG-053):
// the id is derived from the transport session id instead — stable for the
// life of an SSE connection or of a Streamable HTTP Mcp-Session-Id — and
// hashed, because the raw SSE session id authorises POSTs on the message
// endpoint and must not land in a log line. Sessionless messages keep a
// fresh random id.
func correlationIDFor(ctx context.Context) string {
	if cs := server.ClientSessionFromContext(ctx); cs != nil {
		if sid := cs.SessionID(); sid != "" {
			sum := sha256.Sum256([]byte(sid))
			return hex.EncodeToString(sum[:4])
		}
	}
	return generateCorrelationID()
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
	// accessResolver maps an OAuth access token (internal/oauth) to the grant
	// it was issued for; nil when OAuth is off. Consulted only for bearers the
	// static token store does not know.
	accessResolver func(plaintext string) (*auth.Token, bool)
	// principalResolver maps a token's OwnerUserID to that account's authz
	// principal (role) so a token can never outrun the account it belongs to:
	// see effectiveToken (BUG-055). nil (tests, deployments without web
	// accounts) leaves the token's own scope as the only gate.
	principalResolver func(userID string) (authz.Principal, bool)
	// oauthChallenge is the WWW-Authenticate value advertised on 401 when the
	// OAuth authorization server is enabled (carries resource_metadata so
	// clients can discover it); empty falls back to the plain realm.
	oauthChallenge string
	// dnsRebindingOff disables mcp-go's DNS-rebinding guard on both MCP
	// transports (403 for a request that arrived on a loopback-bound
	// connection with a Host header that is not a localhost value). The zero
	// value keeps the guard on; only a same-host reverse proxy that forwards
	// over 127.0.0.1 while preserving the public Host header needs it off.
	// Wired by main from [mcp] disable_dns_rebinding_protection.
	dnsRebindingOff bool
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

// SetDNSRebindingProtection toggles mcp-go's DNS-rebinding guard on both
// MCP transports. On by default; see dnsRebindingOff.
func (s *Server) SetDNSRebindingProtection(enabled bool) {
	s.dnsRebindingOff = !enabled
}

// SetAccessTokenResolver installs the OAuth access-token resolver (IMP-092).
// Bearers that are not static tokens are handed to fn; a hit yields the
// grant the token was minted for, which then authorizes the call exactly
// like a static token with the same projects and scopes.
func (s *Server) SetAccessTokenResolver(fn func(plaintext string) (*auth.Token, bool)) {
	s.accessResolver = fn
}

// SetPrincipalResolver installs the lookup from a token's OwnerUserID to the
// account's authz principal; a miss means the account is gone or disabled.
// With it set, every bearer owned by a non-owner account is narrowed on each
// request to what that account may currently read and write (BUG-055).
func (s *Server) SetPrincipalResolver(fn func(userID string) (authz.Principal, bool)) {
	s.principalResolver = fn
}

// SetOAuthChallenge sets the WWW-Authenticate value sent on 401 responses
// from every MCP endpoint (transports and byte endpoints), e.g. the one
// oauth.Server.Challenge builds with resource_metadata and scope.
func (s *Server) SetOAuthChallenge(challenge string) {
	s.oauthChallenge = challenge
}

// wwwAuthenticate is the 401 challenge shared by every MCP endpoint.
func (s *Server) wwwAuthenticate() string {
	if s.oauthChallenge != "" {
		return s.oauthChallenge
	}
	return `Bearer realm="gosidian"`
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
// the request didn't go through an HTTP transport (tests).
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

// Handler returns an http.Handler exposing both MCP HTTP transports and the
// sibling byte endpoints, ready to be mounted on any mux under basePath:
//
//   - <basePath>          Streamable HTTP (POST JSON-RPC). GET answers 405:
//     gosidian sends nothing server→client — change events travel inside
//     memory_wait_changes — so no long-lived stream sits behind a proxy.
//   - <basePath>/sse      HTTP+SSE, the legacy transport, kept for older
//     clients; <basePath>/message carries its client→server messages.
//   - <basePath>/upload, /download, /append, /ingest/<ticket>  byte endpoints.
//
// basePath is the prefix the transports announce to clients (the SSE
// handshake ships back the /message URL to POST to; tickets advertise the
// /ingest/ URL). Pass "" for the legacy standalone listener: it stays
// SSE-only because Streamable HTTP needs a non-empty exact path to route on.
//
// Both transports share one context func (bearer → token, correlation id,
// Accept-Language, basePath) and one bearer guard when the token store is
// non-empty (unknown bearers → 401). Each invocation constructs fresh
// transport servers — obtain one handler per mount point.
//
// The mux must route both the exact prefix and the prefix subtree to this
// handler WITHOUT StripPrefix: the transports match r.URL.Path exactly.
func (s *Server) Handler(basePath string) http.Handler {
	ctxFn := s.httpContext(basePath)
	opts := []server.SSEOption{
		server.WithSSEContextFunc(ctxFn),
		server.WithSSEDisableLocalhostProtection(s.dnsRebindingOff),
	}
	if basePath != "" {
		opts = append(opts, server.WithStaticBasePath(basePath))
	}
	sse := server.NewSSEServer(s.impl, opts...)

	mux := http.NewServeMux()
	// Sibling HTTP upload endpoint at <basePath>/upload, sharing the MCP
	// bearer auth (IMP-059): agents POST file bytes over HTTP instead of
	// pushing them through the model context as base64.
	mux.HandleFunc(basePath+"/upload", s.handleHTTPUpload)
	// Read-side twin: GET the raw bytes of a note with the same bearer, so a
	// large note reaches the agent's disk without crossing the model context
	// (IMP-081).
	mux.HandleFunc(basePath+"/download", s.handleHTTPDownload)
	// Append-only write endpoint for scripts that hold a bearer but no MCP
	// session (Claude Code hooks, IMP-094); same pipeline as memory_append.
	mux.HandleFunc(basePath+"/append", s.handleHTTPAppend)
	// Note listing for local read-only mirrors, per project, opt-in (IMP-102).
	mux.HandleFunc(basePath+"/manifest", s.handleHTTPManifest)
	// Single-use ticket redemption for memory_ingest transfer:http (ADR-018).
	// No bearer here: the unguessable ticket id, bound to the minting token,
	// is the credential.
	mux.HandleFunc(basePath+"/ingest/", s.handleIngestTicketRedeem)
	if basePath != "" {
		streamable := server.NewStreamableHTTPServer(s.impl,
			server.WithHTTPContextFunc(ctxFn),
			server.WithDisableStreaming(true),
			server.WithDisableLocalhostProtection(s.dnsRebindingOff),
		)
		// Exact path only: the subtree below stays with the SSE server.
		mux.Handle(basePath, s.transport(streamable))
	}
	mux.Handle("/", s.transport(sse))
	return mux
}

// httpContext returns the per-message context decorator shared by both HTTP
// transports: the correlation id (see correlationIDFor), the mount prefix
// the message arrived on (tickets advertise it), Accept-Language for
// localised errors, and the bearer token when valid. mcp-go invokes it once
// per JSON-RPC message with the client session already in ctx.
func (s *Server) httpContext(basePath string) func(ctx context.Context, r *http.Request) context.Context {
	return func(ctx context.Context, r *http.Request) context.Context {
		ctx = context.WithValue(ctx, correlationCtxKey, correlationIDFor(ctx))
		ctx = context.WithValue(ctx, basePathCtxKey, basePath)
		if lang := r.Header.Get("Accept-Language"); lang != "" {
			ctx = context.WithValue(ctx, langCtxKey, lang)
		}
		if tok := s.authenticate(r); tok != nil {
			ctx = context.WithValue(ctx, tokenCtxKey, tok)
		}
		return ctx
	}
}

// transport wraps a transport handler with what both share: the bearer guard
// when the token store is non-empty, and X-Accel-Buffering: no, which tells
// nginx-style proxies not to buffer the response — an SSE stream held in a
// proxy buffer never reaches the client.
func (s *Server) transport(next http.Handler) http.Handler {
	if s.tokens != nil && !s.tokens.Empty() {
		next = s.requireToken(next)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Accel-Buffering", "no")
		next.ServeHTTP(w, r)
	})
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
	if tok, err := s.tokens.Validate(raw); err == nil {
		return s.effectiveToken(tok)
	}
	if s.accessResolver != nil {
		if tok, ok := s.accessResolver(raw); ok {
			return s.effectiveToken(tok)
		}
	}
	return nil
}

// effectiveToken narrows a validated token to the live access of the account
// that owns it. A token records the projects and scopes granted at creation
// (or at OAuth consent); the account's project access can shrink afterwards
// — a membership removed or downgraded to read, member_scope switched, a
// project made private — and nothing rewrites the token, so the runtime
// re-derives the effective scope on every request instead:
//
//   - tokens without an owner (CLI/admin) or owned by the owner account keep
//     their scope unchanged;
//   - an owner that no longer resolves (deleted/disabled) fails closed (nil);
//   - otherwise the project list becomes the declared list — or every project
//     when the record is unscoped — filtered by CanAccessProject, the write
//     scope is dropped when the account may write none of them, and narrowed
//     per project when it may write only some (Token.AllowsWrite);
//   - an account left with no readable project is refused (nil): an empty
//     project list would otherwise mean "admin".
//
// The result is a copy; the stored record is never modified.
func (s *Server) effectiveToken(tok *auth.Token) *auth.Token {
	if tok == nil || tok.OwnerUserID == "" || s.principalResolver == nil {
		return tok
	}
	princ, ok := s.principalResolver(tok.OwnerUserID)
	if !ok {
		return nil
	}
	if princ.Role == webauth.RoleOwner {
		return tok
	}
	cfg := s.projects.AccessConfig()
	candidates := tok.ProjectList()
	if len(candidates) == 0 {
		projs, err := s.vault.Projects()
		if err != nil {
			return nil
		}
		for _, p := range projs {
			candidates = append(candidates, p.Name)
		}
	}
	readable := make([]string, 0, len(candidates))
	writable := map[string]bool{}
	for _, p := range candidates {
		if !princ.CanAccessProject(p, cfg) {
			continue
		}
		readable = append(readable, p)
		if princ.CanWriteProject(p, cfg) {
			writable[p] = true
		}
	}
	if len(readable) == 0 {
		return nil
	}
	eff := *tok
	eff.Project = ""
	eff.Projects = readable
	switch {
	case len(writable) == 0:
		eff.Scopes = withoutScope(tok.Scopes, auth.ScopeWrite)
	case len(writable) < len(readable):
		return eff.WithWriteFilter(func(project string) bool { return writable[project] })
	}
	return &eff
}

// withoutScope returns scopes minus the named one, leaving the input as is.
func withoutScope(scopes []string, drop string) []string {
	out := make([]string, 0, len(scopes))
	for _, sc := range scopes {
		if sc != drop {
			out = append(out, sc)
		}
	}
	return out
}

// requireToken enforces Bearer auth at the HTTP layer before any transport
// handshake.
func (s *Server) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.authenticate(r) == nil {
			w.Header().Set("WWW-Authenticate", s.wwwAuthenticate())
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
