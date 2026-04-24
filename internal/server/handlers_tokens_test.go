package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/i18n"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/vault"
	"github.com/gosidian/gosidian/internal/webauth"
)

// setupTokensServer wires a server with a token store, a webauth account, and
// an audit log pointing to a tempfile. Returns the server plus a live session
// cookie so tests can make authenticated requests without POST /login noise.
func setupTokensServer(t *testing.T) (*Server, *auth.Store, *audit.Log, *http.Cookie) {
	t.Helper()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "gosidian"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "rc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gosidian", "hello.md"), []byte("# Hello"), 0o644); err != nil {
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

	tokStore, err := auth.Open(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}

	waStore, err := webauth.Open(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := waStore.Setup("admin", "supersecret", false, "Gosidian"); err != nil {
		t.Fatal(err)
	}

	auditLog, err := audit.Open(filepath.Join(t.TempDir(), "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	s := New(v, idx, tokStore, "", waStore)
	s.SetAuditLog(auditLog)
	if cat, err := i18n.Load("it"); err == nil {
		s.SetI18n(cat, "it")
	}

	owner := waStore.FirstOwner()
	if owner == nil {
		t.Fatal("first owner missing after Setup")
	}
	sid, err := waStore.CreateSession(owner.ID, loginSessionTTL)
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: webauth.SessionCookieName, Value: sid}

	return s, tokStore, auditLog, cookie
}

// authedReq is doReq with the session cookie attached.
func authedReq(t *testing.T, s *Server, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	r.AddCookie(cookie)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestTokens_GetRequiresAuth(t *testing.T) {
	s, _, _, _ := setupTokensServer(t)
	// No cookie → redirect to /login
	r := httptest.NewRequest("GET", "/admin/tokens", nil)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected redirect, got %d", w.Code)
	}
}

func TestTokens_GetAuthenticatedShowsForm(t *testing.T) {
	s, _, _, cookie := setupTokensServer(t)
	w := authedReq(t, s, "GET", "/admin/tokens", "", cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{
		`name="name"`,
		`name="project"`,
		`name="scope_read"`,
		`name="scope_write"`,
		`name="ttl"`,
		`Crea token`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	// Projects from vault should be in the dropdown
	if !strings.Contains(body, `value="gosidian"`) {
		t.Errorf("expected gosidian in projects dropdown, got body:\n%s", body)
	}
}

func TestTokens_CreateHappyPath(t *testing.T) {
	s, store, auditLog, cookie := setupTokensServer(t)
	form := "action=create&name=test-token&project=gosidian&scope_read=on&ttl=30d"
	w := authedReq(t, s, "POST", "/admin/tokens", form, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "gosidian_") {
		t.Fatalf("plaintext with gosidian_ prefix not in body: %s", body)
	}
	if !strings.Contains(body, "Token creato") {
		t.Errorf("missing success message")
	}

	list := store.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 token, got %d", len(list))
	}
	if list[0].Name != "test-token" || list[0].Project != "gosidian" {
		t.Errorf("stored token = %+v", list[0])
	}
	if list[0].HasScope(auth.ScopeWrite) {
		t.Errorf("token should not have write scope")
	}
	if !list[0].HasScope(auth.ScopeRead) {
		t.Errorf("token should have read scope")
	}

	entries, _ := auditLog.Tail(10)
	if len(entries) != 1 || entries[0].Action != audit.ActionTokenCreate {
		t.Errorf("audit entries = %+v", entries)
	}
	if entries[0].Path != list[0].ID {
		t.Errorf("audit path = %q, want token id %q", entries[0].Path, list[0].ID)
	}
}

func TestTokens_CreateMissingName(t *testing.T) {
	s, store, _, cookie := setupTokensServer(t)
	form := "action=create&name=&project=&scope_read=on&ttl="
	w := authedReq(t, s, "POST", "/admin/tokens", form, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "nome richiesto") {
		t.Errorf("missing error message: %s", w.Body.String())
	}
	if len(store.List()) != 0 {
		t.Errorf("no token should have been created")
	}
}

func TestTokens_CreateNoScope(t *testing.T) {
	s, store, _, cookie := setupTokensServer(t)
	form := "action=create&name=noscope&project=&ttl="
	w := authedReq(t, s, "POST", "/admin/tokens", form, cookie)
	if !strings.Contains(w.Body.String(), "almeno uno scope") {
		t.Errorf("missing error: %s", w.Body.String())
	}
	if len(store.List()) != 0 {
		t.Errorf("no token should have been created")
	}
}

func TestTokens_CreateInvalidTTL(t *testing.T) {
	s, store, _, cookie := setupTokensServer(t)
	form := "action=create&name=x&project=&scope_read=on&ttl=500y"
	w := authedReq(t, s, "POST", "/admin/tokens", form, cookie)
	if !strings.Contains(w.Body.String(), "TTL non valido") {
		t.Errorf("missing error: %s", w.Body.String())
	}
	if len(store.List()) != 0 {
		t.Errorf("no token should have been created")
	}
}

func TestTokens_Revoke(t *testing.T) {
	s, store, auditLog, cookie := setupTokensServer(t)
	_, tok, err := store.Create("to-revoke", "", []string{auth.ScopeRead}, 0, "")
	if err != nil {
		t.Fatal(err)
	}

	form := "action=revoke&id=" + tok.ID
	w := authedReq(t, s, "POST", "/admin/tokens", form, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Token revocato") {
		t.Errorf("missing success message: %s", w.Body.String())
	}
	if len(store.List()) != 0 {
		t.Errorf("token still present after revoke")
	}

	entries, _ := auditLog.Tail(10)
	if len(entries) != 1 || entries[0].Action != audit.ActionTokenRevoke {
		t.Errorf("expected 1 revoke audit entry, got %+v", entries)
	}
}

func TestTokens_RevokeUnknownID(t *testing.T) {
	s, _, _, cookie := setupTokensServer(t)
	form := "action=revoke&id=deadbeef"
	w := authedReq(t, s, "POST", "/admin/tokens", form, cookie)
	if !strings.Contains(w.Body.String(), "revoke:") {
		t.Errorf("expected revoke error, got: %s", w.Body.String())
	}
}

func TestTokens_UnknownAction(t *testing.T) {
	s, _, _, cookie := setupTokensServer(t)
	form := "action=explode"
	w := authedReq(t, s, "POST", "/admin/tokens", form, cookie)
	if !strings.Contains(w.Body.String(), "azione sconosciuta") {
		t.Errorf("expected unknown-action error, got: %s", w.Body.String())
	}
}

func TestTokens_ExpiredIsFlagged(t *testing.T) {
	s, store, _, cookie := setupTokensServer(t)
	// Create with 1ns TTL so it's immediately expired on render.
	if _, _, err := store.Create("stale", "", []string{auth.ScopeRead}, 1, ""); err != nil {
		t.Fatal(err)
	}
	w := authedReq(t, s, "GET", "/admin/tokens", "", cookie)
	body := w.Body.String()
	if !strings.Contains(body, "token-expired") {
		t.Errorf("expected token-expired class in body: %s", body)
	}
	if !strings.Contains(body, "(scaduto)") {
		t.Errorf("expected (scaduto) label in body: %s", body)
	}
}
