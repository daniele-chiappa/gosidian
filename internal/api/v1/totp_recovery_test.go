package v1

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/webauth"
	"github.com/pquerna/otp/totp"
)

// enrolOwnerTOTP walks the owner through /totp/enroll + /totp/confirm and
// returns the secret and the recovery codes the confirmation minted.
func enrolOwnerTOTP(t *testing.T, f *notesFixture) (secret string, codes []string) {
	t.Helper()
	er := f.doAuthRecorder(http.MethodPost, "/api/v1/totp/enroll", "", nil)
	if er.code != http.StatusOK {
		t.Fatalf("enroll status %d: %s", er.code, er.body)
	}
	var enr struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal([]byte(er.body), &enr); err != nil {
		t.Fatal(err)
	}
	code, _ := totp.GenerateCode(enr.Secret, time.Now())
	cr := f.doAuthRecorder(http.MethodPost, "/api/v1/totp/confirm", `{"secret":"`+enr.Secret+`","code":"`+code+`"}`, nil)
	if cr.code != http.StatusOK {
		t.Fatalf("confirm status %d: %s", cr.code, cr.body)
	}
	var out struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.Unmarshal([]byte(cr.body), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.RecoveryCodes) != webauth.RecoveryCodeCount {
		t.Fatalf("confirm returned %d recovery codes: %s", len(out.RecoveryCodes), cr.body)
	}
	return enr.Secret, out.RecoveryCodes
}

func loginBody(username, password, code string) string {
	return fmt.Sprintf(`{"username":%q,"password":%q,"totp":%q}`, username, password, code)
}

