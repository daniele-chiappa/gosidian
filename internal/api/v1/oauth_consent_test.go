package v1

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/oauth"
)

// oauthFixture wires an OAuth server into the admin fixture the way main does.
func oauthFixture(t *testing.T) (*adminFixture, *oauth.Server, http.Handler) {
	t.Helper()
	f := newAdminFixture(t)
	srv, err := oauth.New(oauth.Config{Issuer: "https://notes.example.com", ClientsPath: filepath.Join(t.TempDir(), "clients.json")}, f.mcpStore, f.auditLog)
	if err != nil {
		t.Fatal(err)
	}
	f.router.deps.OAuth = srv
	for _, p := range []string{"alpha", "beta"} {
		if err := os.MkdirAll(filepath.Join(f.vaultRoot, p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(f.vaultRoot, p, "README.md"), []byte("# "+p+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return f, srv, srv.Handler()
}

func startAuthorize(t *testing.T, h http.Handler) (clientID, reqID, verifier string) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, oauth.PathRegister, strings.NewReader(`{"client_name":"Claude","redirect_uris":["https://claude.ai/api/mcp/auth_callback"]}`))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	var reg struct {
		ClientID string `json:"client_id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &reg)
	if rec.Code != http.StatusCreated || reg.ClientID == "" {
		t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
	}
	verifier = strings.Repeat("k", 64)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	q := url.Values{"response_type": {"code"}, "client_id": {reg.ClientID}, "redirect_uri": {"https://claude.ai/api/mcp/auth_callback"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "scope": {"read write"}, "state": {"s"}}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, oauth.PathAuthorize+"?"+q.Encode(), nil))
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if rec.Code != http.StatusFound || loc.Query().Get("req") == "" {
		t.Fatalf("authorize: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	return reg.ClientID, loc.Query().Get("req"), verifier
}

func TestOAuthConsent_ViewApproveExchange(t *testing.T) {
	f, srv, h := oauthFixture(t)
	clientID, reqID, verifier := startAuthorize(t, h)

	w := f.doAuthRecorder(http.MethodGet, "/api/v1/oauth/requests/"+reqID, "", nil)
	if w.code != http.StatusOK {
		t.Fatalf("view: %d %s", w.code, w.body)
	}
	var view oauthConsentView
	if err := json.Unmarshal([]byte(w.body), &view); err != nil {
		t.Fatal(err)
	}
	if view.ClientName != "Claude" || view.RedirectHost != "claude.ai" || !view.CanWrite || !view.IsOwner || len(view.Projects) != 2 {
		t.Fatalf("view: %+v", view)
	}

	w = f.doAuthRecorder(http.MethodPost, "/api/v1/oauth/requests/"+reqID+"/approve", `{"projects":["alpha"],"scopes":["read","write"]}`, nil)
	if w.code != http.StatusOK {
		t.Fatalf("approve: %d %s", w.code, w.body)
	}
	var dec struct {
		RedirectTo string `json:"redirect_to"`
	}
	_ = json.Unmarshal([]byte(w.body), &dec)
	ru, _ := url.Parse(dec.RedirectTo)
	code := ru.Query().Get("code")
	if ru.Host != "claude.ai" || code == "" || ru.Query().Get("state") != "s" {
		t.Fatalf("redirect_to = %q", dec.RedirectTo)
	}
	// The request is consumed.
	if w := f.doAuthRecorder(http.MethodGet, "/api/v1/oauth/requests/"+reqID, "", nil); w.code != http.StatusNotFound {
		t.Errorf("consumed request still viewable: %d", w.code)
	}

	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "client_id": {clientID}, "redirect_uri": {"https://claude.ai/api/mcp/auth_callback"}}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, oauth.PathToken, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rec, req)
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &tok)
	if rec.Code != http.StatusOK || tok.AccessToken == "" {
		t.Fatalf("token: %d %s", rec.Code, rec.Body.String())
	}
	grant, ok := srv.Resolve(tok.AccessToken)
	if !ok || grant.OwnerUserID != f.owner.ID || grant.ScopeLabel() != "alpha" || !grant.HasScope("write") {
		t.Fatalf("grant: %+v ok=%v", grant, ok)
	}
	// The grant is an ordinary token for the admin UI.
	if w := f.doAuthRecorder(http.MethodGet, "/api/v1/admin/tokens", "", nil); !strings.Contains(w.body, `"name":"Claude · oauth"`) {
		t.Errorf("grant not listed in /admin/tokens: %s", w.body)
	}
}

func TestOAuthConsent_GuardsAndDeny(t *testing.T) {
	f, _, h := oauthFixture(t)
	_, reqID, _ := startAuthorize(t, h)

	if w := f.doAuthRecorder(http.MethodGet, "/api/v1/oauth/requests/nope", "", nil); w.code != http.StatusNotFound {
		t.Errorf("unknown request: %d", w.code)
	}
	if w := f.doAuthRecorder(http.MethodPost, "/api/v1/oauth/requests/"+reqID+"/approve", `{"projects":["gamma"]}`, nil); w.code != http.StatusForbidden {
		t.Errorf("invisible project: %d %s", w.code, w.body)
	}
	if w := f.doAuthRecorder(http.MethodPost, "/api/v1/oauth/requests/"+reqID+"/approve", `{"scopes":["admin"]}`, nil); w.code != http.StatusBadRequest {
		t.Errorf("unknown scope: %d %s", w.code, w.body)
	}
	// Unauthenticated callers never see a pending request.
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/oauth/requests/"+reqID, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("anonymous view: %d", rec.Code)
	}
	w := f.doAuthRecorder(http.MethodPost, "/api/v1/oauth/requests/"+reqID+"/deny", "", nil)
	if w.code != http.StatusOK || !strings.Contains(w.body, "access_denied") {
		t.Fatalf("deny: %d %s", w.code, w.body)
	}
	// OAuth off: the consent API is absent.
	f.router.deps.OAuth = nil
	if w := f.doAuthRecorder(http.MethodGet, "/api/v1/oauth/requests/"+reqID, "", nil); w.code != http.StatusNotFound {
		t.Errorf("oauth disabled: %d", w.code)
	}
}
