package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
)

// Client is an OAuth client known to the server: dynamically registered
// (persisted in the client store) or resolved from a Client ID Metadata
// Document (cached in memory, never persisted). All clients are public —
// there is no client secret; PKCE binds the code to the requester.
type Client struct {
	ID           string    `json:"client_id"`
	Name         string    `json:"client_name,omitempty"`
	RedirectURIs []string  `json:"redirect_uris"`
	CreatedAt    time.Time `json:"created_at"`
	LastUsed     time.Time `json:"last_used,omitempty"`
	// CIMD marks a client identified by its metadata-document URL.
	CIMD bool `json:"-"`
}

// LoopbackOnly reports whether every registered redirect is a loopback
// address — the case the consent screen must warn about, since any local
// process can bind a port and claim to be the client.
func (c *Client) LoopbackOnly() bool {
	if len(c.RedirectURIs) == 0 {
		return false
	}
	for _, raw := range c.RedirectURIs {
		u, err := url.Parse(raw)
		if err != nil || !isLoopbackHost(u.Hostname()) {
			return false
		}
	}
	return true
}

// clientStore persists dynamically registered clients as JSON. Bounded: at
// the cap, clients idle for longer than idleTTL are evicted (oldest first);
// if none qualifies, registration is refused.
type clientStore struct {
	path    string
	max     int
	idleTTL time.Duration
	mu      sync.Mutex
	clients map[string]*Client
}

type clientsFile struct {
	Clients []*Client `json:"clients"`
}

func openClientStore(path string, max int, idleTTL time.Duration) (*clientStore, error) {
	cs := &clientStore{path: path, max: max, idleTTL: idleTTL, clients: map[string]*Client{}}
	if path == "" {
		return cs, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cs, nil
		}
		return nil, err
	}
	var f clientsFile
	if len(data) > 0 {
		if err := json.Unmarshal(data, &f); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	}
	for _, c := range f.Clients {
		if c != nil && c.ID != "" {
			cs.clients[c.ID] = c
		}
	}
	return cs, nil
}

// save writes the file atomically; callers hold mu.
func (cs *clientStore) save() error {
	if cs.path == "" {
		return nil
	}
	list := make([]*Client, 0, len(cs.clients))
	for _, c := range cs.clients {
		list = append(list, c)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.Before(list[j].CreatedAt) })
	data, err := json.MarshalIndent(clientsFile{Clients: list}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cs.path), 0o755); err != nil {
		return err
	}
	tmp := cs.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, cs.path)
}

var errTooManyClients = errors.New("too many registered clients")

// add stores c, evicting idle clients when the cap is reached.
func (cs *clientStore) add(c *Client, now time.Time) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if len(cs.clients) >= cs.max {
		cs.evictIdle(now)
	}
	if len(cs.clients) >= cs.max {
		return errTooManyClients
	}
	cs.clients[c.ID] = c
	return cs.save()
}

// evictIdle drops clients whose last use (or creation) is older than idleTTL.
func (cs *clientStore) evictIdle(now time.Time) {
	cutoff := now.Add(-cs.idleTTL)
	for id, c := range cs.clients {
		last := c.LastUsed
		if last.IsZero() {
			last = c.CreatedAt
		}
		if last.Before(cutoff) {
			delete(cs.clients, id)
		}
	}
}

func (cs *clientStore) get(id string) (*Client, bool) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	c, ok := cs.clients[id]
	if !ok {
		return nil, false
	}
	out := *c
	return &out, true
}

// touch records a use; the write is best-effort (a lost timestamp only
// makes the client eligible for eviction a little earlier).
func (cs *clientStore) touch(id string, now time.Time) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if c, ok := cs.clients[id]; ok {
		c.LastUsed = now
		_ = cs.save()
	}
}

// ---- Dynamic Client Registration (RFC 7591) ----

type registrationRequest struct {
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
}

type registrationResponse struct {
	ClientID                string   `json:"client_id"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	ClientName              string   `json:"client_name,omitempty"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
}

