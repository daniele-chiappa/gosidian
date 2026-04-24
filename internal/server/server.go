package server

import (
	"embed"
	"html/template"
	"io/fs"
	"net/http"
	"strings"

	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/gitsync"
	"github.com/gosidian/gosidian/internal/i18n"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/metrics"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/trash"
	"github.com/gosidian/gosidian/internal/vault"
	"github.com/gosidian/gosidian/internal/webauth"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

type Server struct {
	vault      *vault.Vault
	index      *index.Index
	tokens     *auth.Store
	webauth    *webauth.Store
	configPath string
	audit      *audit.Log
	gitSync    *gitsync.Sync
	trash      *trash.Bin
	renderer   *parser.Renderer
	tpls       map[string]*template.Template // template name -> layout+file set
	mux        *http.ServeMux

	// i18n: catalogue + default lang, both optional. When catalog is nil, the
	// template helper returns the key itself (dev-friendly).
	catalog     *i18n.Catalog
	defaultLang string

	// Build + runtime info, populated by SetBuildInfo for /healthz.
	version   string
	gitSyncOn bool

	// Login rate limiter: IP → recent failed attempts.
	loginFails loginLimiter
}

// SetI18n wires the translation catalogue + default language. Called by main
// at startup. Safe to skip when the binary ships without translations.
func (s *Server) SetI18n(cat *i18n.Catalog, defaultLang string) {
	s.catalog = cat
	if defaultLang == "" {
		defaultLang = "en"
	}
	s.defaultLang = defaultLang
}

// userLang resolves the preferred language for the current request using the
// chain: cookie `gosidian_lang` > Accept-Language > default.
func (s *Server) userLang(r *http.Request) string {
	if s == nil {
		return "en"
	}
	if c, err := r.Cookie("gosidian_lang"); err == nil && c.Value != "" {
		return c.Value
	}
	if h := r.Header.Get("Accept-Language"); h != "" {
		return h
	}
	if s.defaultLang != "" {
		return s.defaultLang
	}
	return "en"
}

// langCookieName is the canonical name used by the language switcher.
const langCookieName = "gosidian_lang"

// SetBuildInfo records metadata exposed through the /healthz endpoint. Called
// by main after construction.
func (s *Server) SetBuildInfo(version string, gitSyncEnabled bool) {
	s.version = version
	s.gitSyncOn = gitSyncEnabled
}

// SetAuditLog wires the audit log used by mutating handlers.
func (s *Server) SetAuditLog(a *audit.Log) {
	s.audit = a
}

// SetGitSync wires the git sync helper used by the per-note history page.
func (s *Server) SetGitSync(g *gitsync.Sync) {
	s.gitSync = g
}

// SetTrash enables the soft-delete bin for HTTP delete handlers. nil keeps
// the legacy hard delete behavior.
func (s *Server) SetTrash(t *trash.Bin) {
	s.trash = t
}

// templateFiles lists all templates (pages + partial-only) that should be
// parsed as their own isolated set combining layout.html + the file itself.
// Isolated sets avoid cross-contamination of the `body` block across files.
var templateFiles = []string{
	"note_view.html",
	"note_edit.html",
	"search_results.html",
	"tags.html",
	"tag_notes.html",
	"graph.html",
	"index.html",
	"tree.html",
	"backlinks.html",
	"projects.html",
	"project_detail.html",
	"project_dashboard.html",
	"settings.html",
	"login.html",
	"audit.html",
	"history.html",
	"trash.html",
	"tokens.html",
	"users.html",
	"signup.html",
}

func New(v *vault.Vault, idx *index.Index, tokens *auth.Store, configPath string, webauthStore *webauth.Store) *Server {
	tpls := make(map[string]*template.Template, len(templateFiles))
	for _, name := range templateFiles {
		t := template.Must(
			template.New("layout.html").Funcs(funcMap()).
				ParseFS(templatesFS, "templates/layout.html", "templates/"+name),
		)
		tpls[name] = t
	}

	s := &Server{
		vault:      v,
		index:      idx,
		tokens:     tokens,
		webauth:    webauthStore,
		configPath: configPath,
		renderer:   parser.NewRenderer(),
		tpls:       tpls,
		mux:        http.NewServeMux(),
	}

	sub, _ := fs.Sub(staticFS, "static")
	s.mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(sub))))

	s.mux.HandleFunc("/theme.css", s.handleThemeCSS)
	s.mux.HandleFunc("/healthz", s.handleHealth)
	s.mux.Handle("/metrics", metrics.Handler())
	s.mux.HandleFunc("/login", s.handleLogin)
	s.mux.HandleFunc("/logout", s.handleLogout)
	s.mux.HandleFunc("/", s.handleRoot)
	s.mux.HandleFunc("/notes/", s.handleNotes)
	s.mux.HandleFunc("/notes/new", s.handleNewNote)
	s.mux.HandleFunc("/api/tree", s.handleTree)
	s.mux.HandleFunc("/api/backlinks", s.handleBacklinks)
	s.mux.HandleFunc("/api/graph", s.handleGraphJSON)
	s.mux.HandleFunc("/api/preview", s.handlePreview)
	s.mux.HandleFunc("/api/note-excerpt", s.handleNoteExcerpt)
	s.mux.HandleFunc("/api/command-palette", s.handleCommandPalette)
	s.mux.HandleFunc("/api/attach", s.handleAttach)
	s.mux.HandleFunc("/vault-files/", s.handleVaultFile)
	s.mux.HandleFunc("/search", s.handleSearch)
	s.mux.HandleFunc("/tags", s.handleTags)
	s.mux.HandleFunc("/tags/", s.handleTagsByName)
	s.mux.HandleFunc("/graph", s.handleGraphPage)
	s.mux.HandleFunc("/projects", s.handleProjects)
	s.mux.HandleFunc("/projects/", s.handleProjectDetail)
	s.mux.HandleFunc("/settings", s.handleSettings)
	s.mux.HandleFunc("/admin/tokens", s.handleTokens)
	s.mux.HandleFunc("/admin/users", s.handleUsers)
	s.mux.HandleFunc("/signup", s.handleSignup)
	s.mux.HandleFunc("/api/i18n", s.handleI18nSet)
	s.mux.HandleFunc("/audit", s.handleAudit)
	s.mux.HandleFunc("/trash", s.handleTrash)
	s.mux.HandleFunc("/trash/", s.handleTrashAction)

	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	rw := &statusRecorder{ResponseWriter: w, status: 200}
	defer func() {
		metrics.ObserveHTTP(r.Method, routeLabel(r.URL.Path), rw.status, started)
	}()
	w = rw

	// Web UI auth middleware: when a webauth account is provisioned, every
	// request except the open paths must carry a valid session cookie.
	if s.webauth != nil && s.webauth.Enabled() && !isOpenPath(r.URL.Path) {
		if !s.isAuthenticated(r) {
			// HTMX requests should stay on the page with a 401 so they don't
			// redirect inside a partial swap.
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("HX-Redirect", "/login?next="+r.URL.Path)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			http.Redirect(w, r, "/login?next="+r.URL.Path, http.StatusSeeOther)
			return
		}
	}
	s.mux.ServeHTTP(w, r)
}

