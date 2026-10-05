package v1

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/webauth"
)

// stubLDAP accepts the passwords it holds, by username.
type stubLDAP map[string]string

func (l stubLDAP) Authenticate(username, password string) error {
	if p, ok := l[username]; ok && p == password {
		return nil
	}
	return errors.New("invalid credentials")
}

func bearerHdr(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

func auditActions(t *testing.T, l *audit.Log) []string {
	t.Helper()
	var out []string
	if err := l.Each(func(e audit.Entry) { out = append(out, string(e.Action)) }); err != nil {
		t.Fatal(err)
	}
	return out
}

// An account changes its own password with the current one: a wrong or
// missing one is refused (403, never 401), the same one or a short one
// too; the change closes its other sessions and keeps the current one
// (IMP-063, IMP-088).
func TestMePassword(t *testing.T) {
	f := newNotesFixture(t)
	u, bearer := f.memberUser(t, "mara")
	other, _, _ := f.spaTokens.Create(u.ID, "laptop")
	hdr := bearerHdr(bearer)
	post := func(body string) int {
		t.Helper()
		return f.request(http.MethodPost, "/api/v1/me/password", body, hdr).Code
	}
	if got := post(`{"new_password":"brand-new-pass"}`); got != http.StatusBadRequest {
		t.Errorf("no current password = %d", got)
	}
	if got := post(`{"current_password":"wrong","new_password":"brand-new-pass"}`); got != http.StatusForbidden {
		t.Errorf("wrong current password = %d", got)
	}
	if got := post(`{"current_password":"mara-pass-1234","new_password":"mara-pass-1234"}`); got != http.StatusBadRequest {
		t.Errorf("same password = %d", got)
	}
	if got := post(`{"current_password":"mara-pass-1234","new_password":"short"}`); got != http.StatusBadRequest {
		t.Errorf("short password = %d", got)
	}
	rec := f.request(http.MethodPost, "/api/v1/me/password", `{"current_password":"mara-pass-1234","new_password":"brand-new-pass-1"}`, hdr)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"sessions_closed":1`) {
		t.Fatalf("change = %d %s", rec.Code, rec.Body.String())
	}
	if _, err := f.webauth.Verify("mara", "brand-new-pass-1", ""); err != nil {
		t.Errorf("new password: %v", err)
	}
	if _, err := f.webauth.Verify("mara", "mara-pass-1234", ""); err == nil {
		t.Error("the old password still works")
	}
	if _, err := f.spaTokens.Validate(bearer); err != nil {
		t.Errorf("the current session was closed: %v", err)
	}
	if _, err := f.spaTokens.Validate(other); err == nil {
		t.Error("the other session is still open")
	}
	if acts := strings.Join(auditActions(t, f.auditLog), " "); !strings.Contains(acts, string(audit.ActionPasswordChange)) {
		t.Errorf("audit: %s", acts)
	}
}

// Wrong passwords count against the account's limiter like failed logins.
func TestConfirmPassword_Limiter(t *testing.T) {
	f := newNotesFixture(t)
	hdr := bearerHdr(f.bearer)
	last := 0
	for i := 0; i < 20 && last != http.StatusTooManyRequests; i++ {
		last = f.request(http.MethodPost, "/api/v1/me/password", `{"current_password":"wrong","new_password":"brand-new-pass"}`, hdr).Code
	}
	if last != http.StatusTooManyRequests {
		t.Errorf("after many wrong passwords = %d, want 429", last)
	}
}

// The owner sets another account's password, temporary, with the owner's
// own: the account's sessions close and it must change the password before
// anything else; the gate lets through only the change and the session
// lifecycle, and the event stream stays shut (IMP-063).
func TestAdminResetPassword_AndGate(t *testing.T) {
	f := newNotesFixture(t)
	u, bearer := f.memberUser(t, "nico")
	url := "/api/v1/admin/users/" + u.ID + "/password"
	if rec := f.doAuthRecorder(http.MethodPost, url, `{"password":"temp-pass-1234","owner_password":"wrong"}`, nil); rec.code != http.StatusForbidden {
		t.Errorf("wrong owner password = %d", rec.code)
	}
	if rec := f.doAuthRecorder(http.MethodPost, "/api/v1/admin/users/"+f.owner.ID+"/password", fmt.Sprintf(`{"password":"temp-pass-1234","owner_password":%q}`, f.password), nil); rec.code != http.StatusBadRequest {
		t.Errorf("own account = %d", rec.code)
	}
	rec := f.doAuthRecorder(http.MethodPost, url, fmt.Sprintf(`{"password":"temp-pass-1234","owner_password":%q}`, f.password), nil)
	if rec.code != http.StatusOK || !strings.Contains(rec.body, `"must_change_password":true`) {
		t.Fatalf("reset = %d %s", rec.code, rec.body)
	}
	if _, err := f.spaTokens.Validate(bearer); err == nil {
		t.Error("the account's session is still open after the reset")
	}

	// The next login says so, and every route but the change is refused.
	w := f.request(http.MethodPost, "/api/v1/login", `{"username":"nico","password":"temp-pass-1234"}`, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"password_change_required":true`) {
		t.Fatalf("login = %d %s", w.Code, w.Body.String())
	}
	tok := jsonField(t, w.Body.String(), "token")
	hdr := bearerHdr(tok)
	if rec := f.request(http.MethodGet, "/api/v1/tree", "", hdr); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), CodeAuthPasswordChangeRequired) {
		t.Errorf("gated route = %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.request(http.MethodGet, "/api/v1/me", "", hdr); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"password_change_required":true`) {
		t.Errorf("me = %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.request(http.MethodPost, "/api/v1/me/password", `{"current_password":"temp-pass-1234","new_password":"nico-own-pass"}`, hdr); rec.Code != http.StatusOK {
		t.Fatalf("change = %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.request(http.MethodGet, "/api/v1/tree", "", hdr); rec.Code != http.StatusOK {
		t.Errorf("after the change = %d %s", rec.Code, rec.Body.String())
	}
	if acts := strings.Join(auditActions(t, f.auditLog), " "); !strings.Contains(acts, string(audit.ActionPasswordReset)) {
		t.Errorf("audit: %s", acts)
	}
}

// An account that owes both a password change and a TOTP enrolment changes
// the password first, then enrols: neither gate locks the other out.
func TestPasswordGate_WithEnrolmentGate(t *testing.T) {
	f := newNotesFixture(t)
	f.webauth.SetTOTPMode(webauth.TOTPRequired)
	u, bearer := f.memberUser(t, "olga")
	if err := f.webauth.SetMustChangePassword(u.ID, true); err != nil {
		t.Fatal(err)
	}
	hdr := bearerHdr(bearer)
	if rec := f.request(http.MethodPost, "/api/v1/totp/enroll", "", hdr); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), CodeAuthPasswordChangeRequired) {
		t.Errorf("enrol before the change = %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.request(http.MethodPost, "/api/v1/me/password", `{"current_password":"olga-pass-1234","new_password":"olga-own-pass"}`, hdr); rec.Code != http.StatusOK {
		t.Fatalf("change while enrolment is owed = %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.request(http.MethodPost, "/api/v1/totp/enroll", "", hdr); rec.Code != http.StatusOK {
		t.Errorf("enrol after the change = %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.request(http.MethodGet, "/api/v1/tree", "", hdr); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), CodeAuthEnrollmentRequired) {
		t.Errorf("still owes the enrolment = %d %s", rec.Code, rec.Body.String())
	}
}

// An LDAP account confirms with its directory password and cannot change
// it here; a token needs the password, as a TOTP removal does.
func TestConfirmPassword_LDAPAndTokens(t *testing.T) {
	f := newAdminFixture(t)
	f.router.deps.Auth.LDAP = stubLDAP{"lena": "ldap-pass-1234"}
	u, err := f.webauth.AddLDAPUser("lena")
	if err != nil {
		t.Fatal(err)
	}
	b, _, _ := f.spaTokens.Create(u.ID, "test")
	hdr := bearerHdr(b)
	if rec := f.request(http.MethodPost, "/api/v1/me/password", `{"current_password":"ldap-pass-1234","new_password":"another-pass"}`, hdr); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "LDAP") {
		t.Errorf("LDAP change = %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.request(http.MethodPost, "/api/v1/me/tokens", `{"name":"t"}`, hdr); rec.Code != http.StatusBadRequest {
		t.Errorf("token without password = %d", rec.Code)
	}
	if rec := f.request(http.MethodPost, "/api/v1/me/tokens", `{"name":"t","password":"wrong"}`, hdr); rec.Code != http.StatusForbidden {
		t.Errorf("token with a wrong password = %d", rec.Code)
	}
	if rec := f.request(http.MethodPost, "/api/v1/me/tokens", `{"name":"t","password":"ldap-pass-1234"}`, hdr); rec.Code != http.StatusCreated {
		t.Errorf("token with the directory password = %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.doAuthRecorder(http.MethodPost, "/api/v1/admin/tokens", `{"name":"a","scopes":["read"]}`, nil); rec.code != http.StatusBadRequest {
		t.Errorf("admin token without password = %d", rec.code)
	}
}