const maxRegistrationBody = 16 << 10

// handleRegister registers a public client. Only the metadata gosidian acts
// on is validated; unknown fields are ignored as RFC 7591 allows.
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeOAuthError(w, http.StatusMethodNotAllowed, "invalid_request", "POST required")
		return
	}
	now := s.now()
	if !s.limiter.allow("register:"+s.cfg.ClientIP(r), now) {
		writeOAuthError(w, http.StatusTooManyRequests, "invalid_request", "too many registrations from this address, retry later")
		return
	}
	var req registrationRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRegistrationBody)).Decode(&req); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "body must be a JSON object")
		return
	}
	if len(req.RedirectURIs) == 0 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris is required")
		return
	}
	if len(req.RedirectURIs) > 10 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", "at most 10 redirect_uris")
		return
	}
	for _, raw := range req.RedirectURIs {
		if err := s.validRedirectURI(raw); err != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_redirect_uri", err.Error())
			return
		}
	}
	if m := req.TokenEndpointAuthMethod; m != "" && m != "none" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "only public clients are supported: token_endpoint_auth_method must be \"none\"")
		return
	}
	for _, g := range req.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "unsupported grant_type "+strconv.Quote(g))
			return
		}
	}
	for _, rt := range req.ResponseTypes {
		if rt != "code" {
			writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "unsupported response_type "+strconv.Quote(rt))
			return
		}
	}
	name := strings.TrimSpace(req.ClientName)
	if name == "" {
		name = "MCP client"
	}
	if len(name) > 100 {
		name = name[:100]
	}
	id, err := randomToken(clientPrefix)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "entropy unavailable")
		return
	}
	c := &Client{ID: id, Name: name, RedirectURIs: append([]string(nil), req.RedirectURIs...), CreatedAt: now.UTC()}
	if err := s.clients.add(c, now); err != nil {
		if errors.Is(err, errTooManyClients) {
			writeOAuthError(w, http.StatusServiceUnavailable, "server_error", "client registry is full")
			return
		}
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not persist client")
		return
	}
	s.auditWrite(audit.ActionOAuthClientRegister, "", s.cfg.ClientIP(r), c.ID, c.Name)
	writeJSON(w, http.StatusCreated, registrationResponse{
		ClientID:                c.ID,
		ClientIDIssuedAt:        now.Unix(),
		ClientName:              c.Name,
		RedirectURIs:            c.RedirectURIs,
		TokenEndpointAuthMethod: "none",
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
	})
}

// resolveClient finds the client for client_id: a metadata-document URL is
// fetched (and cached), anything else must be a registered id.
func (s *Server) resolveClient(ctx context.Context, clientID string) (*Client, error) {
	if strings.HasPrefix(clientID, "https://") {
		return s.cimd.resolve(ctx, clientID, s.now())
	}
	c, ok := s.clients.get(clientID)
	if !ok {
		return nil, errors.New("unknown client_id")
	}
	return c, nil
}

// ---- Client ID Metadata Documents ----

// cimdCache fetches and caches metadata documents. fetch is swappable for
// tests; the default refuses private and loopback destinations (SSRF).
type cimdCache struct {
	mu      sync.Mutex
	entries map[string]cimdEntry
	fetch   func(ctx context.Context, docURL string) ([]byte, http.Header, error)
}

type cimdEntry struct {
	client *Client
	err    error
	exp    time.Time
}

const (
	cimdMaxBody     = 64 << 10
	cimdTimeout     = 10 * time.Second
	cimdMinTTL      = 5 * time.Minute
	cimdMaxTTL      = 24 * time.Hour
	cimdDefaultTTL  = time.Hour
	cimdErrorTTL    = time.Minute
	cimdMaxEntries  = 500
	cimdMaxURLBytes = 2048
)

func newCIMDCache() *cimdCache {
	return &cimdCache{entries: map[string]cimdEntry{}, fetch: fetchCIMD}
}