// statusRecorder lets the metrics middleware observe the final status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// routeLabel collapses URL paths into bounded label values so metrics
// cardinality stays manageable. Group anything under /notes/ as one route,
// likewise /projects/ etc.
func routeLabel(p string) string {
	switch {
	case p == "/" || p == "/healthz" || p == "/metrics" || p == "/login" || p == "/logout":
		return p
	case strings.HasPrefix(p, "/notes/"):
		return "/notes/*"
	case strings.HasPrefix(p, "/projects/"):
		return "/projects/*"
	case strings.HasPrefix(p, "/tags/"):
		return "/tags/*"
	case strings.HasPrefix(p, "/api/"):
		return p
	case strings.HasPrefix(p, "/static/"):
		return "/static/*"
	case strings.HasPrefix(p, "/vault-files/"):
		return "/vault-files/*"
	case strings.HasPrefix(p, "/trash/"):
		return "/trash/*"
	}
	return p
}

// isOpenPath lists routes that never require authentication: the login
// endpoints themselves, the liveness probe, static assets, and the signup
// page (which is gated by the invite token inside the query string instead).
//
// Note: vault attachments (/vault-files/*) are NOT open — they live in the
// vault and are part of the user's content, so they require login when web
// auth is on.
func isOpenPath(p string) bool {
	switch p {
	case "/login", "/logout", "/healthz", "/metrics", "/theme.css", "/signup":
		return true
	}
	return strings.HasPrefix(p, "/static/")
}

// isAuthenticated returns true if r carries a live session cookie.
func (s *Server) isAuthenticated(r *http.Request) bool {
	if s.webauth == nil {
		return true
	}
	c, err := r.Cookie(webauth.SessionCookieName)
	if err != nil {
		return false
	}
	return s.webauth.ValidateSession(c.Value)
}

// currentUser returns the webauth user behind the session cookie on r, or
// nil when web auth is disabled or the request is unauthenticated. Handlers
// sitting behind the auth middleware can rely on this to scope behavior by
// user id / role without re-parsing the cookie.
func (s *Server) currentUser(r *http.Request) *webauth.User {
	if s.webauth == nil || !s.webauth.Enabled() {
		return nil
	}
	c, err := r.Cookie(webauth.SessionCookieName)
	if err != nil {
		return nil
	}
	u, ok := s.webauth.UserBySession(c.Value)
	if !ok {
		return nil
	}
	return u
}

// resolver bridges parser.Resolver to the index.
func (s *Server) resolver() parser.Resolver {
	return parser.ResolverFunc(func(target string) string {
		// re-use index resolution logic by trying exact/title/basename match.
		return s.indexResolve(target)
	})
}

func (s *Server) indexResolve(target string) string {
	notes, err := s.index.AllNotes()
	if err != nil {
		return ""
	}
	// 1. exact path
	for _, n := range notes {
		if n.Path == target || n.Path == target+".md" {
			return n.Path
		}
	}
	// 2. title (case-insensitive)
	for _, n := range notes {
		if equalFold(n.Title, target) {
			return n.Path
		}
	}
	// 3. basename
	for _, n := range notes {
		if basenameMatch(n.Path, target) {
			return n.Path
		}
	}
	return ""
}
