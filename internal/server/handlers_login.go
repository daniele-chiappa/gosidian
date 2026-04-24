package server

import (
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gosidian/gosidian/internal/webauth"
)

const (
	loginCleanupEvery = 100
)

// Login rate-limit / session tuning: set via Config.Webauth (+ GOSIDIAN_LOGIN_*
// env vars), falling back to sane defaults matching the historical constants
// used before v1.7.
var (
	loginSessionTTL  = 24 * time.Hour
	loginWindow      = 15 * time.Minute
	loginMaxFailures = 5
)

// ConfigureLogin wires the dynamic parameters from config into the package-
// level defaults used by the login rate limiter + session TTL. Called by
// main.go after Config.ApplyEnv().
func ConfigureLogin(sessionTTL, window time.Duration, maxFailures int) {
	if sessionTTL > 0 {
		loginSessionTTL = sessionTTL
	}
	if window > 0 {
		loginWindow = window
	}
	if maxFailures > 0 {
		loginMaxFailures = maxFailures
	}
}

// loginLimiter is a minimal per-IP failed-login counter. It's intentionally
// lightweight: a map protected by a mutex with lazy cleanup. Single-user
// deployment doesn't need Redis.
type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
	hits     int // for periodic cleanup
}

func (l *loginLimiter) RegisterFail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.attempts == nil {
		l.attempts = map[string][]time.Time{}
	}
	now := time.Now()
	l.attempts[ip] = append(l.attempts[ip], now)
	l.hits++
	if l.hits%loginCleanupEvery == 0 {
		l.sweep(now)
	}
}

// Allowed returns true if the IP is under the limit.
func (l *loginLimiter) Allowed(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.attempts == nil {
		return true
	}
	cutoff := time.Now().Add(-loginWindow)
	var recent []time.Time
	for _, t := range l.attempts[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	l.attempts[ip] = recent
	return len(recent) < loginMaxFailures
}

// Reset clears failures for an IP after a successful login.
func (l *loginLimiter) Reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, ip)
}

func (l *loginLimiter) sweep(now time.Time) {
	cutoff := now.Add(-loginWindow)
	for ip, ts := range l.attempts {
		var recent []time.Time
		for _, t := range ts {
			if t.After(cutoff) {
				recent = append(recent, t)
			}
		}
		if len(recent) == 0 {
			delete(l.attempts, ip)
		} else {
			l.attempts[ip] = recent
		}
	}
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// First IP in the chain is the original client.
		for i, c := range xff {
			if c == ',' {
				return trimSpace(xff[:i])
			}
		}
		return trimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

// handleLogin renders the login form on GET and processes credentials on POST.
// When the web auth store is nil or disabled, it redirects to the home page
// since there's nothing to log into.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.webauth == nil || !s.webauth.Enabled() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// "next" redirect target after successful login
	next := r.URL.Query().Get("next")
	if next == "" {
		next = r.FormValue("next")
	}
	if next == "" || next == "/login" {
		next = "/"
	}

	switch r.Method {
	case http.MethodGet:
		s.renderLogin(w, r, next, "")
	case http.MethodPost:
		ip := clientIP(r)
		if !s.loginFails.Allowed(ip) {
			s.renderLoginStatus(w, r, next, "Troppi tentativi falliti, riprova più tardi.", http.StatusTooManyRequests)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		username := r.FormValue("username")
		password := r.FormValue("password")
		totpCode := r.FormValue("totp")

		user, err := s.webauth.Verify(username, password, totpCode)
		if err != nil {
			s.loginFails.RegisterFail(ip)
			s.renderLoginStatus(w, r, next, "Credenziali non valide.", http.StatusUnauthorized)
			return
		}
		// Success: create session + set cookie + redirect
		id, err := s.webauth.CreateSession(user.ID, loginSessionTTL)
		if err != nil {
			http.Error(w, "session create: "+err.Error(), http.StatusInternalServerError)
			return
		}
		s.loginFails.Reset(ip)
		http.SetCookie(w, webauth.SessionCookie(id, loginSessionTTL, webauth.IsSecureRequest(r)))

		// Validate the next URL is a local redirect to avoid open-redirect.
		if !safeNext(next) {
			next = "/"
		}
		http.Redirect(w, r, next, http.StatusSeeOther)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(webauth.SessionCookieName); err == nil && s.webauth != nil {
		s.webauth.RevokeSession(c.Value)
	}
	http.SetCookie(w, webauth.ClearCookie(webauth.IsSecureRequest(r)))
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// safeNext rejects anything that isn't a local absolute path, preventing
// ?next=//evil.com open-redirect tricks.
func safeNext(next string) bool {
	if next == "" || next[0] != '/' {
		return false
	}
	if len(next) > 1 && next[1] == '/' {
		return false // protocol-relative URL
	}
	// Ensure it parses as a relative URL with no host
	u, err := url.Parse(next)
	if err != nil {
		return false
	}
	return u.Host == "" && u.Scheme == ""
}

func (s *Server) renderLogin(w http.ResponseWriter, r *http.Request, next, errMsg string) {
	s.renderLoginStatus(w, r, next, errMsg, http.StatusOK)
}

func (s *Server) renderLoginStatus(w http.ResponseWriter, r *http.Request, next, errMsg string, status int) {
	data := map[string]any{
		"Title":       "Accedi",
		"Next":        next,
		"Error":       errMsg,
		"TOTPEnabled": s.webauth != nil && s.webauth.TOTPEnabled(),
		"Username":    "",
		// Suppress the sidebar on the login page: it would otherwise fire
		// hx-get=/api/tree which the auth middleware rejects with
		// HX-Redirect=/login, causing an infinite reload loop.
		"NoSidebar": true,
	}
	if r.Method == http.MethodPost {
		data["Username"] = r.FormValue("username")
	}
	// Use renderPage so i18n injection + status handling stay in one place.
	if status != http.StatusOK {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
	}
	s.renderPage(w, r, "login.html", data)
}
