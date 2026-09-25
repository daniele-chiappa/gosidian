package oauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
)

// authCode is a consent turned into a single-use authorization code.
type authCode struct {
	Client        *Client
	RedirectURI   string
	CodeChallenge string
	Resource      string
	UserID        string
	Username      string
	Projects      []string
	Scopes        []string // read/write only
	Expires       time.Time
}

type accessEntry struct {
	tokenID string
	exp     time.Time
}

type consumedRefresh struct {
	tokenID string
	exp     time.Time
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token,omitempty"`
	Scope        string `json:"scope"`
}

const maxTokenBody = 16 << 10

// handleToken implements the authorization_code and refresh_token grants
// for public clients (client_id in the body, no secret).
func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeOAuthError(w, http.StatusMethodNotAllowed, "invalid_request", "POST required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxTokenBody)
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "body must be application/x-www-form-urlencoded")
		return
	}
	now := s.now()
	if !s.limiter.allow("token:"+s.cfg.ClientIP(r), now) {
		writeOAuthError(w, http.StatusTooManyRequests, "invalid_request", "too many token requests from this address, retry later")
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		s.exchangeCode(w, r, now)
	case "refresh_token":
		s.refreshGrant(w, r, now)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
	}
}

func (s *Server) exchangeCode(w http.ResponseWriter, r *http.Request, now time.Time) {
	f := r.PostForm
	code := f.Get("code")
	verifier := f.Get("code_verifier")
	clientID := strings.TrimSpace(f.Get("client_id"))
	if code == "" || verifier == "" || clientID == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "code, code_verifier and client_id are required")
		return
	}
	s.mu.Lock()
	key := hashHex(code)
	ac, ok := s.codes[key]
	if ok {
		delete(s.codes, key) // single use, whatever happens next
	}
	s.mu.Unlock()
	if !ok || !now.Before(ac.Expires) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "authorization code is unknown, used or expired")
		return
	}
	if ac.Client.ID != clientID {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "code was issued to another client")
		return
	}
	if ru := f.Get("redirect_uri"); ru != "" && ru != ac.RedirectURI {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri does not match the authorization request")
		return
	}
	if !verifyPKCE(verifier, ac.CodeChallenge) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}
	if res := strings.TrimSpace(f.Get("resource")); res != "" && !s.resourceMatches(res) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_target", "resource must be "+s.ResourceURL())
		return
	}
	name := ac.Client.Name + " · oauth"
	grant, err := s.tokens.CreateGrant(name, ac.Projects, ac.Scopes, s.cfg.RefreshTTL, ac.UserID, ac.Client.ID)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", "could not create grant: "+err.Error())
		return
	}
	resp, err := s.issueTokens(&grant, now)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	s.auditWrite(audit.ActionOAuthGrant, ac.UserID, ac.Username, grant.ID, ac.Client.Name)
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) refreshGrant(w http.ResponseWriter, r *http.Request, now time.Time) {
	f := r.PostForm
	refresh := f.Get("refresh_token")
	clientID := strings.TrimSpace(f.Get("client_id"))
	if refresh == "" || clientID == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "refresh_token and client_id are required")
		return
	}
	h := hashHex(refresh)
	grant, ok := s.tokens.ByRefreshHash(h)
	if !ok {
		// A rotated-away refresh token presented again means it leaked (or
		// the client lost the rotation): revoke the whole grant.
		s.mu.Lock()
		c, seen := s.consumed[h]
		s.mu.Unlock()
		if seen {
			_ = s.tokens.Revoke(c.tokenID)
			s.dropAccessFor(c.tokenID)
			s.auditWrite(audit.ActionOAuthRevoke, "", "refresh-reuse", c.tokenID, "refresh token reuse detected")
		}
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token is invalid")
		return
	}
	if grant.ClientID != clientID || !grant.IsOAuthGrant() || grant.Expired() ||
		(!grant.RefreshExpiresAt.IsZero() && !now.Before(grant.RefreshExpiresAt)) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "refresh token is invalid")
		return
	}
	if res := strings.TrimSpace(f.Get("resource")); res != "" && !s.resourceMatches(res) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_target", "resource must be "+s.ResourceURL())
		return
	}
	if sc := f.Get("scope"); sc != "" {
		asked, err := parseScopes(sc)
		if err != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_scope", err.Error())
			return
		}
		for _, a := range asked {
			if a != ScopeOffline && !grant.HasScope(a) {
				writeOAuthError(w, http.StatusBadRequest, "invalid_scope", "scope exceeds the grant")
				return
			}
		}
	}
	s.mu.Lock()
	s.consumed[h] = consumedRefresh{tokenID: grant.ID, exp: grant.RefreshExpiresAt}
	s.mu.Unlock()
	resp, err := s.issueTokens(grant, now)
	if err != nil {
		writeOAuthError(w, http.StatusInternalServerError, "server_error", err.Error())
		return
	}
	s.auditWrite(audit.ActionOAuthRefresh, grant.OwnerUserID, grant.Name, grant.ID, grant.ClientID)
	writeJSON(w, http.StatusOK, resp)
}