// TestTOTPRecoveryLogin: a recovery code stands in for the TOTP exactly once,
// the login response says so and carries the remaining count, and the use is
// audited.
func TestTOTPRecoveryLogin(t *testing.T) {
	f := newNotesFixture(t)
	f.webauth.SetTOTPMode(webauth.TOTPOptional)
	_, codes := enrolOwnerTOTP(t, f)
	login := func(code string) *httptest.ResponseRecorder {
		return f.request(http.MethodPost, "/api/v1/login", loginBody("owner", "supersecret", code), nil)
	}

	if w := login(""); w.Code != http.StatusUnauthorized {
		t.Fatalf("enrolled account without code: status %d", w.Code)
	}
	w := login(codes[0])
	if w.Code != http.StatusOK {
		t.Fatalf("recovery login status %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		RecoveryCodeUsed bool `json:"recovery_code_used"`
		User             struct {
			Enrolled  bool `json:"totp_enrolled"`
			Remaining *int `json:"recovery_codes_remaining"`
		} `json:"user"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.RecoveryCodeUsed || !resp.User.Enrolled {
		t.Errorf("flags: %s", w.Body.String())
	}
	if resp.User.Remaining == nil || *resp.User.Remaining != webauth.RecoveryCodeCount-1 {
		t.Errorf("remaining: %s", w.Body.String())
	}
	if w := login(codes[0]); w.Code != http.StatusUnauthorized {
		t.Errorf("reused recovery code: status %d", w.Code)
	}
	// /me exposes the counter for the Settings view.
	if me := f.doAuthRecorder(http.MethodGet, "/api/v1/me", "", nil); !strings.Contains(me.body, `"recovery_codes_remaining":7`) {
		t.Errorf("/me: %s", me.body)
	}
	if al := f.doAuthRecorder(http.MethodGet, "/api/v1/admin/audit?action=totp_recovery_used", "", nil); !strings.Contains(al.body, `"totp_recovery_used"`) {
		t.Errorf("recovery use not audited: %s", al.body)
	}
}

// TestTOTPRecoveryRegen: regeneration needs an enrolled secret and a current
// TOTP; the new set replaces the old one entirely.
func TestTOTPRecoveryRegen(t *testing.T) {
	f := newNotesFixture(t)
	f.webauth.SetTOTPMode(webauth.TOTPOptional)
	if rec := f.doAuthRecorder(http.MethodPost, "/api/v1/totp/recovery-codes", `{"code":"000000"}`, nil); rec.code != http.StatusForbidden {
		t.Fatalf("not enrolled: status %d want 403: %s", rec.code, rec.body)
	}
	secret, old := enrolOwnerTOTP(t, f)
	if rec := f.doAuthRecorder(http.MethodPost, "/api/v1/totp/recovery-codes", `{}`, nil); rec.code != http.StatusBadRequest {
		t.Errorf("missing code: status %d want 400", rec.code)
	}
	if rec := f.doAuthRecorder(http.MethodPost, "/api/v1/totp/recovery-codes", `{"code":"000000"}`, nil); rec.code != http.StatusBadRequest {
		t.Errorf("wrong code: status %d want 400: %s", rec.code, rec.body)
	}
	code, _ := totp.GenerateCode(secret, time.Now())
	rec := f.doAuthRecorder(http.MethodPost, "/api/v1/totp/recovery-codes", `{"code":"`+code+`"}`, nil)
	if rec.code != http.StatusOK {
		t.Fatalf("regen status %d: %s", rec.code, rec.body)
	}
	var out struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	if err := json.Unmarshal([]byte(rec.body), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.RecoveryCodes) != webauth.RecoveryCodeCount {
		t.Fatalf("regen returned %d codes", len(out.RecoveryCodes))
	}
	if w := f.request(http.MethodPost, "/api/v1/login", loginBody("owner", "supersecret", old[0]), nil); w.Code != http.StatusUnauthorized {
		t.Errorf("old code after regen: status %d", w.Code)
	}
	if w := f.request(http.MethodPost, "/api/v1/login", loginBody("owner", "supersecret", out.RecoveryCodes[0]), nil); w.Code != http.StatusOK {
		t.Errorf("fresh code: status %d: %s", w.Code, w.Body.String())
	}
	if al := f.doAuthRecorder(http.MethodGet, "/api/v1/admin/audit?action=totp_recovery_regen", "", nil); !strings.Contains(al.body, `"totp_recovery_regen"`) {
		t.Errorf("regeneration not audited: %s", al.body)
	}
}

// TestAdminResetTOTP: the owner clears a locked-out member's second factor;
// the member then re-enrols under the policy instead of being stuck.
func TestAdminResetTOTP(t *testing.T) {
	f := newNotesFixture(t)
	// Optional globally (the owner stays free of the enrolment gate) with a
	// per-user "enabled" override: the member must have TOTP.
	f.webauth.SetTOTPMode(webauth.TOTPOptional)
	m, bearer := f.memberUser(t, "mia")
	if err := f.webauth.SetTOTPPolicy(m.ID, webauth.TOTPEnabled); err != nil {
		t.Fatal(err)
	}
	secret, _, err := f.webauth.GenerateTOTPSecret("mia", "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.webauth.EnrollTOTP(m.ID, secret); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/admin/users/" + m.ID + "/totp"

	if rec := f.req(t, http.MethodDelete, path, "", bearer); rec.code != http.StatusForbidden {
		t.Errorf("member reached the admin reset: status %d", rec.code)
	}
	if rec := f.doAuthRecorder(http.MethodGet, path, "", nil); rec.code != http.StatusMethodNotAllowed {
		t.Errorf("GET on /totp: status %d want 405", rec.code)
	}
	if rec := f.doAuthRecorder(http.MethodDelete, "/api/v1/admin/users/"+m.ID+"/bogus", "", nil); rec.code != http.StatusBadRequest {
		t.Errorf("unknown sub-resource: status %d want 400", rec.code)
	}
	if rec := f.doAuthRecorder(http.MethodDelete, "/api/v1/admin/users/nope/totp", "", nil); rec.code != http.StatusNotFound {
		t.Errorf("unknown user: status %d want 404", rec.code)
	}

	if rec := f.doAuthRecorder(http.MethodDelete, path, "", nil); rec.code != http.StatusNoContent {
		t.Fatalf("reset status %d: %s", rec.code, rec.body)
	}
	if u, ok := f.webauth.UserByID(m.ID); !ok || u.TOTPSec != "" || len(u.RecoveryCodes) != 0 {
		t.Fatalf("after reset: %+v", u)
	}
	// The policy is untouched, so the member now owes a fresh enrolment
	// (BUG-020 gate) instead of being locked out.
	w := f.request(http.MethodPost, "/api/v1/login", `{"username":"mia","password":"mia-pass-1234"}`, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"totp_enrollment_required":true`) {
		t.Errorf("member login after reset: status %d body %s", w.Code, w.Body.String())
	}
	if al := f.doAuthRecorder(http.MethodGet, "/api/v1/admin/audit?action=totp_reset", "", nil); !strings.Contains(al.body, m.ID) {
		t.Errorf("reset not audited with the target id: %s", al.body)
	}
	// The plain item route is unaffected by the sub-resource parsing.
	if rec := f.doAuthRecorder(http.MethodPatch, "/api/v1/admin/users/"+m.ID, `{"totp_policy":"disabled"}`, nil); rec.code != http.StatusOK {
		t.Errorf("PATCH item after routing change: status %d: %s", rec.code, rec.body)
	}
}

// TestSecondFactorAccountLimiter: wrong second factors close the account for
// the window regardless of the client IP, while wrong passwords — the only
// thing a stranger can send — never touch the account bucket.
func TestSecondFactorAccountLimiter(t *testing.T) {
	f := newNotesFixture(t)
	withTrustedProxies(t, "192.0.2.1")
	prevMax, prevWin := LoginMaxFailures, LoginWindow
	LoginMaxFailures = 2
	LoginWindow = time.Minute
	defer func() {
		LoginMaxFailures = prevMax
		LoginWindow = prevWin
	}()
	f.webauth.SetTOTPMode(webauth.TOTPOptional)
	secret, _ := enrolOwnerTOTP(t, f)
	loginFrom := func(ip, body string) *httptest.ResponseRecorder {
		return f.request(http.MethodPost, "/api/v1/login", body, map[string]string{"X-Forwarded-For": ip})
	}
	wrongPassword := loginBody("owner", "nope", "000000")
	wrongCode := loginBody("owner", "supersecret", "000000")
	code, _ := totp.GenerateCode(secret, time.Now())
	right := loginBody("owner", "supersecret", code)

	// A stranger rotating IPs with bad passwords cannot lock the account.
	for i := 0; i <= LoginMaxFailures; i++ {
		if w := loginFrom(fmt.Sprintf("10.1.0.%d", i+1), wrongPassword); w.Code != http.StatusUnauthorized {
			t.Fatalf("wrong password %d: status %d", i+1, w.Code)
		}
	}
	if w := loginFrom("10.1.0.200", right); w.Code != http.StatusOK {
		t.Fatalf("legit login after password spray: status %d: %s", w.Code, w.Body.String())
	}

	// Right password, wrong code, from distinct IPs: the account closes and
	// even the right code from a fresh IP is refused for the window.
	for i := 0; i < LoginMaxFailures; i++ {
		if w := loginFrom(fmt.Sprintf("10.2.0.%d", i+1), wrongCode); w.Code != http.StatusUnauthorized {
			t.Fatalf("wrong code %d: status %d", i+1, w.Code)
		}
	}
	if w := loginFrom("10.2.0.200", right); w.Code != http.StatusTooManyRequests {
		t.Errorf("account not closed after second-factor failures: status %d", w.Code)
	}
	// Regeneration shares the bucket.
	if rec := f.doAuthRecorder(http.MethodPost, "/api/v1/totp/recovery-codes", `{"code":"`+code+`"}`, nil); rec.code != http.StatusTooManyRequests {
		t.Errorf("recovery-codes with closed account: status %d want 429", rec.code)
	}
}
