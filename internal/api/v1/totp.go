package v1

import (
	"log/slog"
	"net/http"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/qrsvg"
	"github.com/gosidian/gosidian/internal/webauth"
)

// handleAuthConfig is a PUBLIC endpoint the LoginView fetches before
// authentication to decide whether to render the TOTP field (and, in Phase 3,
// surface the LDAP option). It returns only booleans — never which usernames
// have TOTP — so probing it leaks nothing.
func (r *Router) handleAuthConfig(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	totp := false
	if r.deps.Auth != nil && r.deps.Auth.WebAuth != nil {
		totp = r.deps.Auth.WebAuth.TOTPMode() != webauth.TOTPOff
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"totp": totp,
		"ldap": r.deps.Auth != nil && r.deps.Auth.LDAP != nil,
	})
}

type totpEnrollResponse struct {
	Secret     string `json:"secret"`
	OTPAuthURI string `json:"otpauth_uri"`
	// QRCodeSVG is a self-contained SVG of the otpauth URI the SPA renders
	// inline for scanning. Empty if rendering failed — the secret + URI above
	// are always present as a manual-entry fallback, so a QR failure never
	// blocks enrolment.
	QRCodeSVG string `json:"qr_svg"`
}

// handleTOTPEnroll generates a fresh secret + provisioning URI. The secret is
// NOT persisted until the user confirms a valid code via handleTOTPConfirm.
func (r *Router) handleTOTPEnroll(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	user := UserFromContext(req.Context())
	if user == nil || r.deps.Auth == nil || r.deps.Auth.WebAuth == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	if user.isAnonymous() {
		WriteError(w, http.StatusForbidden, CodeAuthForbidden, "anonymous session has no account to manage")
		return
	}
	secret, uri, err := r.deps.Auth.WebAuth.GenerateTOTPSecret(user.Username, "gosidian")
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "totp: "+err.Error())
		return
	}
	// Best-effort QR: the secret + URI are the authoritative payload, so a
	// rendering failure degrades to manual entry rather than failing enrolment.
	svg, qerr := qrsvg.SVG(uri)
	if qerr != nil {
		slog.Default().Warn("api/v1: totp qr render failed", "err", qerr)
	}
	WriteJSON(w, http.StatusOK, totpEnrollResponse{Secret: secret, OTPAuthURI: uri, QRCodeSVG: svg})
}

type totpConfirmRequest struct {
	Secret string `json:"secret"`
	Code   string `json:"code"`
}

// totpRecoveryCodesResponse carries a freshly minted set of recovery codes.
// They are shown exactly once: the store keeps only their hashes.
type totpRecoveryCodesResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
}

// handleTOTPConfirm validates a code against the candidate secret from
// /totp/enroll and, on success, activates it for the user together with the
// account's first set of recovery codes. No rate limit here: the code is
// checked against a secret the client itself supplied, so there is nothing
// to guess.
func (r *Router) handleTOTPConfirm(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	user := UserFromContext(req.Context())
	if user == nil || r.deps.Auth == nil || r.deps.Auth.WebAuth == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	if user.isAnonymous() {
		WriteError(w, http.StatusForbidden, CodeAuthForbidden, "anonymous session has no account to manage")
		return
	}
	var body totpConfirmRequest
	if err := DecodeJSON(req, &body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	if body.Secret == "" || body.Code == "" {
		WriteError(w, http.StatusBadRequest, CodeValidationRequired, "secret and code are required")
		return
	}
	if !webauth.ValidateTOTPCode(body.Secret, body.Code) {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "invalid code")
		return
	}
	codes, err := r.deps.Auth.WebAuth.EnrollTOTP(user.ID, body.Secret)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	if r.deps.Audit != nil {
		_ = r.deps.Audit.Write(audit.Entry{Source: audit.SourceHTTP, Actor: user.Username, UserID: user.ID, Action: audit.ActionTOTPEnroll, Path: user.ID})
	}
	WriteJSON(w, http.StatusOK, totpRecoveryCodesResponse{RecoveryCodes: codes})
}

type totpRecoveryRequest struct {
	Code string `json:"code"`
}

// handleTOTPRecoveryCodes replaces the caller's recovery codes with a fresh
// set. POST /api/v1/totp/recovery-codes with a current TOTP code: the session
// alone must not be enough, or a hijacked browser tab could mint itself a
// lasting second factor. A wrong code counts against the account's
// second-factor limiter exactly like a failed login.
func (r *Router) handleTOTPRecoveryCodes(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	user := UserFromContext(req.Context())
	if user == nil || r.deps.Auth == nil || r.deps.Auth.WebAuth == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	if user.isAnonymous() {
		WriteError(w, http.StatusForbidden, CodeAuthForbidden, "anonymous session has no account to manage")
		return
	}
	var body totpRecoveryRequest
	if err := DecodeJSON(req, &body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	if body.Code == "" {
		WriteError(w, http.StatusBadRequest, CodeValidationRequired, "code is required")
		return
	}
	full, ok := r.deps.Auth.WebAuth.UserByID(user.ID)
	if !ok || full.TOTPSec == "" {
		WriteError(w, http.StatusForbidden, CodeAuthForbidden, "two-factor is not enrolled")
		return
	}
	acct := accountLimiterKey(user.Username)
	if r.loginLimiter != nil && !r.loginLimiter.allowed(acct) {
		WriteError(w, http.StatusTooManyRequests, CodeRateLimit, "too many failed two-factor attempts; try again later")
		return
	}
	if !webauth.ValidateTOTPCode(full.TOTPSec, body.Code) {
		if r.loginLimiter != nil {
			r.loginLimiter.registerFail(acct)
		}
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "invalid code")
		return
	}
	if r.loginLimiter != nil {
		r.loginLimiter.reset(acct)
	}
	codes, err := r.deps.Auth.WebAuth.GenerateRecoveryCodes(user.ID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	if r.deps.Audit != nil {
		_ = r.deps.Audit.Write(audit.Entry{Source: audit.SourceHTTP, Actor: user.Username, UserID: user.ID, Action: audit.ActionTOTPRecoveryRegen, Path: user.ID})
	}
	WriteJSON(w, http.StatusOK, totpRecoveryCodesResponse{RecoveryCodes: codes})
}

// handleTOTPDisenroll removes the user's TOTP secret and recovery codes,
// unless their effective policy requires it (403). DELETE /api/v1/totp.
func (r *Router) handleTOTPDisenroll(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodDelete {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	user := UserFromContext(req.Context())
	if user == nil || r.deps.Auth == nil || r.deps.Auth.WebAuth == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	if user.isAnonymous() {
		WriteError(w, http.StatusForbidden, CodeAuthForbidden, "anonymous session has no account to manage")
		return
	}
	// Re-load the full account: RequestUser carries only id/username/role, but
	// the required-policy check needs the per-user TOTPPolicy.
	if full, ok := r.deps.Auth.WebAuth.UserByID(user.ID); ok && r.deps.Auth.WebAuth.TOTPRequired(full) {
		WriteError(w, http.StatusForbidden, CodeAuthForbidden, "TOTP is required for your account and cannot be removed")
		return
	}
	if err := r.deps.Auth.WebAuth.ResetTOTP(user.ID); err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	if r.deps.Audit != nil {
		_ = r.deps.Audit.Write(audit.Entry{Source: audit.SourceHTTP, Actor: user.Username, UserID: user.ID, Action: audit.ActionTOTPDisenroll, Path: user.ID})
	}
	w.WriteHeader(http.StatusNoContent)
}
