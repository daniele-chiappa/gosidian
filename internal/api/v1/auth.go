package v1

import (
	"errors"
	"net/http"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/webauth"
)

// loginRequest matches the OpenAPI LoginRequest schema. JSON only — the
// SPA never POSTs form-encoded credentials so we don't accept it.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	// TOTP is the second factor: a 6-digit TOTP code, or one of the
	// account's single-use recovery codes.
	TOTP string `json:"totp,omitempty"`
}

// loginResponse mirrors the OpenAPI LoginResponse. The plaintext token
// is shown exactly once — the SPA stores it in localStorage and the
// server never sees it again outside Authorization headers.
type loginResponse struct {
	Token      string   `json:"token"`
	ExpiresAt  string   `json:"expires_at"`
	HardExpiry string   `json:"hard_expiry"`
	User       userView `json:"user"`
	// TOTPEnrollmentRequired is true when the user's effective policy mandates
	// TOTP but no secret is enrolled yet — the SPA forces the enrolment
	// interstitial before granting access.
	TOTPEnrollmentRequired bool `json:"totp_enrollment_required,omitempty"`
	// RecoveryCodeUsed is true when this login consumed a recovery code
	// instead of a TOTP; the SPA nudges the user to regenerate the set.
	RecoveryCodeUsed bool `json:"recovery_code_used,omitempty"`
}

type refreshResponse struct {
	Token      string `json:"token"`
	ExpiresAt  string `json:"expires_at"`
	HardExpiry string `json:"hard_expiry"`
}

// userView is the redacted projection of webauth.User the SPA receives.
// It deliberately omits Hash, TOTPSec, CreatedAt, DisabledAt — none
// belong on the client.
type userView struct {
	ID           string `json:"id"`
	Username     string `json:"username"`
	Role         string `json:"role"`
	TOTPEnrolled bool   `json:"totp_enrolled"`
	// RecoveryCodesRemaining counts the unused recovery codes; omitted when
	// not enrolled, so the SPA can tell "none left" from "not applicable".
	RecoveryCodesRemaining *int `json:"recovery_codes_remaining,omitempty"`
}

// recoveryRemaining projects the recovery-code counter for userView.
func recoveryRemaining(u *webauth.User) *int {
	if u == nil || u.TOTPSec == "" {
		return nil
	}
	n := u.RecoveryCodesRemaining()
	return &n
}

// handleLogin implements POST /api/v1/login. Body is JSON; on success
// we mint a SPA token, audit the create, and return the plaintext +
// expiry envelope. Two rate limits guard it: per client IP (any
// failure) and per account (wrong second factor only — see
// accountLimiterKey).
func (r *Router) handleLogin(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	if r.deps.Auth == nil || r.deps.Auth.WebAuth == nil || !r.deps.Auth.WebAuth.Enabled() {
		// Auth disabled at the server level — login itself is meaningless.
		// Surface a clear error rather than letting the SPA spin on a
		// silent failure.
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "web auth not configured")
		return
	}

	// Per-IP rate limit before we touch bcrypt. allowed() lazy-prunes
	// stale entries so a slow-burning brute force doesn't bloat the
	// map; registerFail runs only on actual auth failure.
	ip := clientIP(req)
	if r.loginLimiter != nil && !r.loginLimiter.allowed(ip) {
		WriteError(w, http.StatusTooManyRequests, CodeRateLimit, "too many failed login attempts; try again later")
		return
	}

	var body loginRequest
	if err := DecodeJSON(req, &body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	if body.Username == "" || body.Password == "" {
		WriteError(w, http.StatusBadRequest, CodeValidationRequired, "username and password are required")
		return
	}

	// Per-account second-factor limit: an account whose TOTP has been
	// guessed at too often is closed for the window even from a fresh IP.
	acct := accountLimiterKey(body.Username)
	if r.loginLimiter != nil && !r.loginLimiter.allowed(acct) {
		WriteError(w, http.StatusTooManyRequests, CodeRateLimit, "too many failed login attempts; try again later")
		return
	}

	res, verr := r.deps.Auth.WebAuth.Authenticate(body.Username, body.Password, body.TOTP, r.deps.Auth.LDAP)
	if verr != nil {
		if r.loginLimiter != nil {
			r.loginLimiter.registerFail(ip)
			if errors.Is(verr, webauth.ErrInvalidSecondFactor) {
				r.loginLimiter.registerFail(acct)
			}
		}
		// Audit the failure with the actor we tried to authenticate as
		// — this is what SOC dashboards grep for when triaging brute
		// force attempts.
		if r.deps.Audit != nil {
			_ = r.deps.Audit.Write(audit.Entry{
				Source: audit.SourceHTTP,
				Actor:  body.Username,
				Action: audit.ActionSpaLoginFailed,
				Path:   verr.Error(),
			})
		}
		WriteError(w, http.StatusUnauthorized, CodeAuthInvalidCredentials, "invalid credentials")
		return
	}
	user := res.User
	// Successful auth resets both failure histories so a typo right
	// before a correct login doesn't punish the next legit failure burst.
	if r.loginLimiter != nil {
		r.loginLimiter.reset(ip)
		r.loginLimiter.reset(acct)
	}

	plain, tok, terr := r.deps.Auth.SpaAuth.Create(user.ID, req.UserAgent())
	if terr != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "session create: "+terr.Error())
		return
	}

	if r.deps.Audit != nil {
		_ = r.deps.Audit.Write(audit.Entry{
			Source: audit.SourceHTTP,
			Actor:  user.Username,
			UserID: user.ID,
			Action: audit.ActionSpaTokenCreate,
			Path:   tok.ID,
		})
		if res.RecoveryCodeUsed {
			_ = r.deps.Audit.Write(audit.Entry{
				Source: audit.SourceHTTP,
				Actor:  user.Username,
				UserID: user.ID,
				Action: audit.ActionTOTPRecoveryUsed,
				Path:   user.ID,
			})
		}
	}

	r.setFilesCookie(w, req, plain, tok.HardExpiry)
	WriteJSON(w, http.StatusOK, loginResponse{
		Token:      plain,
		ExpiresAt:  tok.ExpiresAt.UTC().Format(rfc3339Z),
		HardExpiry: tok.HardExpiry.UTC().Format(rfc3339Z),
		User: userView{
			ID:                     user.ID,
			Username:               user.Username,
			Role:                   string(user.Role),
			TOTPEnrolled:           user.TOTPSec != "",
			RecoveryCodesRemaining: recoveryRemaining(user),
		},
		TOTPEnrollmentRequired: r.deps.Auth.WebAuth.TOTPEnrollmentRequired(user),
		RecoveryCodeUsed:       res.RecoveryCodeUsed,
	})
}