func (c *cimdCache) resolve(ctx context.Context, docURL string, now time.Time) (*Client, error) {
	c.mu.Lock()
	if e, ok := c.entries[docURL]; ok && now.Before(e.exp) {
		c.mu.Unlock()
		if e.err != nil {
			return nil, e.err
		}
		out := *e.client
		return &out, nil
	}
	c.mu.Unlock()

	client, ttl, err := c.fetchAndParse(ctx, docURL)
	c.mu.Lock()
	if len(c.entries) >= cimdMaxEntries {
		for k, e := range c.entries {
			if !now.Before(e.exp) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= cimdMaxEntries {
			c.entries = map[string]cimdEntry{}
		}
	}
	c.entries[docURL] = cimdEntry{client: client, err: err, exp: now.Add(ttl)}
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	out := *client
	return &out, nil
}

func (c *cimdCache) fetchAndParse(ctx context.Context, docURL string) (*Client, time.Duration, error) {
	if len(docURL) > cimdMaxURLBytes {
		return nil, cimdErrorTTL, errors.New("client_id URL too long")
	}
	u, err := url.Parse(docURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Path == "" || u.Path == "/" || u.Fragment != "" {
		return nil, cimdErrorTTL, errors.New("client_id must be an https URL with a path")
	}
	body, hdr, err := c.fetch(ctx, docURL)
	if err != nil {
		return nil, cimdErrorTTL, fmt.Errorf("client metadata document: %w", err)
	}
	client, err := parseCIMD(docURL, body)
	if err != nil {
		return nil, cimdErrorTTL, err
	}
	return client, cacheTTL(hdr), nil
}

// parseCIMD validates the document the way the draft requires: client_id
// equal to the URL, redirect_uris present and valid, public client only.
func parseCIMD(docURL string, body []byte) (*Client, error) {
	var doc struct {
		ClientID                string   `json:"client_id"`
		ClientName              string   `json:"client_name"`
		RedirectURIs            []string `json:"redirect_uris"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, errors.New("client metadata document is not valid JSON")
	}
	if doc.ClientID != docURL {
		return nil, errors.New("client metadata document: client_id does not match the document URL")
	}
	if len(doc.RedirectURIs) == 0 || len(doc.RedirectURIs) > 20 {
		return nil, errors.New("client metadata document: redirect_uris missing or too many")
	}
	for _, raw := range doc.RedirectURIs {
		if err := validRedirectURI(raw); err != nil {
			return nil, fmt.Errorf("client metadata document: %w", err)
		}
	}
	if m := doc.TokenEndpointAuthMethod; m != "" && m != "none" {
		return nil, errors.New("client metadata document: only public clients (token_endpoint_auth_method \"none\") are supported")
	}
	name := strings.TrimSpace(doc.ClientName)
	if name == "" {
		name = docURL
	}
	if len(name) > 100 {
		name = name[:100]
	}
	return &Client{ID: docURL, Name: name, RedirectURIs: doc.RedirectURIs, CIMD: true}, nil
}

// cacheTTL honours Cache-Control max-age within [cimdMinTTL, cimdMaxTTL].
func cacheTTL(h http.Header) time.Duration {
	ttl := cimdDefaultTTL
	for _, part := range strings.Split(h.Get("Cache-Control"), ",") {
		part = strings.TrimSpace(strings.ToLower(part))
		if v, ok := strings.CutPrefix(part, "max-age="); ok {
			if n, err := strconv.Atoi(v); err == nil {
				ttl = time.Duration(n) * time.Second
			}
		}
		if part == "no-store" || part == "no-cache" {
			ttl = cimdMinTTL
		}
	}
	if ttl < cimdMinTTL {
		ttl = cimdMinTTL
	}
	if ttl > cimdMaxTTL {
		ttl = cimdMaxTTL
	}
	return ttl
}

// fetchCIMD downloads a metadata document with the SSRF guard: no
// redirects, every resolved address checked before dialing, tight timeout
// and body cap.
func fetchCIMD(ctx context.Context, docURL string) ([]byte, http.Header, error) {
	dialer := &net.Dialer{Timeout: cimdTimeout}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			if len(ips) == 0 {
				return nil, errors.New("no address")
			}
			for _, ip := range ips {
				if isDisallowedIP(ip.IP) {
					return nil, fmt.Errorf("refusing to fetch client metadata from %s (%s)", host, ip.IP)
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
		TLSHandshakeTimeout:    cimdTimeout,
		ResponseHeaderTimeout:  cimdTimeout,
		MaxResponseHeaderBytes: 8 << 10,
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   cimdTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("redirects are not followed")
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, docURL, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "gosidian-oauth/1")
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, cimdMaxBody+1))
	if err != nil {
		return nil, nil, err
	}
	if len(body) > cimdMaxBody {
		return nil, nil, errors.New("document larger than 64 KiB")
	}
	return body, resp.Header, nil
}

// isDisallowedIP rejects loopback, private, link-local, multicast,
// unspecified and CGNAT/ULA ranges — everything an authorization server
// must not be tricked into calling.
func isDisallowedIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast() {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		// 100.64.0.0/10 (CGNAT), 0.0.0.0/8, 192.0.0.0/24, 198.18.0.0/15, 240.0.0.0/4
		switch {
		case v4[0] == 100 && v4[1]&0xc0 == 64, v4[0] == 0,
			v4[0] == 192 && v4[1] == 0 && v4[2] == 0,
			v4[0] == 198 && v4[1]&0xfe == 18,
			v4[0] >= 240:
			return true
		}
	}
	return false
}

// ---- redirect URI rules ----

// validRedirectURI enforces the OAuth 2.1 rule: absolute, no fragment, and
// either https or http to a loopback host.
func validRedirectURI(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Host == "" {
		return fmt.Errorf("redirect_uri %q is not an absolute URL", raw)
	}
	if u.Fragment != "" {
		return fmt.Errorf("redirect_uri %q must not carry a fragment", raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if isLoopbackHost(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("redirect_uri %q: http is allowed only for loopback hosts", raw)
	}
	return fmt.Errorf("redirect_uri %q: scheme must be https (or http on loopback)", raw)
}

// validRedirectURI on the server also applies the optional host allowlist.
func (s *Server) validRedirectURI(raw string) error {
	if err := validRedirectURI(raw); err != nil {
		return err
	}
	if len(s.cfg.AllowedRedirectHosts) == 0 {
		return nil
	}
	u, _ := url.Parse(raw)
	host := strings.ToLower(u.Hostname())
	if isLoopbackHost(host) {
		return nil
	}
	for _, allowed := range s.cfg.AllowedRedirectHosts {
		if strings.EqualFold(strings.TrimSpace(allowed), host) {
			return nil
		}
	}
	return fmt.Errorf("redirect_uri host %q is not in the allowed list", host)
}

func isLoopbackHost(host string) bool {
	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// redirectMatches compares a presented redirect_uri with a registered one:
// exact match, except that loopback redirects ignore the port (RFC 8252
// §7.3 — native clients bind an ephemeral port per session).
func redirectMatches(registered, presented string) bool {
	if registered == presented {
		return true
	}
	ru, err1 := url.Parse(registered)
	pu, err2 := url.Parse(presented)
	if err1 != nil || err2 != nil {
		return false
	}
	if !isLoopbackHost(ru.Hostname()) || !isLoopbackHost(pu.Hostname()) {
		return false
	}
	return ru.Scheme == pu.Scheme && strings.EqualFold(ru.Hostname(), pu.Hostname()) &&
		ru.Path == pu.Path && ru.RawQuery == pu.RawQuery
}

// matchRedirect returns the registered URI that presented matches, if any.
func (c *Client) matchRedirect(presented string) (string, bool) {
	for _, reg := range c.RedirectURIs {
		if redirectMatches(reg, presented) {
			return reg, true
		}
	}
	return "", false
}
