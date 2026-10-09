package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
)

const issuer = "https://notes.example.com"

func newTestServer(t *testing.T) (*Server, *auth.Store) {
	t.Helper()
	dir := t.TempDir()
	tokens, err := auth.Open(filepath.Join(dir, "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	al, err := audit.Open(filepath.Join(dir, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{Issuer: issuer + "/", ClientsPath: filepath.Join(dir, "oauth_clients.json")}, tokens, al)
	if err != nil {
		t.Fatal(err)
	}
	return s, tokens
}

func pkce(t *testing.T) (verifier, challenge string) {
	t.Helper()
	verifier = strings.Repeat("v", 43) + "abcXYZ-._~"
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func do(h http.Handler, method, target, contentType, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func register(t *testing.T, h http.Handler, redirect string) string {
	t.Helper()
	rec := do(h, http.MethodPost, PathRegister, "application/json",
		`{"client_name":"Test Client","redirect_uris":["`+redirect+`"],"token_endpoint_auth_method":"none"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
	}
	var resp registrationResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(resp.ClientID, clientPrefix) || resp.TokenEndpointAuthMethod != "none" {
		t.Fatalf("registration response: %+v", resp)
	}
	return resp.ClientID
}

// authorize runs the authorize request and returns the pending request id.
func authorize(t *testing.T, h http.Handler, params url.Values) string {
	t.Helper()
	rec := do(h, http.MethodGet, PathAuthorize+"?"+params.Encode(), "", "")
	if rec.Code != http.StatusFound {
		t.Fatalf("authorize: %d %s", rec.Code, rec.Body.String())
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if loc.Path != "/oauth/consent" || loc.Query().Get("req") == "" {
		t.Fatalf("authorize redirect = %q", rec.Header().Get("Location"))
	}
	return loc.Query().Get("req")
}

func tokenRequest(t *testing.T, h http.Handler, form url.Values) (int, map[string]any) {
	t.Helper()
	rec := do(h, http.MethodPost, PathToken, "application/x-www-form-urlencoded", form.Encode())
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func TestMetadataDocuments(t *testing.T) {
	s, _ := newTestServer(t)
	h := s.Handler()
	rec := do(h, http.MethodGet, WellKnownAuthorizationServer, "", "")
	if rec.Code != 200 {
		t.Fatalf("AS metadata: %d", rec.Code)
	}
	var as asMetadata
	_ = json.Unmarshal(rec.Body.Bytes(), &as)
	if as.Issuer != issuer || as.TokenEndpoint != issuer+PathToken || !as.ClientIDMetadataDocumentSupported ||
		len(as.CodeChallengeMethodsSupported) != 1 || as.CodeChallengeMethodsSupported[0] != "S256" ||
		as.TokenEndpointAuthMethodsSupported[0] != "none" {
		t.Errorf("AS metadata: %+v", as)
	}
	for _, p := range []string{WellKnownProtectedResource, WellKnownProtectedResource + "/mcp"} {
		rec = do(h, http.MethodGet, p, "", "")
		var pr prMetadata
		_ = json.Unmarshal(rec.Body.Bytes(), &pr)
		if rec.Code != 200 || pr.Resource != issuer+"/mcp" || pr.AuthorizationServers[0] != issuer {
			t.Errorf("PRM %s: %d %+v", p, rec.Code, pr)
		}
	}
	if s.ResourceMetadataURL() != issuer+"/.well-known/oauth-protected-resource/mcp" {
		t.Errorf("resource metadata URL = %q", s.ResourceMetadataURL())
	}
	if !strings.Contains(s.Challenge(), `resource_metadata="`+issuer) || !strings.Contains(s.Challenge(), `scope="read write"`) {
		t.Errorf("challenge = %q", s.Challenge())
	}
	if rec := do(h, http.MethodOptions, WellKnownAuthorizationServer, "", ""); rec.Code != http.StatusNoContent {
		t.Errorf("preflight: %d", rec.Code)
	}
}

func TestRegisterValidation(t *testing.T) {
	s, _ := newTestServer(t)
	h := s.Handler()
	cases := map[string]string{
		"no redirects":      `{"client_name":"x"}`,
		"http non-loopback": `{"redirect_uris":["http://example.com/cb"]}`,
		"fragment":          `{"redirect_uris":["https://example.com/cb#x"]}`,
		"confidential":      `{"redirect_uris":["https://example.com/cb"],"token_endpoint_auth_method":"client_secret_basic"}`,
		"bad grant":         `{"redirect_uris":["https://example.com/cb"],"grant_types":["implicit"]}`,
		"not json":          `nope`,
	}
	for name, body := range cases {
		if rec := do(h, http.MethodPost, PathRegister, "application/json", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", name, rec.Code, rec.Body.String())
		}
	}
	// Loopback http and https are fine; the client persists across a reopen.
	id := register(t, h, "http://127.0.0.1/callback")
	s2, err := New(Config{Issuer: issuer, ClientsPath: s.cfg.ClientsPath}, s.tokens, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := s2.clients.get(id); !ok || c.Name != "Test Client" {
		t.Errorf("client not persisted: %v %+v", ok, c)
	}
}

func TestRegisterCapEvictsIdle(t *testing.T) {
	s, tokens := newTestServer(t)
	s.clients.max = 2
	s.clients.idleTTL = time.Hour
	h := s.Handler()
	base := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return base }
	a := register(t, h, "https://a.example/cb")
	s.now = func() time.Time { return base.Add(time.Minute) }
	b := register(t, h, "https://b.example/cb")
	// At the cap with nothing idle the least recent goes: refusing let
	// anyone lock new connectors out (BUG-111, S1-7).
	s.now = func() time.Time { return base.Add(2 * time.Minute) }
	c := register(t, h, "https://c.example/cb")
	if _, ok := s.clients.get(a); ok {
		t.Error("the least recently used client was kept")
	}
	if _, ok := s.clients.get(b); !ok {
		t.Error("a more recent client was evicted")
	}
	s.now = func() time.Time { return base.Add(3 * time.Hour) }
	d := register(t, h, "https://d.example/cb") // idle ones evicted
	if _, ok := s.clients.get(c); ok {
		t.Error("an idle client was kept")
	}
	// A client someone authorized holds a grant: never evicted, and with
	// every client holding one the registration is refused.
	s.now = func() time.Time { return base.Add(3*time.Hour + time.Minute) }
	e := register(t, h, "https://e.example/cb")
	for _, id := range []string{d, e} {
		if _, err := tokens.CreateGrant("g-"+id, []string{"p"}, []string{"read"}, 0, "u1", id); err != nil {
			t.Fatal(err)
		}
	}
	if rec := do(h, http.MethodPost, PathRegister, "application/json", `{"redirect_uris":["https://f.example/cb"]}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("at the cap with every client granted: %d, want 503", rec.Code)
	}
	if _, ok := s.clients.get(d); !ok {
		t.Error("a client with a grant was evicted")
	}
}

// Pending authorizations at the cap make room by dropping the oldest of the
// address with the most, so a flood evicts its own and not a consent in
// progress; the rate limits count an IPv6 /64 as one address (S1-7).
func TestPendingCapAndIPv6Limit(t *testing.T) {
	s, _ := newTestServer(t)
	h := s.Handler()
	clientID := register(t, h, "https://claude.ai/api/mcp/auth_callback")
	_, challenge := pkce(t)
	base := time.Now()
	s.mu.Lock()
	for i := 0; i < maxPendingRequests; i++ {
		id, from := fmt.Sprintf("old-%d", i), "198.51.100.9"
		if i == 0 {
			from = "203.0.113.5" // a user mid-consent, the oldest of all
		}
		s.pending[id] = &pendingRequest{ID: id, Created: base.Add(time.Duration(i) * time.Millisecond), Expires: base.Add(time.Hour), From: from}
	}
	s.mu.Unlock()
	authorize(t, h, url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {"https://claude.ai/api/mcp/auth_callback"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}})
	s.mu.Lock()
	_, first := s.pending["old-0"]
	_, second := s.pending["old-1"]
	n := len(s.pending)
	s.mu.Unlock()
	if !first || second || n != maxPendingRequests {
		t.Errorf("after an authorize at the cap: the user's old-0 kept=%v, the flood's old-1 kept=%v, %d pending", first, second, n)
	}

	req := func(addr string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = addr
		return r
	}
	if a, b := s.limitKey(req("[2001:db8:1:2::1]:4000")), s.limitKey(req("[2001:db8:1:2:ffff::9]:4001")); a != b {
		t.Errorf("one /64 counts as two addresses: %q, %q", a, b)
	}
	if a, b := s.limitKey(req("[2001:db8:1:2::1]:4000")), s.limitKey(req("[2001:db8:1:3::1]:4000")); a == b {
		t.Errorf("two /64 count as one: %q", a)
	}
	if got := s.limitKey(req("192.0.2.7:5000")); got != "192.0.2.7" {
		t.Errorf("IPv4 key = %q", got)
	}
}

func TestFullFlowAndRotation(t *testing.T) {
	s, tokens := newTestServer(t)
	h := s.Handler()
	clientID := register(t, h, "https://claude.ai/api/mcp/auth_callback")
	verifier, challenge := pkce(t)

	reqID := authorize(t, h, url.Values{
		"response_type": {"code"}, "client_id": {clientID},
		"redirect_uri": {"https://claude.ai/api/mcp/auth_callback"}, "state": {"xyz"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"},
		"scope": {"read write offline_access"}, "resource": {issuer + "/mcp"},
	})
	view, err := s.Pending(reqID)
	if err != nil || view.ClientName != "Test Client" || view.RedirectHost != "claude.ai" || view.LoopbackOnly {
		t.Fatalf("pending view: %+v %v", view, err)
	}
	// Consent grants read+write on one project.
	redirect, err := s.Approve(reqID, "u1", "alice", []string{"proj"}, []string{"read", "write"})
	if err != nil {
		t.Fatal(err)
	}
	ru, _ := url.Parse(redirect)
	code := ru.Query().Get("code")
	if ru.Host != "claude.ai" || code == "" || ru.Query().Get("state") != "xyz" {
		t.Fatalf("approve redirect = %q", redirect)
	}
	if _, err := s.Pending(reqID); err == nil {
		t.Error("pending request survived approval")
	}

	// Wrong verifier is refused and burns the code.
	status, body := tokenRequest(t, h, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {strings.Repeat("x", 50)}, "client_id": {clientID}, "redirect_uri": {"https://claude.ai/api/mcp/auth_callback"}})
	if status != 400 || body["error"] != "invalid_grant" {
		t.Fatalf("bad verifier: %d %v", status, body)
	}
	status, body = tokenRequest(t, h, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "client_id": {clientID}})
	if status != 400 {
		t.Fatalf("burnt code reused: %d %v", status, body)
	}

	// Fresh consent, correct exchange.
	reqID = authorize(t, h, url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {"https://claude.ai/api/mcp/auth_callback"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}})
	redirect, _ = s.Approve(reqID, "u1", "alice", []string{"proj"}, []string{"read", "write"})
	ru, _ = url.Parse(redirect)
	code = ru.Query().Get("code")
	status, body = tokenRequest(t, h, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "client_id": {clientID}, "redirect_uri": {"https://claude.ai/api/mcp/auth_callback"}, "resource": {issuer + "/mcp/"}})
	if status != 200 {
		t.Fatalf("exchange: %d %v", status, body)
	}
	access, _ := body["access_token"].(string)
	refresh, _ := body["refresh_token"].(string)
	if !strings.HasPrefix(access, accessPrefix) || !strings.HasPrefix(refresh, refreshPrefix) || body["token_type"] != "Bearer" || body["scope"] != "read write offline_access" {
		t.Fatalf("token response: %v", body)
	}

	grant, ok := s.Resolve(access)
	if !ok || !grant.IsOAuthGrant() || grant.OwnerUserID != "u1" || grant.ClientID != clientID ||
		grant.ScopeLabel() != "proj" || !grant.HasScope("write") || !strings.Contains(grant.Name, "Test Client") {
		t.Fatalf("resolved grant: %+v ok=%v", grant, ok)
	}
	if _, ok := s.Resolve("gsa_nope"); ok {
		t.Error("unknown access token resolved")
	}
	if tok, err := tokens.Validate(access); err == nil {
		t.Errorf("access token must not validate as a static bearer: %+v", tok)
	}
	if listed := tokens.List(); len(listed) != 1 || listed[0].Kind != auth.KindOAuth || listed[0].RefreshHash == "" {
		t.Fatalf("grant record: %+v", listed)
	}

	// Refresh rotates; the old access token keeps working until it expires,
	// the old refresh token is dead.
	status, body2 := tokenRequest(t, h, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID}})
	if status != 200 {
		t.Fatalf("refresh: %d %v", status, body2)
	}
	refresh2, _ := body2["refresh_token"].(string)
	access2, _ := body2["access_token"].(string)
	if refresh2 == refresh || access2 == access {
		t.Fatal("refresh did not rotate")
	}
	if _, ok := s.Resolve(access2); !ok {
		t.Error("new access token does not resolve")
	}
	// Reuse of the rotated refresh token revokes the grant entirely.
	status, body3 := tokenRequest(t, h, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID}})
	if status != 400 || body3["error"] != "invalid_grant" {
		t.Fatalf("reuse: %d %v", status, body3)
	}
	if _, ok := tokens.ByID(grant.ID); ok {
		t.Error("grant survived refresh-token reuse")
	}
	if _, ok := s.Resolve(access2); ok {
		t.Error("access token survived grant revocation")
	}
	if status, _ := tokenRequest(t, h, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh2}, "client_id": {clientID}}); status != 400 {
		t.Errorf("refresh after revocation: %d", status)
	}
}

// grantWithRefresh runs a consent and the code exchange, and returns the
// client id and the refresh token.
func grantWithRefresh(t *testing.T, s *Server, h http.Handler) (string, string) {
	t.Helper()
	const cb = "https://claude.ai/api/mcp/auth_callback"
	clientID := register(t, h, cb)
	verifier, challenge := pkce(t)
	reqID := authorize(t, h, url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {cb}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}})
	redirect, err := s.Approve(reqID, "u1", "alice", []string{"proj"}, []string{"read"})
	if err != nil {
		t.Fatal(err)
	}
	ru, _ := url.Parse(redirect)
	status, body := tokenRequest(t, h, url.Values{"grant_type": {"authorization_code"}, "code": {ru.Query().Get("code")}, "code_verifier": {verifier}, "client_id": {clientID}, "redirect_uri": {cb}})
	if status != 200 {
		t.Fatalf("exchange: %d %v", status, body)
	}
	refresh, _ := body["refresh_token"].(string)
	return clientID, refresh
}

// A refresh that fails on the server's side (here the cap on live access
// tokens) rotates nothing: the client retries with the same token, and that
// is not taken for a reuse (BUG-111, S1-6).
func TestRefreshFailureKeepsTheToken(t *testing.T) {
	s, tokens := newTestServer(t)
	h := s.Handler()
	clientID, refresh := grantWithRefresh(t, s, h)
	later := time.Now().Add(time.Hour)
	s.mu.Lock()
	for i := len(s.access); i < maxAccessEntries; i++ {
		s.access[fmt.Sprintf("filler-%d", i)] = accessEntry{tokenID: "x", exp: later}
	}
	s.mu.Unlock()
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID}}
	if status, body := tokenRequest(t, h, form); status != http.StatusInternalServerError {
		t.Fatalf("refresh at the cap: %d %v", status, body)
	}
	s.mu.Lock()
	for k := range s.access {
		if strings.HasPrefix(k, "filler-") {
			delete(s.access, k)
		}
	}
	s.mu.Unlock()
	if status, body := tokenRequest(t, h, form); status != 200 {
		t.Fatalf("retry with the same token: %d %v", status, body)
	}
	if len(tokens.List()) != 1 {
		t.Error("the retry revoked the grant")
	}
}

// One refresh token presented by several requests at once: at most one gets
// new tokens, and the reuse revokes the grant, whatever the interleaving
// (BUG-111, S1-8).
func TestConcurrentRefreshIsAReuse(t *testing.T) {
	s, tokens := newTestServer(t)
	h := s.Handler()
	clientID, refresh := grantWithRefresh(t, s, h)
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID}}
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if status, _ := tokenRequest(t, h, form); status == 200 {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	if ok > 1 {
		t.Errorf("%d refreshes with the same token succeeded", ok)
	}
	if len(tokens.List()) != 0 {
		t.Error("the grant survived a refresh token used several times")
	}
}

func TestRevokeEndpointAndAdminRevocation(t *testing.T) {
	s, tokens := newTestServer(t)
	h := s.Handler()
	clientID := register(t, h, "https://app.example/cb")
	verifier, challenge := pkce(t)
	reqID := authorize(t, h, url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {"https://app.example/cb"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}})
	redirect, _ := s.Approve(reqID, "u1", "alice", nil, []string{"read"})
	ru, _ := url.Parse(redirect)
	_, body := tokenRequest(t, h, url.Values{"grant_type": {"authorization_code"}, "code": {ru.Query().Get("code")}, "code_verifier": {verifier}, "client_id": {clientID}})
	access, _ := body["access_token"].(string)
	refresh, _ := body["refresh_token"].(string)
	if body["scope"] != "read offline_access" {
		t.Errorf("read-only consent scope = %v", body["scope"])
	}
	grant, _ := s.Resolve(access)
	if grant == nil || !grant.IsAdmin() || grant.HasScope("write") {
		t.Fatalf("grant = %+v", grant)
	}
	// Admin revocation (what /admin/tokens does) kills the access token.
	if err := tokens.Revoke(grant.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Resolve(access); ok {
		t.Error("access token survived admin revocation")
	}
	// RFC 7009 on an already-revoked refresh token still answers 200.
	if rec := do(h, http.MethodPost, PathRevoke, "application/x-www-form-urlencoded", url.Values{"token": {refresh}, "client_id": {clientID}}.Encode()); rec.Code != 200 {
		t.Errorf("revoke: %d", rec.Code)
	}
}

func TestAuthorizeErrors(t *testing.T) {
	s, _ := newTestServer(t)
	h := s.Handler()
	clientID := register(t, h, "https://app.example/cb")
	_, challenge := pkce(t)
	// Unknown client and unregistered redirect: answered directly, never redirected.
	if rec := do(h, http.MethodGet, PathAuthorize+"?client_id=gsc_nope&redirect_uri=https://evil.example/cb&response_type=code", "", ""); rec.Code != 400 {
		t.Errorf("unknown client: %d", rec.Code)
	}
	if rec := do(h, http.MethodGet, PathAuthorize+"?client_id="+clientID+"&redirect_uri=https://evil.example/cb&response_type=code", "", ""); rec.Code != 400 {
		t.Errorf("unregistered redirect: %d", rec.Code)
	}
	// Missing PKCE, wrong resource, unknown scope: redirected with an error.
	for name, extra := range map[string]url.Values{
		"no pkce":       {},
		"plain pkce":    {"code_challenge": {challenge}, "code_challenge_method": {"plain"}},
		"resource":      {"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "resource": {"https://other.example/mcp"}},
		"scope":         {"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "scope": {"admin"}},
		"response_type": {"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "response_type": {"token"}},
	} {
		params := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {"https://app.example/cb"}, "state": {"s1"}}
		for k, v := range extra {
			params[k] = v
		}
		rec := do(h, http.MethodGet, PathAuthorize+"?"+params.Encode(), "", "")
		loc, _ := url.Parse(rec.Header().Get("Location"))
		if rec.Code != http.StatusFound || loc.Host != "app.example" || loc.Query().Get("error") == "" || loc.Query().Get("state") != "s1" {
			t.Errorf("%s: %d %q", name, rec.Code, rec.Header().Get("Location"))
		}
	}
	// Deny.
	reqID := authorize(t, h, url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {"https://app.example/cb"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}, "state": {"d"}})
	redirect, err := s.Deny(reqID)
	if err != nil || !strings.Contains(redirect, "error=access_denied") || !strings.Contains(redirect, "state=d") {
		t.Errorf("deny: %q %v", redirect, err)
	}
	// Expired pending request.
	reqID = authorize(t, h, url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {"https://app.example/cb"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}})
	s.now = func() time.Time { return time.Now().Add(time.Hour) }
	if _, err := s.Pending(reqID); err == nil {
		t.Error("expired request still pending")
	}
}

func TestCIMDClientAndLoopbackRedirect(t *testing.T) {
	s, _ := newTestServer(t)
	h := s.Handler()
	docURL := "https://claude.ai/oauth/claude-code-client-metadata"
	fetched := 0
	s.cimd.fetch = func(_ context.Context, u string) ([]byte, http.Header, error) {
		fetched++
		if u != docURL {
			t.Errorf("fetch %q", u)
		}
		doc := `{"client_id":"` + docURL + `","client_name":"Claude Code","redirect_uris":["http://localhost/callback","http://127.0.0.1/callback"],"token_endpoint_auth_method":"none"}`
		return []byte(doc), http.Header{"Cache-Control": {"max-age=600"}}, nil
	}
	verifier, challenge := pkce(t)
	// Ephemeral port on loopback is matched port-agnostically.
	reqID := authorize(t, h, url.Values{"response_type": {"code"}, "client_id": {docURL}, "redirect_uri": {"http://localhost:3118/callback"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}})
	view, _ := s.Pending(reqID)
	if view == nil || !view.ClientCIMD || !view.LoopbackOnly || view.ClientName != "Claude Code" {
		t.Fatalf("view = %+v", view)
	}
	redirect, err := s.Approve(reqID, "u1", "alice", []string{"p"}, []string{"read", "write"})
	if err != nil || !strings.HasPrefix(redirect, "http://localhost:3118/callback?code=") {
		t.Fatalf("approve: %q %v", redirect, err)
	}
	ru, _ := url.Parse(redirect)
	status, body := tokenRequest(t, h, url.Values{"grant_type": {"authorization_code"}, "code": {ru.Query().Get("code")}, "code_verifier": {verifier}, "client_id": {docURL}, "redirect_uri": {"http://localhost:3118/callback"}})
	if status != 200 {
		t.Fatalf("exchange: %d %v", status, body)
	}
	// Second authorize hits the cache.
	authorize(t, h, url.Values{"response_type": {"code"}, "client_id": {docURL}, "redirect_uri": {"http://127.0.0.1:4000/callback"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}})
	if fetched != 1 {
		t.Errorf("document fetched %d times, want 1 (cached)", fetched)
	}
	// A document whose client_id differs from its URL is rejected.
	s.cimd.fetch = func(_ context.Context, u string) ([]byte, http.Header, error) {
		return []byte(`{"client_id":"https://other/x.json","redirect_uris":["https://a/cb"]}`), nil, nil
	}
	if rec := do(h, http.MethodGet, PathAuthorize+"?client_id=https://evil.example/meta.json&redirect_uri=https://a/cb&response_type=code", "", ""); rec.Code != 400 {
		t.Errorf("mismatched client_id accepted: %d", rec.Code)
	}
}

// allowed_redirect_hosts holds for a client metadata document and for a
// client registered before the list was set: both were checked only by the
// package's rules (BUG-111, S1-5).
func TestAllowedRedirectHostsAtAuthorize(t *testing.T) {
	s, _ := newTestServer(t)
	h := s.Handler()
	_, challenge := pkce(t)
	early := register(t, h, "https://elsewhere.example.org/cb")
	docURL := "https://client.example.org/meta.json"
	s.cimd.fetch = func(_ context.Context, u string) ([]byte, http.Header, error) {
		return []byte(`{"client_id":"` + docURL + `","redirect_uris":["https://evil.example.org/cb","http://localhost/cb"]}`), nil, nil
	}
	s.cfg.AllowedRedirectHosts = []string{"good.example.org"}
	for _, c := range []struct{ client, redirect string }{
		{early, "https://elsewhere.example.org/cb"},
		{docURL, "https://evil.example.org/cb"},
	} {
		q := url.Values{"response_type": {"code"}, "client_id": {c.client}, "redirect_uri": {c.redirect}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
		if rec := do(h, http.MethodGet, PathAuthorize+"?"+q.Encode(), "", ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%s → %s = %d, want 400", c.client, c.redirect, rec.Code)
		}
	}
	// Loopback stays allowed, as at registration.
	authorize(t, h, url.Values{"response_type": {"code"}, "client_id": {docURL}, "redirect_uri": {"http://localhost:5000/cb"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}})
}

func TestSSRFGuard(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "192.168.1.1", "172.20.3.4", "169.254.169.254", "100.64.1.1", "0.0.0.0", "::1", "fe80::1", "fc00::1", "224.0.0.1"} {
		if !isDisallowedIP(net.ParseIP(ip)) {
			t.Errorf("%s should be refused", ip)
		}
	}
	for _, ip := range []string{"8.8.8.8", "160.79.104.10", "2606:4700::1111"} {
		if isDisallowedIP(net.ParseIP(ip)) {
			t.Errorf("%s should be allowed", ip)
		}
	}
	// The real fetcher refuses a loopback destination before dialing.
	if _, _, err := fetchCIMD(context.Background(), "https://127.0.0.1:1/client.json"); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("loopback fetch not refused: %v", err)
	}
	if _, _, err := (&cimdCache{fetch: fetchCIMD}).fetchAndParse(context.Background(), "http://example.com/x.json"); err == nil {
		t.Error("http client_id accepted")
	}
}

func TestRedirectRulesAndHelpers(t *testing.T) {
	if !redirectMatches("http://localhost/cb", "http://localhost:51234/cb") || !redirectMatches("http://127.0.0.1/cb", "http://127.0.0.1:9/cb") {
		t.Error("loopback port should be ignored")
	}
	if redirectMatches("https://app.example/cb", "https://app.example:8443/cb") || redirectMatches("http://localhost/cb", "http://localhost/other") {
		t.Error("non-loopback port or path difference must not match")
	}
	if cacheTTL(http.Header{"Cache-Control": {"max-age=5"}}) != cimdMinTTL || cacheTTL(http.Header{"Cache-Control": {"max-age=999999"}}) != cimdMaxTTL || cacheTTL(nil) != cimdDefaultTTL {
		t.Error("cache TTL clamping")
	}
	if got, _ := grantedScopes([]string{"read", "write"}, []string{"read"}); strings.Join(got, " ") != "read" {
		t.Errorf("granted = %v", got)
	}
	if _, err := grantedScopes([]string{"read"}, []string{"write"}); err == nil {
		t.Error("write not requested must not be granted; read missing must error")
	}
	if _, err := parseIssuer("notes.example.com"); err == nil {
		t.Error("issuer without scheme accepted")
	}
	if u, err := parseIssuer("https://notes.example.com/"); err != nil || u.String() != "https://notes.example.com" {
		t.Errorf("issuer normalization: %v %v", u, err)
	}
	// A path broke the flow halfway: refused at start (IMP-159, S1-13).
	if _, err := parseIssuer("https://notes.example.com/base/"); err == nil || !strings.Contains(err.Error(), "has a path") {
		t.Errorf("issuer with a path: %v", err)
	}
	s, _ := newTestServer(t)
	if !s.resourceMatches("HTTPS://NOTES.EXAMPLE.COM/mcp/") || s.resourceMatches(issuer+"/mcp?x=1") || s.resourceMatches(issuer) {
		t.Error("resource matching")
	}
}