// handleLogout revokes the Bearer token that authorized the request.
// Always 204 — even if the token vanished mid-flight, the client's
// next call will get a 401 and re-authenticate.
func (r *Router) handleLogout(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	token := TokenFromContext(req.Context())
	user := UserFromContext(req.Context())
	if token != "" {
		_ = r.deps.Auth.SpaAuth.Revoke(token)
	}
	if r.deps.Audit != nil && user != nil {
		_ = r.deps.Audit.Write(audit.Entry{
			Source: audit.SourceHTTP,
			Actor:  user.Username,
			UserID: user.ID,
			Action: audit.ActionSpaTokenRevoke,
		})
	}
	http.SetCookie(w, clearFilesCookie(webauth.IsSecureRequest(req)))
	w.WriteHeader(http.StatusNoContent)
}

// handleMe returns the authenticated user. Behind requireAuth so the
// 401 case is handled by the middleware before we reach here.
func (r *Router) handleMe(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	u := UserFromContext(req.Context())
	if u == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	enrolled := false
	var remaining *int
	if full, ok := r.deps.Auth.WebAuth.UserByID(u.ID); ok {
		enrolled = full.TOTPSec != ""
		remaining = recoveryRemaining(full)
	}
	WriteJSON(w, http.StatusOK, userView{
		ID:                     u.ID,
		Username:               u.Username,
		Role:                   string(u.Role),
		TOTPEnrolled:           enrolled,
		RecoveryCodesRemaining: remaining,
	})
}

// handleRefresh extends the sliding TTL of the current token without
// rotating its plaintext value. The SPA fires this from a timer ~60s
// before `expires_at`; the response carries the same token string so
// clients that miss the call until expiry can still recover by
// re-issuing the login request.
func (r *Router) handleRefresh(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	token := TokenFromContext(req.Context())
	if token == "" {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "missing token")
		return
	}
	tok, err := r.deps.Auth.SpaAuth.Refresh(token)
	if err != nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenExpired, err.Error())
		return
	}
	if r.deps.Audit != nil {
		if u := UserFromContext(req.Context()); u != nil {
			_ = r.deps.Audit.Write(audit.Entry{
				Source: audit.SourceHTTP,
				Actor:  u.Username,
				UserID: u.ID,
				Action: audit.ActionSpaTokenRefresh,
				Path:   tok.ID,
			})
		}
	}
	WriteJSON(w, http.StatusOK, refreshResponse{
		Token:      token,
		ExpiresAt:  tok.ExpiresAt.UTC().Format(rfc3339Z),
		HardExpiry: tok.HardExpiry.UTC().Format(rfc3339Z),
	})
}

// rfc3339Z is the time format the OpenAPI spec advertises. Stick to
// UTC always so the SPA never has to second-guess timezone offsets.
const rfc3339Z = "2006-01-02T15:04:05Z07:00"
