package v1

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/server/events"
	"github.com/gosidian/gosidian/internal/vault"
	"github.com/gosidian/gosidian/internal/webauth"
)

// authFixture spins up a self-contained Router wired to an isolated
// webauth + spa-token store. The vault and audit log point at temp
// dirs so tests can run in parallel without colliding.
type authFixture struct {
	t         *testing.T
	router    *Router
	webauth   *webauth.Store
	spaTokens *auth.SpaTokenStore
	auditLog  *audit.Log
	username  string
	password  string
	owner     *webauth.User
}

func newAuthFixture(t *testing.T) *authFixture {
	t.Helper()
	dir := t.TempDir()

	wa, err := webauth.Open(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	username, password := "owner", "supersecret"
	if _, err := wa.Setup(username, password, false, "test-issuer"); err != nil {
		t.Fatal(err)
	}
	owner := wa.FirstOwner()
	if owner == nil {
		t.Fatal("FirstOwner nil after Setup")
	}

	spa, err := auth.OpenSpaTokens(filepath.Join(dir, "spa_tokens.json"))
	if err != nil {
		t.Fatal(err)
	}

	auditPath := filepath.Join(dir, "audit.jsonl")
	al, err := audit.Open(auditPath)
	if err != nil {
		t.Fatal(err)
	}

	v := vault.New(t.TempDir())
	hub := events.New(events.HubOptions{Logger: slog.Default()})

	router := NewRouter(&Deps{
		Auth: &AuthDeps{
			WebAuth: wa,
			SpaAuth: spa,
			Logger:  slog.Default(),
		},
		Audit:  al,
		Vault:  v,
		Events: hub,
	})

	return &authFixture{
		t:         t,
		router:    router,
		webauth:   wa,
		spaTokens: spa,
		auditLog:  al,
		username:  username,
		password:  password,
		owner:     owner,
	}
}

func (f *authFixture) request(method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	f.t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, r)
	return w
}

func decodeJSON[T any](t *testing.T, body io.Reader) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(body).Decode(&v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return v
}

func TestLogin_Success(t *testing.T) {
	f := newAuthFixture(t)
	body := `{"username":"owner","password":"supersecret"}`
	w := f.request(http.MethodPost, "/api/v1/login", body, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	res := decodeJSON[loginResponse](t, w.Body)
	if !strings.HasPrefix(res.Token, "gsp_") {
		t.Errorf("token shape: %q", res.Token)
	}
	if res.User.Username != "owner" || res.User.Role != "owner" {
		t.Errorf("user view: %+v", res.User)
	}
	if res.ExpiresAt == "" || res.HardExpiry == "" {
		t.Errorf("missing timestamps: %+v", res)
	}
	// Token should be immediately usable
	w2 := f.request(http.MethodGet, "/api/v1/me", "", map[string]string{
		"Authorization": "Bearer " + res.Token,
	})
	if w2.Code != http.StatusOK {
		t.Errorf("/me failed: %d %s", w2.Code, w2.Body.String())
	}
}

func TestLogin_InvalidPassword(t *testing.T) {
	f := newAuthFixture(t)
	body := `{"username":"owner","password":"wrong"}`
	w := f.request(http.MethodPost, "/api/v1/login", body, nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", w.Code)
	}
	if !strings.Contains(w.Body.String(), CodeAuthInvalidCredentials) {
		t.Errorf("error code missing: %s", w.Body.String())
	}
}

func TestLogin_InvalidUsername(t *testing.T) {
	f := newAuthFixture(t)
	body := `{"username":"ghost","password":"whatever"}`
	w := f.request(http.MethodPost, "/api/v1/login", body, nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", w.Code)
	}
}

func TestLogin_MissingFields(t *testing.T) {
	f := newAuthFixture(t)
	w := f.request(http.MethodPost, "/api/v1/login", `{"username":"owner"}`, nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", w.Code)
	}
}

func TestLogin_MalformedJSON(t *testing.T) {
	f := newAuthFixture(t)
	w := f.request(http.MethodPost, "/api/v1/login", `{`, nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400", w.Code)
	}
}

func TestLogin_GetMethodNotAllowed(t *testing.T) {
	f := newAuthFixture(t)
	w := f.request(http.MethodGet, "/api/v1/login", "", nil)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status=%d, want 405", w.Code)
	}
}

