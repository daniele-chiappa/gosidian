package oauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// pendingRequest is an authorization request waiting for the user's consent.
type pendingRequest struct {
	ID            string
	Client        *Client
	RedirectURI   string
	State         string
	CodeChallenge string
	Resource      string
	Scopes        []string // requested, validated; may include offline_access
	Created       time.Time
	Expires       time.Time
}

// ConsentView is what the consent screen shows and decides on.
type ConsentView struct {
	RequestID    string    `json:"request_id"`
	ClientID     string    `json:"client_id"`
	ClientName   string    `json:"client_name"`
	ClientCIMD   bool      `json:"client_cimd"`
	RedirectURI  string    `json:"redirect_uri"`
	RedirectHost string    `json:"redirect_host"`
	LoopbackOnly bool      `json:"loopback_only"`
	Scopes       []string  `json:"scopes"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// ErrNoSuchRequest is returned by Pending/Approve/Deny for unknown or
// expired consent requests.
var ErrNoSuchRequest = errors.New("consent request not found or expired")

// handleAuthorize validates an authorization request and parks it for the
// consent screen. Errors in client_id/redirect_uri are answered to the
// user agent directly (never redirected, per OAuth 2.1); everything else is
// reported to the client through the redirect URI.
func (s *Server) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			writeAuthorizePage(w, http.StatusBadRequest, "invalid_request", "malformed form body")
			return
		}
	}
	q := r.Form
	if r.Method == http.MethodGet {
		q = r.URL.Query()
	}
	now := s.now()
	if !s.limiter.allow("authorize:"+s.cfg.ClientIP(r), now) {
		writeAuthorizePage(w, http.StatusTooManyRequests, "invalid_request", "too many authorization requests from this address, retry later")
		return
	}
	clientID := strings.TrimSpace(q.Get("client_id"))
	if clientID == "" {
		writeAuthorizePage(w, http.StatusBadRequest, "invalid_request", "client_id is required")
		return
	}
	client, err := s.resolveClient(r.Context(), clientID)
	if err != nil {
		writeAuthorizePage(w, http.StatusBadRequest, "invalid_client", err.Error())
		return
	}
	redirectURI := strings.TrimSpace(q.Get("redirect_uri"))
	if redirectURI == "" {
		if len(client.RedirectURIs) != 1 {
			writeAuthorizePage(w, http.StatusBadRequest, "invalid_request", "redirect_uri is required")
			return
		}
		redirectURI = client.RedirectURIs[0]
	}
	if _, ok := client.matchRedirect(redirectURI); !ok {
		writeAuthorizePage(w, http.StatusBadRequest, "invalid_request", "redirect_uri is not registered for this client")
		return
	}
	state := q.Get("state")
	if len(state) > 1024 {
		writeAuthorizePage(w, http.StatusBadRequest, "invalid_request", "state too long")
		return
	}
	// From here on the redirect is trusted: errors go back to the client.
	if q.Get("response_type") != "code" {
		s.redirectError(w, r, redirectURI, state, "unsupported_response_type", "response_type must be code")
		return
	}
	challenge := q.Get("code_challenge")
	if q.Get("code_challenge_method") != "S256" || !validChallenge(challenge) {
		s.redirectError(w, r, redirectURI, state, "invalid_request", "PKCE with code_challenge_method=S256 is required")
		return
	}
	scopes, err := parseScopes(q.Get("scope"))
	if err != nil {
		s.redirectError(w, r, redirectURI, state, "invalid_scope", err.Error())
		return
	}
	resource := strings.TrimSpace(q.Get("resource"))
	if resource != "" && !s.resourceMatches(resource) {
		s.redirectError(w, r, redirectURI, state, "invalid_target", "resource must be "+s.ResourceURL())
		return
	}
	id, err := randomToken("gsreq_")
	if err != nil {
		s.redirectError(w, r, redirectURI, state, "server_error", "entropy unavailable")
		return
	}
	s.mu.Lock()
	s.sweepPendingLocked(now)
	if len(s.pending) >= maxPendingRequests {
		s.mu.Unlock()
		s.redirectError(w, r, redirectURI, state, "temporarily_unavailable", "too many pending authorizations")
		return
	}
	s.pending[id] = &pendingRequest{
		ID: id, Client: client, RedirectURI: redirectURI, State: state,
		CodeChallenge: challenge, Resource: resource, Scopes: scopes,
		Created: now, Expires: now.Add(s.cfg.RequestTTL),
	}
	s.mu.Unlock()
	if !client.CIMD {
		s.clients.touch(client.ID, now)
	}
	// Same-origin relative redirect: the SPA route renders the consent and
	// sends the user to /login first if needed (the router's next= guard).
	http.Redirect(w, r, s.cfg.ConsentPath+"?req="+url.QueryEscape(id), http.StatusFound)
}

// Pending returns the consent view for a request id.
func (s *Server) Pending(id string) (*ConsentView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[id]
	if !ok || !s.now().Before(p.Expires) {
		return nil, ErrNoSuchRequest
	}
	return s.viewLocked(p), nil
}

func (s *Server) viewLocked(p *pendingRequest) *ConsentView {
	u, _ := url.Parse(p.RedirectURI)
	host := ""
	if u != nil {
		host = u.Host
	}
	return &ConsentView{
		RequestID:    p.ID,
		ClientID:     p.Client.ID,
		ClientName:   p.Client.Name,
		ClientCIMD:   p.Client.CIMD,
		RedirectURI:  p.RedirectURI,
		RedirectHost: host,
		LoopbackOnly: p.Client.LoopbackOnly(),
		Scopes:       append([]string(nil), p.Scopes...),
		ExpiresAt:    p.Expires,
	}
}

// Approve records the user's decision, mints the authorization code and
// returns the redirect URL to send the browser to. scopes must be a subset
// of the requested ones (the caller has already applied role limits);
// projects is the scope list of the future grant (nil = admin, all).
func (s *Server) Approve(id, userID, username string, projects, scopes []string) (string, error) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[id]
	if !ok || !now.Before(p.Expires) {
		return "", ErrNoSuchRequest
	}
	if userID == "" {
		return "", errors.New("user required")
	}
	granted, err := grantedScopes(p.Scopes, scopes)
	if err != nil {
		return "", err
	}
	code, err := randomToken("gsac_")
	if err != nil {
		return "", err
	}
	s.sweepCodesLocked(now)
	if len(s.codes) >= maxCodes {
		return "", errors.New("too many pending codes")
	}
	s.codes[hashHex(code)] = &authCode{
		Client: p.Client, RedirectURI: p.RedirectURI, CodeChallenge: p.CodeChallenge,
		Resource: p.Resource, UserID: userID, Username: username,
		Projects: append([]string(nil), projects...), Scopes: granted,
		Expires: now.Add(s.cfg.CodeTTL),
	}
	delete(s.pending, id)
	params := url.Values{"code": {code}}
	if p.State != "" {
		params.Set("state", p.State)
	}
	return appendParams(p.RedirectURI, params), nil
}

// Deny discards the request and returns the redirect URL carrying
// error=access_denied.
func (s *Server) Deny(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[id]
	if !ok {
		return "", ErrNoSuchRequest
	}
	delete(s.pending, id)
	params := url.Values{"error": {"access_denied"}, "error_description": {"the user denied the request"}}
	if p.State != "" {
		params.Set("state", p.State)
	}
	return appendParams(p.RedirectURI, params), nil
}

func (s *Server) sweepPendingLocked(now time.Time) {
	for id, p := range s.pending {
		if !now.Before(p.Expires) {
			delete(s.pending, id)
		}
	}
}

// resourceMatches compares a resource indicator with the canonical MCP URL,
// tolerating case in scheme/host and a trailing slash.
func (s *Server) resourceMatches(resource string) bool {
	want, _ := url.Parse(s.ResourceURL())
	got, err := url.Parse(resource)
	if err != nil || want == nil {
		return false
	}
	return strings.EqualFold(got.Scheme, want.Scheme) && strings.EqualFold(got.Host, want.Host) &&
		strings.TrimRight(got.Path, "/") == strings.TrimRight(want.Path, "/") &&
		got.RawQuery == "" && got.Fragment == ""
}

// parseScopes validates a space-separated scope list. Empty means the
// default: read and write (the consent screen and the role narrow it).
func parseScopes(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return []string{ScopeRead, ScopeWrite}, nil
	}
	seen := map[string]bool{}
	var out []string
	for _, sc := range strings.Fields(raw) {
		switch sc {
		case ScopeRead, ScopeWrite, ScopeOffline:
		default:
			return nil, fmt.Errorf("unknown scope %q", sc)
		}
		if !seen[sc] {
			seen[sc] = true
			out = append(out, sc)
		}
	}
	return out, nil
}

// grantedScopes intersects the consent decision with the request: the
// result carries the token scopes (read/write) that were both requested and
// granted; offline_access is implicit. At least read must survive.
func grantedScopes(requested, decided []string) ([]string, error) {
	req := map[string]bool{}
	for _, sc := range requested {
		req[sc] = true
	}
	var out []string
	for _, sc := range []string{ScopeRead, ScopeWrite} {
		if !req[sc] && !(sc == ScopeRead && len(requested) == 0) {
			continue
		}
		for _, d := range decided {
			if d == sc {
				out = append(out, sc)
				break
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("at least the read scope must be granted")
	}
	sort.Strings(out)
	return out, nil
}

// validChallenge applies the PKCE length and alphabet rules (RFC 7636 §4.2).
func validChallenge(c string) bool {
	if len(c) < 43 || len(c) > 128 {
		return false
	}
	for _, ch := range c {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9', ch == '-', ch == '_', ch == '.', ch == '~':
		default:
			return false
		}
	}
	return true
}

func appendParams(base string, params url.Values) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	for k, vs := range params {
		for _, v := range vs {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func (s *Server) redirectError(w http.ResponseWriter, r *http.Request, redirectURI, state, code, desc string) {
	params := url.Values{"error": {code}, "error_description": {desc}}
	if state != "" {
		params.Set("state", state)
	}
	http.Redirect(w, r, appendParams(redirectURI, params), http.StatusFound)
}

// writeAuthorizePage answers the user agent directly (no trusted redirect).
func writeAuthorizePage(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	fmt.Fprintf(w, "<!doctype html><title>gosidian — authorization error</title><h1>Authorization request rejected</h1><p><code>%s</code>: %s</p>",
		html.EscapeString(code), html.EscapeString(desc))
}

// writeJSON / writeOAuthError render RFC 6749 style bodies.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeOAuthError(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}
