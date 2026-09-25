package v1

import (
	"net/http"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/gitsync"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/oauth"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/projects"
	"github.com/gosidian/gosidian/internal/server/events"
	"github.com/gosidian/gosidian/internal/trash"
	"github.com/gosidian/gosidian/internal/vault"
)

// Deps bundles every backend dependency the v1 handlers need. Wired
// once by cmd/gosidian/main.go and held by the *Router for the
// lifetime of the process. Optional fields (Index, Trash, Renderer)
// gate the corresponding endpoints — handlers return 503 with
// CodeServerUnavailable when their dependency is nil so the SPA can
// surface the missing-feature state cleanly.
type Deps struct {
	Auth       *AuthDeps
	Audit      *audit.Log
	Vault      *vault.Vault
	Events     *events.Hub
	Index      *index.Index
	Trash      *trash.Bin
	Renderer   *parser.Renderer
	Projects   *projects.Store
	GitSync    *gitsync.Sync // optional; nil disables /history
	ConfigPath string        // path to cfg.toml; "" disables /settings PUT
	OAuth      *oauth.Server // optional; nil disables the consent API (IMP-092)
}

// Router owns the http.Handler tree under /api/v1/*. A separate type
// (rather than free functions on a mux) keeps state out of package
// globals, makes tests trivial — construct a Router with a tmpdir
// vault, call ServeHTTP — and gives a single place to attach
// telemetry hooks.
type Router struct {
	deps         *Deps
	mux          *http.ServeMux
	loginLimiter *loginLimiter
}

// NewRouter wires the v1 routes against the given dependencies. The
// caller mounts the returned handler under the /api/v1/ prefix.
func NewRouter(deps *Deps) *Router {
	r := &Router{
		deps:         deps,
		mux:          http.NewServeMux(),
		loginLimiter: newLoginLimiter(),
	}
	r.registerPublic()
	r.registerAuthed()
	return r
}

// ServeHTTP implements http.Handler so the parent mux can mount the
// router under any prefix without a wrapper.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	// Common middleware chain applied to every v1 endpoint, including
	// the public ones. Order matters:
	//   securityHeadersMW → outermost so cheap defense-in-depth
	//                       headers land on every response, even
	//                       errors that short-circuit early.
	//   observe          → slow-handler logging.
	//   originValidate   → conservative: allows missing Origin
	//                       (server-to-server, curl) and only rejects
	//                       browser-shaped requests with a mismatch.
	//   jsonHeaders      → Content-Type: application/json default.
	h := securityHeadersMW(observe(originValidate(jsonHeaders(r.mux))))
	h.ServeHTTP(w, req)
}

// registerPublic wires endpoints reachable without a Bearer token.
// Login is the entry point that issues new tokens; the rest are
// handshake utilities the SPA fetches before login (i18n catalog,
// build version banner).
func (r *Router) registerPublic() {
	r.mux.HandleFunc("/api/v1/health", r.handleHealth)
	r.mux.HandleFunc("/api/v1/version", r.handleVersion)
	r.mux.HandleFunc("/api/v1/auth-config", r.handleAuthConfig)
	r.mux.HandleFunc("/api/v1/i18n", r.handleI18nCatalog)
	r.mux.HandleFunc("/api/v1/login", r.handleLogin)
	r.mux.HandleFunc("/api/v1/signup", r.handleSignup)
	// SSE events endpoint is "public" in the routing sense — its own
	// handler validates the token from the query string because
	// EventSource cannot send custom headers.
	r.mux.HandleFunc("/api/v1/events", r.handleEvents)
}