func TestLogout_RevokesToken(t *testing.T) {
	f := newAuthFixture(t)
	body := `{"username":"owner","password":"supersecret"}`
	loginW := f.request(http.MethodPost, "/api/v1/login", body, nil)
	res := decodeJSON[loginResponse](t, loginW.Body)

	w := f.request(http.MethodPost, "/api/v1/logout", "", map[string]string{
		"Authorization": "Bearer " + res.Token,
	})
	if w.Code != http.StatusNoContent {
		t.Errorf("status=%d body=%s", w.Code, w.Body.String())
	}

	// Token must now be invalid
	w2 := f.request(http.MethodGet, "/api/v1/me", "", map[string]string{
		"Authorization": "Bearer " + res.Token,
	})
	if w2.Code != http.StatusUnauthorized {
		t.Errorf("token still valid after logout: %d", w2.Code)
	}
}

func TestMe_RequiresAuth(t *testing.T) {
	f := newAuthFixture(t)
	w := f.request(http.MethodGet, "/api/v1/me", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", w.Code)
	}
}

func TestRefresh_ExtendsTTL(t *testing.T) {
	f := newAuthFixture(t)
	loginW := f.request(http.MethodPost, "/api/v1/login", `{"username":"owner","password":"supersecret"}`, nil)
	res := decodeJSON[loginResponse](t, loginW.Body)
	originalExpiry := res.ExpiresAt

	// Refresh
	w := f.request(http.MethodPost, "/api/v1/refresh", "", map[string]string{
		"Authorization": "Bearer " + res.Token,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	refreshed := decodeJSON[refreshResponse](t, w.Body)
	if refreshed.Token != res.Token {
		t.Errorf("refresh rotated token: was %q now %q", res.Token, refreshed.Token)
	}
	if refreshed.ExpiresAt < originalExpiry {
		t.Errorf("refresh did not extend ExpiresAt")
	}
}

func TestRefresh_RequiresAuth(t *testing.T) {
	f := newAuthFixture(t)
	w := f.request(http.MethodPost, "/api/v1/refresh", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", w.Code)
	}
}

func TestSignup_RequiresInvite(t *testing.T) {
	f := newAuthFixture(t)
	body := `{"username":"alice","password":"alice-pass-123","invite":"bogus"}`
	w := f.request(http.MethodPost, "/api/v1/signup", body, nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status=%d, want 400 for unknown invite", w.Code)
	}
}

func TestSignup_ConsumesInviteAndCreatesUser(t *testing.T) {
	f := newAuthFixture(t)
	inv, err := f.webauth.CreateInvite(f.owner.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"username":"alice","password":"alice-pass-123","invite":"` + inv.Token + `"}`
	w := f.request(http.MethodPost, "/api/v1/signup", body, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	created := decodeJSON[userView](t, w.Body)
	if created.Username != "alice" || created.Role != "member" {
		t.Errorf("created user: %+v", created)
	}
	// Invite must be consumed: second signup with the same token fails.
	w2 := f.request(http.MethodPost, "/api/v1/signup", body, nil)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("invite reused: status=%d", w2.Code)
	}
}

func TestSignup_DuplicateUsername(t *testing.T) {
	f := newAuthFixture(t)
	inv, _ := f.webauth.CreateInvite(f.owner.ID, time.Hour)
	body := `{"username":"owner","password":"alice-pass-123","invite":"` + inv.Token + `"}`
	w := f.request(http.MethodPost, "/api/v1/signup", body, nil)
	if w.Code != http.StatusConflict {
		t.Errorf("status=%d, want 409 on duplicate username; body=%s", w.Code, w.Body.String())
	}
}

func TestVersion_PublicGet(t *testing.T) {
	f := newAuthFixture(t)
	Version = "test-version"
	defer func() { Version = "dev" }()
	w := f.request(http.MethodGet, "/api/v1/version", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	v := decodeJSON[versionResponse](t, w.Body)
	if v.Version != "test-version" || v.API != "v1" {
		t.Errorf("version: %+v", v)
	}
}

func TestI18n_DefaultLang(t *testing.T) {
	f := newAuthFixture(t)
	w := f.request(http.MethodGet, "/api/v1/i18n", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"common"`) {
		t.Errorf("expected common section in catalog, got: %s", truncate(w.Body.String(), 200))
	}
}

func TestI18n_ItalianLang(t *testing.T) {
	f := newAuthFixture(t)
	w := f.request(http.MethodGet, "/api/v1/i18n?lang=it", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	// IT catalog has "Salva" under common.save (verified earlier)
	if !strings.Contains(w.Body.String(), `"Salva"`) {
		t.Errorf("expected Salva in IT catalog, got: %s", truncate(w.Body.String(), 200))
	}
}

func TestI18n_ScopeAll(t *testing.T) {
	f := newAuthFixture(t)
	w := f.request(http.MethodGet, "/api/v1/i18n?lang=en&scope=all", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"ui"`) {
		t.Errorf("missing ui scope in merged response: %s", truncate(body, 200))
	}
}

func TestI18n_UnknownScope(t *testing.T) {
	f := newAuthFixture(t)
	w := f.request(http.MethodGet, "/api/v1/i18n?lang=en&scope=mcp", "", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404 for a scope with no catalog", w.Code)
	}
}

func TestI18n_UnknownLang(t *testing.T) {
	f := newAuthFixture(t)
	w := f.request(http.MethodGet, "/api/v1/i18n?lang=zz", "", nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("status=%d, want 404", w.Code)
	}
}

func TestHealth_Public(t *testing.T) {
	f := newAuthFixture(t)
	w := f.request(http.MethodGet, "/api/v1/health", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `"status":"ok"`) {
		t.Errorf("body: %s", w.Body.String())
	}
}

func TestUnauthenticated_Returns401(t *testing.T) {
	f := newAuthFixture(t)
	w := f.request(http.MethodGet, "/api/v1/notes", "", nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status=%d, want 401", w.Code)
	}
}

func TestRevokedTokenReturns401(t *testing.T) {
	f := newAuthFixture(t)
	loginW := f.request(http.MethodPost, "/api/v1/login", `{"username":"owner","password":"supersecret"}`, nil)
	res := decodeJSON[loginResponse](t, loginW.Body)
	if err := f.spaTokens.Revoke(res.Token); err != nil {
		t.Fatal(err)
	}
	w := f.request(http.MethodGet, "/api/v1/me", "", map[string]string{
		"Authorization": "Bearer " + res.Token,
	})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("revoked token still valid: status=%d", w.Code)
	}
}

func TestDisabledUserReturns401(t *testing.T) {
	f := newAuthFixture(t)
	// Add a member, log in, disable, attempt to use the token.
	if _, err := f.webauth.AddUser("bob", "bob-pass-123", webauth.RoleMember); err != nil {
		t.Fatal(err)
	}
	loginW := f.request(http.MethodPost, "/api/v1/login", `{"username":"bob","password":"bob-pass-123"}`, nil)
	if loginW.Code != http.StatusOK {
		t.Fatalf("login bob failed: %d %s", loginW.Code, loginW.Body.String())
	}
	res := decodeJSON[loginResponse](t, loginW.Body)
	if err := f.webauth.DisableUser(res.User.ID); err != nil {
		t.Fatal(err)
	}
	w := f.request(http.MethodGet, "/api/v1/me", "", map[string]string{
		"Authorization": "Bearer " + res.Token,
	})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("disabled user token still valid: status=%d", w.Code)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
