package server

import (
	"net/http"
	"net/url"
	"strings"
	"time"
)

// handleI18nSet switches the preferred UI language by setting a persistent
// cookie and redirecting back to the caller. Accepts the target language via
// `lang` query string; a `next` query string is honoured when safe (local
// absolute path), otherwise fallback to /.
func (s *Server) handleI18nSet(w http.ResponseWriter, r *http.Request) {
	lang := strings.TrimSpace(r.URL.Query().Get("lang"))
	if lang == "" {
		http.Error(w, "lang required", http.StatusBadRequest)
		return
	}
	// Narrow to the primary tag to keep the cookie simple (it/en, not it-IT).
	if i := strings.IndexByte(lang, '-'); i >= 0 {
		lang = lang[:i]
	}
	lang = strings.ToLower(lang)

	next := r.URL.Query().Get("next")
	if !safeNext(next) {
		next = "/"
	}

	// Cookie TTL: 1 year is plenty for a UI preference; refreshed on every
	// explicit user switch.
	http.SetCookie(w, &http.Cookie{
		Name:     langCookieName,
		Value:    lang,
		Path:     "/",
		Expires:  time.Now().Add(365 * 24 * time.Hour),
		HttpOnly: false, // readable by JS if we ever add a client-side switch
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// compile-time reference so gofmt keeps net/url imported (used by safeNext in
// handlers_login.go — shared).
var _ = url.Parse
