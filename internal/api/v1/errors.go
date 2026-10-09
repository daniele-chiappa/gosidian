// Package v1 implements the REST/JSON API consumed by the Vue 3 SPA
// (gosidian v2.0). The package is a peer of internal/server (HTML
// handlers) and internal/mcp (agent transport) — a third audience with
// its own auth flow (Bearer token, browser session) and its own
// versioning surface.
//
// Lifecycle: every endpoint accepts and emits JSON. Errors travel in a
// uniform envelope (see ErrorResponse) with stable string codes that
// the SPA maps to localized messages. Auth is enforced by middleware
// (auth.go) so handlers can assume a populated *RequestUser in the
// request context.
package v1

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

// ErrorResponse is the canonical JSON shape for non-2xx responses. The
// `code` field is a stable dotted identifier (e.g. "auth.invalid_credentials")
// that the SPA i18n layer uses to look up a localized message — the
// `message` field is a developer-readable English fallback, not meant
// for end-user display.
type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody carries the per-error fields. TraceID is best-effort; in
// dev it's empty, in production it's populated by an upstream tracing
// middleware.
type ErrorBody struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
	TraceID string         `json:"trace_id,omitempty"`
}

// Common error codes. Keep the list flat — proliferation of codes
// hurts the SPA i18n catalog more than it helps. Add a new entry only
// when an existing one would mislead.
const (
	CodeAuthInvalidCredentials = "auth.invalid_credentials"
	CodeAuthTokenInvalid       = "auth.token_invalid"
	CodeAuthTokenExpired       = "auth.token_expired"
	CodeAuthForbidden          = "auth.forbidden"
	CodeAuthOwnerOnly          = "auth.owner_only"
	// CodeAuthEnrollmentRequired gates an authenticated user whose effective
	// policy mandates TOTP but who has not enrolled a secret yet: the token is
	// valid, but every route outside the enrolment flow is refused with this
	// code so the SPA can surface the enrolment interstitial. See BUG-020.
	CodeAuthEnrollmentRequired = "auth.enrollment_required"
	// CodeAuthPasswordChangeRequired gates an account whose password the
	// owner chose (created or reset from Admin): every route but the change
	// itself is refused until it sets its own (IMP-063).
	CodeAuthPasswordChangeRequired = "auth.password_change_required"
	CodeValidationRequired         = "validation.required_field"
	CodeValidationFormat           = "validation.invalid_format"
	CodeNotFound                   = "resource.not_found"
	CodeConflict                   = "resource.conflict"
	CodeMethodNotAllowed           = "request.method_not_allowed"
	CodeRateLimit                  = "request.rate_limited"
	CodeConcurrencyEtag            = "concurrency.etag_mismatch"
	CodeServerInternal             = "server.internal_error"
	CodeServerUnavailable          = "server.unavailable"
	// CodeAuthInviteInvalid refuses a signup whose invite is unknown,
	// used or expired: the web UI says so and asks for a new link,
	// instead of a format error the invitee cannot fix (BUG-099).
	CodeAuthInviteInvalid = "auth.invite_invalid"
)

// WriteError writes a JSON ErrorResponse with the given HTTP status.
// The message is freeform but should remain stable across releases for
// any given code (the SPA i18n layer keys off code, but log scrapers
// often grep for message strings).
func WriteError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, ErrorResponse{Error: ErrorBody{Code: code, Message: message}})
}

// WriteErrorWithDetails attaches a freeform `details` map (e.g. the
// list of failing validation fields, or the current ETag on a 412).
func WriteErrorWithDetails(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	writeJSON(w, status, ErrorResponse{Error: ErrorBody{Code: code, Message: message, Details: details}})
}

// WriteJSON serialises v to the response with the given status code.
// On marshal failure it falls back to a plain 500 with a generic body
// — the original error is logged but not surfaced to keep the response
// shape predictable.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	writeJSON(w, status, v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Default().Warn("api/v1: encode response failed", "err", err, "status", status)
	}
}

// DecodeJSON parses the request body into v. Returns a clean
// validation error on malformed JSON; the caller should pass it
// directly to WriteError. Body size is capped at 1 MiB; larger
// payloads return CodeValidationFormat with hint "body too large".
func DecodeJSON(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			return errors.New("body too large (max 1 MiB)")
		}
		return err
	}
	return nil
}
