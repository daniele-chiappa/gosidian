package v1

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gosidian/gosidian/internal/webauth"
	"github.com/pquerna/otp/totp"
)

func TestAuthConfigTOTP(t *testing.T) {
	f := newNotesFixture(t)
	// Default mode is off → field hidden.
	rec := f.request(http.MethodGet, "/api/v1/auth-config", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("auth-config status %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"totp":false`) {
		t.Errorf("expected totp:false, got %s", rec.Body.String())
	}
	f.webauth.SetTOTPMode(webauth.TOTPOptional)
	rec = f.request(http.MethodGet, "/api/v1/auth-config", "", nil)
	if !strings.Contains(rec.Body.String(), `"totp":true`) {
		t.Errorf("expected totp:true after enabling, got %s", rec.Body.String())
	}
}

func TestTOTPEnrollConfirmDisenroll(t *testing.T) {
	f := newNotesFixture(t)
	f.webauth.SetTOTPMode(webauth.TOTPOptional)

	rec := f.doAuthRecorder(http.MethodPost, "/api/v1/totp/enroll", "", nil)
	if rec.code != http.StatusOK {
		t.Fatalf("enroll status %d: %s", rec.code, rec.body)
	}
	var enr struct {
		Secret string `json:"secret"`
		URI    string `json:"otpauth_uri"`
	}
	if err := json.Unmarshal([]byte(rec.body), &enr); err != nil {
		t.Fatal(err)
	}
	if enr.Secret == "" || !strings.HasPrefix(enr.URI, "otpauth://") {
		t.Fatalf("bad enroll payload: %s", rec.body)
	}

	// Bogus code → 400, no persistence.
	if rec := f.doAuthRecorder(http.MethodPost, "/api/v1/totp/confirm", `{"secret":"`+enr.Secret+`","code":"000000"}`, nil); rec.code != http.StatusBadRequest {
		t.Errorf("bogus confirm status %d want 400", rec.code)
	}

	code, _ := totp.GenerateCode(enr.Secret, time.Now())
	rec = f.doAuthRecorder(http.MethodPost, "/api/v1/totp/confirm", `{"secret":"`+enr.Secret+`","code":"`+code+`"}`, nil)
	if rec.code != http.StatusOK {
		t.Fatalf("confirm status %d: %s", rec.code, rec.body)
	}
	// Confirmation mints the recovery codes alongside the secret.
	if !strings.Contains(rec.body, `"recovery_codes":[`) {
		t.Errorf("confirm response missing recovery codes: %s", rec.body)
	}
	if u, ok := f.webauth.UserByID(f.owner.ID); !ok || u.TOTPSec == "" || u.RecoveryCodesRemaining() != webauth.RecoveryCodeCount {
		t.Error("secret or recovery codes not persisted after confirm")
	}

	// Disenroll (optional + not required) → 204, secret and codes cleared.
	if rec := f.doAuthRecorder(http.MethodDelete, "/api/v1/totp", "", nil); rec.code != http.StatusNoContent {
		t.Fatalf("disenroll status %d: %s", rec.code, rec.body)
	}
	if u, ok := f.webauth.UserByID(f.owner.ID); ok && (u.TOTPSec != "" || len(u.RecoveryCodes) != 0) {
		t.Error("secret or codes not cleared after disenroll")
	}
}

func TestLoginTOTPEnrollmentRequired(t *testing.T) {
	f := newNotesFixture(t)
	u, err := f.webauth.AddUser("needs2fa", "needs2fa-pass-1", webauth.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	// Global enabled (optional) + a per-user "required" override forces TOTP
	// for this user even though others remain opt-in.
	f.webauth.SetTOTPMode(webauth.TOTPOptional)
	if err := f.webauth.SetTOTPPolicy(u.ID, webauth.TOTPEnabled); err != nil {
		t.Fatal(err)
	}
	body := `{"username":"needs2fa","password":"needs2fa-pass-1"}`
	rec := f.request(http.MethodPost, "/api/v1/login", body, map[string]string{"Content-Type": "application/json"})
	if rec.Code != http.StatusOK {
		t.Fatalf("login status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"totp_enrollment_required":true`) {
		t.Errorf("expected enrollment-required flag: %s", rec.Body.String())
	}
}

// TestTOTPMasterOffDormantOverride guards the anti-lockout invariant: with the
// global mode off, a per-user "enabled" override must stay dormant (the login
// field is hidden, so enforcing TOTP would lock the user out).
func TestTOTPMasterOffDormantOverride(t *testing.T) {
	f := newNotesFixture(t) // store defaults to mode "off"
	u, err := f.webauth.AddUser("forced", "forced-pass-12", webauth.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.webauth.SetTOTPPolicy(u.ID, webauth.TOTPEnabled); err != nil {
		t.Fatal(err)
	}
	if rec := f.request(http.MethodGet, "/api/v1/auth-config", "", nil); !strings.Contains(rec.Body.String(), `"totp":false`) {
		t.Errorf("global off must report totp:false, got %s", rec.Body.String())
	}
	rec := f.request(http.MethodPost, "/api/v1/login", `{"username":"forced","password":"forced-pass-12"}`, map[string]string{"Content-Type": "application/json"})
	if rec.Code != http.StatusOK {
		t.Fatalf("login status %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"totp_enrollment_required":true`) {
		t.Errorf("override must be dormant under global off (no lockout): %s", rec.Body.String())
	}
}

func TestAdminTOTPPolicyOverride(t *testing.T) {
	f := newNotesFixture(t)
	u, err := f.webauth.AddUser("ov", "ov-pass-12345", webauth.RoleMember)
	if err != nil {
		t.Fatal(err)
	}
	if rec := f.doAuthRecorder(http.MethodPatch, "/api/v1/admin/users/"+u.ID, `{"totp_policy":"enabled"}`, nil); rec.code != http.StatusOK {
		t.Fatalf("set policy status %d: %s", rec.code, rec.body)
	}
	if uu, ok := f.webauth.UserByID(u.ID); !ok || uu.TOTPPolicy != webauth.TOTPEnabled {
		t.Error("totp policy not persisted")
	}
	if rec := f.doAuthRecorder(http.MethodPatch, "/api/v1/admin/users/"+u.ID, `{"totp_policy":"bogus"}`, nil); rec.code != http.StatusBadRequest {
		t.Errorf("bogus policy status %d want 400 (%s)", rec.code, rec.body)
	}
}

// TestTOTPEnrollmentGate is the server-side counterpart of the SPA enrolment
// interstitial: a user whose effective policy mandates TOTP but who has not
// enrolled a secret holds a valid token, yet every route outside the enrolment
// flow is refused with auth.enrollment_required until a secret is confirmed.
// Guards BUG-020 — the gate used to be SPA-only and bypassable via the API.
func TestTOTPEnrollmentGate(t *testing.T) {
	f := newNotesFixture(t)
	// Global "required" makes the (not-yet-enrolled) owner owe an enrolment.
	f.webauth.SetTOTPMode(webauth.TOTPRequired)

	// A normal data route is gated by the middleware, before the handler runs.
	rec := f.doAuthRecorder(http.MethodGet, "/api/v1/tree", "", nil)
	if rec.code != http.StatusForbidden {
		t.Fatalf("gated route status %d want 403: %s", rec.code, rec.body)
	}
	if !strings.Contains(rec.body, `"auth.enrollment_required"`) {
		t.Errorf("expected auth.enrollment_required code, got %s", rec.body)
	}

	// The enrolment flow itself stays reachable (exempt path), and now carries
	// an inline QR for scanning.
	er := f.doAuthRecorder(http.MethodPost, "/api/v1/totp/enroll", "", nil)
	if er.code != http.StatusOK {
		t.Fatalf("enroll status %d (must be exempt): %s", er.code, er.body)
	}
	var enr struct {
		Secret string `json:"secret"`
		QRSVG  string `json:"qr_svg"`
	}
	if err := json.Unmarshal([]byte(er.body), &enr); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(enr.QRSVG, "<svg ") {
		t.Errorf("enroll response missing inline QR svg: %.40q", enr.QRSVG)
	}

	// Confirm a valid code → secret persisted → the gate lifts.
	code, _ := totp.GenerateCode(enr.Secret, time.Now())
	if cr := f.doAuthRecorder(http.MethodPost, "/api/v1/totp/confirm", `{"secret":"`+enr.Secret+`","code":"`+code+`"}`, nil); cr.code != http.StatusOK {
		t.Fatalf("confirm status %d: %s", cr.code, cr.body)
	}
	if rec := f.doAuthRecorder(http.MethodGet, "/api/v1/tree", "", nil); rec.code != http.StatusOK {
		t.Fatalf("after enrolment the data route must pass, got %d: %s", rec.code, rec.body)
	}
}