// registerAuthed wires the rest of the API behind requireAuth (and
// requireOwner where applicable). Each handler is a thin shell at this
// stage; bodies arrive in subsequent phases.
func (r *Router) registerAuthed() {
	authed := func(h http.HandlerFunc) http.Handler {
		return r.deps.Auth.requireAuth(http.HandlerFunc(h))
	}
	owner := func(h http.HandlerFunc) http.Handler {
		return r.deps.Auth.requireAuth(r.deps.Auth.requireOwner(http.HandlerFunc(h)))
	}

	r.mux.Handle("/api/v1/me", authed(r.handleMe))
	r.mux.Handle("/api/v1/me/access", authed(r.handleMeAccess))
	r.mux.Handle("/api/v1/refresh", authed(r.handleRefresh))
	r.mux.Handle("/api/v1/logout", authed(r.handleLogout))

	r.mux.Handle("/api/v1/totp", authed(r.handleTOTPDisenroll))
	r.mux.Handle("/api/v1/totp/enroll", authed(r.handleTOTPEnroll))
	r.mux.Handle("/api/v1/totp/confirm", authed(r.handleTOTPConfirm))
	r.mux.Handle("/api/v1/totp/recovery-codes", authed(r.handleTOTPRecoveryCodes))
	// OAuth consent (IMP-092): the SPA screen reads the pending request and
	// posts the decision; the OAuth server itself lives outside /api/v1.
	r.mux.Handle("/api/v1/oauth/requests/", authed(r.handleOAuthRequest))

	r.mux.Handle("/api/v1/notes", authed(r.handleNotes))
	r.mux.Handle("/api/v1/notes/", authed(r.handleNoteByPath))
	r.mux.Handle("/api/v1/note-titles", authed(r.handleNoteTitles))
	r.mux.Handle("/api/v1/preview", authed(r.handlePreview))

	r.mux.Handle("/api/v1/projects", authed(r.handleProjects))
	r.mux.Handle("/api/v1/projects/", authed(r.handleProjectByName))

	r.mux.Handle("/api/v1/tags", authed(r.handleTags))
	r.mux.Handle("/api/v1/tags/", authed(r.handleTagByName))

	r.mux.Handle("/api/v1/search", authed(r.handleSearch))
	r.mux.Handle("/api/v1/graph", authed(r.handleGraph))
	r.mux.Handle("/api/v1/tree", authed(r.handleTree))
	r.mux.Handle("/api/v1/command-palette", authed(r.handleCommandPalette))

	r.mux.Handle("/api/v1/upload", authed(r.handleUpload))
	r.mux.Handle("/api/v1/attach", authed(r.handleAttach))

	r.mux.Handle("/api/v1/settings", authed(r.handleSettings))

	r.mux.Handle("/api/v1/trash", authed(r.handleTrash))
	r.mux.Handle("/api/v1/trash/", authed(r.handleTrashItem))

	r.mux.Handle("/api/v1/admin/tokens", owner(r.handleAdminTokens))
	r.mux.Handle("/api/v1/admin/tokens/", owner(r.handleAdminTokenItem))
	r.mux.Handle("/api/v1/admin/spa-tokens", owner(r.handleAdminSpaTokens))
	r.mux.Handle("/api/v1/admin/spa-tokens/", owner(r.handleAdminSpaTokenItem))
	r.mux.Handle("/api/v1/admin/users", owner(r.handleAdminUsers))
	r.mux.Handle("/api/v1/admin/users/", owner(r.handleAdminUserItem))
	r.mux.Handle("/api/v1/admin/teams", owner(r.handleAdminTeams))
	r.mux.Handle("/api/v1/admin/teams/", owner(r.handleAdminTeamItem))
	r.mux.Handle("/api/v1/admin/invites", owner(r.handleAdminInvites))
	r.mux.Handle("/api/v1/admin/invites/", owner(r.handleAdminInviteItem))
	r.mux.Handle("/api/v1/admin/audit", owner(r.handleAdminAudit))

	r.mux.Handle("/api/v1/insights/pending", owner(r.handleInsightsPending))
}

// Health is the only handler implemented end-to-end at Phase 0; it
// confirms the v1 mux is wired and reachable.
func (r *Router) handleHealth(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]any{
		"api":      "v1",
		"status":   "ok",
		"sse_subs": r.deps.Events.SubCount(),
	})
}

// All handlers (auth, notes CRUD, preview, note-titles, projects,
// tree, tags, search, settings, trash, admin/*, graph, upload, attach,
// command-palette, history, events SSE, i18n, version) live in their
// own files. notImplemented is kept around for any future stub.