// issueTokens mints a fresh access token (memory) and refresh token
// (persisted on the grant, replacing the previous one).
func (s *Server) issueTokens(grant *auth.Token, now time.Time) (*tokenResponse, error) {
	access, err := randomToken(accessPrefix)
	if err != nil {
		return nil, err
	}
	refresh, err := randomToken(refreshPrefix)
	if err != nil {
		return nil, err
	}
	accessExp := now.Add(s.cfg.AccessTTL)
	if !grant.ExpiresAt.IsZero() && grant.ExpiresAt.Before(accessExp) {
		accessExp = grant.ExpiresAt
	}
	refreshExp := now.Add(s.cfg.RefreshTTL)
	if !grant.ExpiresAt.IsZero() && grant.ExpiresAt.Before(refreshExp) {
		refreshExp = grant.ExpiresAt
	}
	if err := s.tokens.SetRefresh(grant.ID, hashHex(refresh), refreshExp); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.issued++
	if s.issued%sweepEvery == 0 || len(s.access) >= maxAccessEntries {
		s.sweepAccessLocked(now)
	}
	if len(s.access) >= maxAccessEntries {
		s.mu.Unlock()
		return nil, errors.New("too many live access tokens")
	}
	s.access[hashHex(access)] = accessEntry{tokenID: grant.ID, exp: accessExp}
	s.mu.Unlock()
	return &tokenResponse{
		AccessToken:  access,
		TokenType:    "Bearer",
		ExpiresIn:    int64(accessExp.Sub(now).Seconds()),
		RefreshToken: refresh,
		Scope:        strings.Join(append(append([]string(nil), grant.Scopes...), ScopeOffline), " "),
	}, nil
}

// Resolve maps an access token to its grant. It is the hook the MCP
// transports call for bearers that are not static tokens; a revoked or
// expired grant makes every access token die with it.
func (s *Server) Resolve(plaintext string) (*auth.Token, bool) {
	if !strings.HasPrefix(plaintext, accessPrefix) {
		return nil, false
	}
	now := s.now()
	s.mu.Lock()
	e, ok := s.access[hashHex(plaintext)]
	s.mu.Unlock()
	if !ok || !now.Before(e.exp) {
		return nil, false
	}
	grant, ok := s.tokens.ByID(e.tokenID)
	if !ok || !grant.IsOAuthGrant() || grant.Expired() {
		return nil, false
	}
	return grant, true
}

// IsAccessToken reports whether a bearer has the OAuth access-token shape.
func IsAccessToken(plaintext string) bool { return strings.HasPrefix(plaintext, accessPrefix) }

// handleRevoke implements RFC 7009 for public clients: revoking a refresh
// token deletes the grant; revoking an access token drops it. Always 200.
func (s *Server) handleRevoke(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeOAuthError(w, http.StatusMethodNotAllowed, "invalid_request", "POST required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxTokenBody)
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "body must be application/x-www-form-urlencoded")
		return
	}
	now := s.now()
	if !s.limiter.allow("token:"+s.cfg.ClientIP(r), now) {
		writeOAuthError(w, http.StatusTooManyRequests, "invalid_request", "too many requests from this address, retry later")
		return
	}
	tok := r.PostForm.Get("token")
	clientID := strings.TrimSpace(r.PostForm.Get("client_id"))
	switch {
	case strings.HasPrefix(tok, refreshPrefix):
		if grant, ok := s.tokens.ByRefreshHash(hashHex(tok)); ok && (clientID == "" || grant.ClientID == clientID) {
			_ = s.tokens.Revoke(grant.ID)
			s.dropAccessFor(grant.ID)
			s.auditWrite(audit.ActionOAuthRevoke, grant.OwnerUserID, grant.Name, grant.ID, grant.ClientID)
		}
	case strings.HasPrefix(tok, accessPrefix):
		s.mu.Lock()
		delete(s.access, hashHex(tok))
		s.mu.Unlock()
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}

func (s *Server) dropAccessFor(tokenID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, e := range s.access {
		if e.tokenID == tokenID {
			delete(s.access, k)
		}
	}
}

func (s *Server) sweepAccessLocked(now time.Time) {
	for k, e := range s.access {
		if !now.Before(e.exp) {
			delete(s.access, k)
		}
	}
	for k, c := range s.consumed {
		if !c.exp.IsZero() && !now.Before(c.exp) {
			delete(s.consumed, k)
		}
	}
}

func (s *Server) sweepCodesLocked(now time.Time) {
	for k, c := range s.codes {
		if !now.Before(c.Expires) {
			delete(s.codes, k)
		}
	}
}

// verifyPKCE checks base64url(sha256(verifier)) == challenge (S256).
func verifyPKCE(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(got), []byte(challenge)) == 1
}
