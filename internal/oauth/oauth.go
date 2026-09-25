// Package oauth is the OAuth 2.1 authorization server embedded in gosidian
// and the resource-server glue that lets MCP clients (claude.ai, ChatGPT,
// Claude Code, Cursor…) obtain credentials through a browser consent flow
// instead of a pasted bearer token (IMP-092).
//
// Shape. Identities are the existing web-auth users; a consent becomes an
// auth.Token "grant" (auth.KindOAuth) — listed and revocable in
// /admin/tokens, swept by the user-disable cascade — while the short-lived
// access tokens handed to clients live only in memory and resolve to that
// grant on every request. Refresh tokens rotate on use; presenting a rotated
// one revokes the grant. Clients either register dynamically (RFC 7591) or
// identify themselves with a Client ID Metadata Document URL; only public
// clients exist (token_endpoint_auth_method "none", PKCE S256 mandatory).
//
// Endpoints (mount every path in Paths on the public mux): the RFC 8414
// authorization-server metadata, the RFC 9728 protected-resource metadata
// (root and per-resource), /oauth/register, /oauth/authorize, /oauth/token,
// /oauth/revoke. The consent itself is rendered by the SPA at
// Config.ConsentPath and talks to the server through Pending/Approve/Deny.
package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
)

const (
	// ScopeRead and ScopeWrite mirror the token scopes; ScopeOffline is the
	// conventional request for a refresh token (claude.ai adds it when the
	// metadata lists it). Refresh tokens are issued regardless.
	ScopeRead    = auth.ScopeRead
	ScopeWrite   = auth.ScopeWrite
	ScopeOffline = "offline_access"

	// Well-known paths (RFC 8414, RFC 9728).
	WellKnownAuthorizationServer = "/.well-known/oauth-authorization-server"
	WellKnownProtectedResource   = "/.well-known/oauth-protected-resource"

	PathRegister  = "/oauth/register"
	PathAuthorize = "/oauth/authorize"
	PathToken     = "/oauth/token"
	PathRevoke    = "/oauth/revoke"

	accessPrefix  = "gsa_"
	refreshPrefix = "gsr_"
	clientPrefix  = "gsc_"

	// Bounds that are not worth a config knob.
	maxPendingRequests = 1000
	maxCodes           = 1000
	maxAccessEntries   = 20000
	sweepEvery         = 256
)

// Config tunes the server. Zero values take the defaults noted per field.
type Config struct {
	// Issuer is the public origin clients reach gosidian at, e.g.
	// "https://notes.example.com". Required. Every advertised endpoint and
	// the canonical resource URL derive from it.
	Issuer string
	// ResourcePath is the MCP endpoint path under Issuer. Default "/mcp".
	ResourcePath string
	// ConsentPath is the SPA route that renders the consent screen. Default
	// "/oauth/consent". The authorize endpoint redirects there with ?req=.
	ConsentPath string
	AccessTTL   time.Duration // default 1h
	RefreshTTL  time.Duration // default 30 days; also the grant lifetime
	CodeTTL     time.Duration // default 5m
	RequestTTL  time.Duration // default 10m (pending consent)
	// ClientMax caps dynamically registered clients; ClientIdleTTL lets
	// unused ones be evicted when the cap is hit. Defaults 1000 / 90 days.
	ClientMax     int
	ClientIdleTTL time.Duration
	// ClientsPath is the JSON file holding registered clients (state dir).
	// Empty keeps them in memory only.
	ClientsPath string
	// AllowedRedirectHosts restricts non-loopback redirect URIs to these
	// hosts (exact, case-insensitive). Empty allows any HTTPS host.
	AllowedRedirectHosts []string
	// ClientIP extracts the peer address for rate limiting; nil uses
	// RemoteAddr. main passes the API's trusted-proxy aware resolver.
	ClientIP func(*http.Request) string
	Logger   *slog.Logger
}

