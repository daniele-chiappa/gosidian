package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/i18n"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/vault"
	"github.com/gosidian/gosidian/internal/webauth"
)

// setupAuthedServer mirrors setupServer but wires a webauth store with a
// pre-provisioned account. Returns both the server and the plaintext
// credentials used.
func setupAuthedServer(t *testing.T) (*Server, *webauth.Store, string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.md"), []byte("# Hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := index.Open(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	v := vault.New(dir)
	if err := v.ScanInto(idx); err != nil {
		t.Fatal(err)
	}

	waStore, err := webauth.Open(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	username := "admin"
	password := "supersecret"
	if _, err := waStore.Setup(username, password, false, "Gosidian"); err != nil {
		t.Fatal(err)
	}

	srv := New(v, idx, nil, "", waStore)
	if cat, err := i18n.Load("it"); err == nil {
		srv.SetI18n(cat, "it")
	}
	return srv, waStore, username, password
}

func TestMiddleware_BlocksProtectedWithoutSession(t *testing.T) {
	s, _, _, _ := setupAuthedServer(t)
	w := doReq(t, s, "GET", "/", "", false)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/login") {
		t.Errorf("redirect = %q", loc)
	}
}

func TestMiddleware_AllowsOpenPaths(t *testing.T) {
	s, _, _, _ := setupAuthedServer(t)
	for _, p := range []string{"/healthz", "/login", "/static/css/app.css"} {
		w := doReq(t, s, "GET", p, "", false)
		if w.Code == http.StatusSeeOther {
			t.Errorf("open path %q got redirected", p)
		}
	}
}

func TestLogin_PostValidSetsCookie(t *testing.T) {
	s, _, user, pass := setupAuthedServer(t)

	form := "username=" + user + "&password=" + pass + "&next=/"
	w := doReq(t, s, "POST", "/login", form, false)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("login = %d, body=%s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	var sess *http.Cookie
	for _, c := range cookies {
		if c.Name == webauth.SessionCookieName {
			sess = c
			break
		}
	}
	if sess == nil || sess.Value == "" {
		t.Fatalf("missing session cookie in %+v", cookies)
	}

	// Reuse the cookie on a protected route
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(sess)
	w2 := httptest.NewRecorder()
	s.ServeHTTP(w2, r)
	if w2.Code != 200 {
		t.Errorf("authenticated GET / = %d, body=%s", w2.Code, w2.Body.String())
	}
}

func TestLogin_PostInvalid(t *testing.T) {
	s, _, _, _ := setupAuthedServer(t)
	form := "username=admin&password=wrong"
	w := doReq(t, s, "POST", "/login", form, false)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Credenziali non valide") {
		t.Errorf("missing error message: %s", w.Body.String())
	}
}

func TestLogin_RateLimit(t *testing.T) {
	s, _, _, _ := setupAuthedServer(t)
	form := "username=admin&password=wrong"
	for i := 0; i < loginMaxFailures; i++ {
		w := doReq(t, s, "POST", "/login", form, false)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i, w.Code)
		}
	}
	// Next attempt should be rate limited
	w := doReq(t, s, "POST", "/login", form, false)
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", w.Code)
	}
}

func TestLogout_ClearsSession(t *testing.T) {
	s, waStore, _, _ := setupAuthedServer(t)
	owner := waStore.FirstOwner()
	if owner == nil {
		t.Fatal("owner missing after Setup")
	}
	id, _ := waStore.CreateSession(owner.ID, loginSessionTTL)

	r := httptest.NewRequest("GET", "/logout", nil)
	r.AddCookie(&http.Cookie{Name: webauth.SessionCookieName, Value: id})
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", w.Code)
	}
	if waStore.ValidateSession(id) {
		t.Errorf("session should be invalidated")
	}
}

func TestMiddleware_HTMXReturnsUnauthorized(t *testing.T) {
	s, _, _, _ := setupAuthedServer(t)
	w := doReq(t, s, "GET", "/api/tree", "", true)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for HTMX unauthenticated, got %d", w.Code)
	}
	if loc := w.Header().Get("HX-Redirect"); !strings.HasPrefix(loc, "/login") {
		t.Errorf("HX-Redirect = %q", loc)
	}
}

func TestLogin_SafeNext(t *testing.T) {
	cases := map[string]bool{
		"/notes/x.md": true,
		"/":           true,
		"//evil.com":  false,
		"https://x":   false,
		"":            false,
	}
	for in, want := range cases {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q) = %v, want %v", in, got, want)
		}
	}
}