func (c Config) withDefaults() Config {
	if c.ResourcePath == "" {
		c.ResourcePath = "/mcp"
	}
	if c.ConsentPath == "" {
		c.ConsentPath = "/oauth/consent"
	}
	if c.AccessTTL <= 0 {
		c.AccessTTL = time.Hour
	}
	if c.RefreshTTL <= 0 {
		c.RefreshTTL = 30 * 24 * time.Hour
	}
	if c.CodeTTL <= 0 {
		c.CodeTTL = 5 * time.Minute
	}
	if c.RequestTTL <= 0 {
		c.RequestTTL = 10 * time.Minute
	}
	if c.ClientMax <= 0 {
		c.ClientMax = 1000
	}
	if c.ClientIdleTTL <= 0 {
		c.ClientIdleTTL = 90 * 24 * time.Hour
	}
	if c.ClientIP == nil {
		c.ClientIP = remoteIP
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	return c
}

// Server holds the authorization-server state. Pending consents, codes and
// access tokens are in memory by design: a restart voids them and clients
// recover through the refresh token (persisted on the grant) or a fresh
// consent.
type Server struct {
	cfg     Config
	issuer  *url.URL
	tokens  *auth.Store
	audit   *audit.Log
	clients *clientStore
	cimd    *cimdCache
	limiter *ipLimiter
	now     func() time.Time

	mu       sync.Mutex
	pending  map[string]*pendingRequest
	codes    map[string]*authCode
	access   map[string]accessEntry     // sha256 hex of the access token
	consumed map[string]consumedRefresh // sha256 hex of rotated refresh tokens
	issued   int
}

// New validates cfg and opens the client store. tokens receives the grants;
// auditLog may be nil (no audit).
func New(cfg Config, tokens *auth.Store, auditLog *audit.Log) (*Server, error) {
	cfg = cfg.withDefaults()
	if tokens == nil {
		return nil, errors.New("oauth: token store required")
	}
	iss, err := parseIssuer(cfg.Issuer)
	if err != nil {
		return nil, err
	}
	cfg.Issuer = iss.String()
	clients, err := openClientStore(cfg.ClientsPath, cfg.ClientMax, cfg.ClientIdleTTL)
	if err != nil {
		return nil, fmt.Errorf("oauth: client store: %w", err)
	}
	s := &Server{
		cfg:      cfg,
		issuer:   iss,
		tokens:   tokens,
		audit:    auditLog,
		clients:  clients,
		cimd:     newCIMDCache(),
		limiter:  newIPLimiter(15*time.Minute, 30),
		now:      time.Now,
		pending:  map[string]*pendingRequest{},
		codes:    map[string]*authCode{},
		access:   map[string]accessEntry{},
		consumed: map[string]consumedRefresh{},
	}
	return s, nil
}

// parseIssuer accepts an absolute http(s) origin with an optional path and
// no query or fragment, returning it without a trailing slash.
func parseIssuer(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("oauth: issuer is required when oauth is enabled (the public URL clients reach gosidian at, e.g. https://notes.example.com)")
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("oauth: issuer %q must be an absolute http(s) URL without query or fragment", raw)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u, nil
}

// Issuer is the normalized issuer identifier.
func (s *Server) Issuer() string { return s.cfg.Issuer }

// ResourceURL is the canonical MCP resource identifier (RFC 8707): issuer +
// resource path, no trailing slash.
func (s *Server) ResourceURL() string { return s.cfg.Issuer + s.cfg.ResourcePath }

// ResourceMetadataURL is where the RFC 9728 document for the MCP resource
// lives (the path-suffixed form; the root form is served too).
func (s *Server) ResourceMetadataURL() string {
	return s.cfg.Issuer + WellKnownProtectedResource + s.cfg.ResourcePath
}

// Challenge is the WWW-Authenticate value the MCP endpoints send on 401 so
// clients discover the authorization server (MCP authorization spec).
func (s *Server) Challenge() string {
	return fmt.Sprintf(`Bearer realm="gosidian", resource_metadata=%q, scope="read write"`, s.ResourceMetadataURL())
}

// Paths lists every path Handler serves; mount each on the public mux.
func (s *Server) Paths() []string {
	return []string{
		WellKnownAuthorizationServer,
		WellKnownProtectedResource,
		WellKnownProtectedResource + s.cfg.ResourcePath,
		PathRegister, PathAuthorize, PathToken, PathRevoke,
	}
}

// Handler serves every OAuth endpoint by exact path.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(WellKnownAuthorizationServer, s.handleASMetadata)
	mux.HandleFunc(WellKnownProtectedResource, s.handlePRM)
	mux.HandleFunc(WellKnownProtectedResource+s.cfg.ResourcePath, s.handlePRM)
	mux.HandleFunc(PathRegister, s.handleRegister)
	mux.HandleFunc(PathAuthorize, s.handleAuthorize)
	mux.HandleFunc(PathToken, s.handleToken)
	mux.HandleFunc(PathRevoke, s.handleRevoke)
	return mux
}

// randomToken returns prefix + 32 random bytes, base64url without padding.
func randomToken(prefix string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// hashHex is the hex sha256 used for every stored secret.
func hashHex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) auditWrite(action audit.Action, userID, actor, path, to string) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Write(audit.Entry{
		Source: audit.SourceHTTP,
		Actor:  actor,
		UserID: userID,
		Action: action,
		Path:   path,
		To:     to,
	})
}
