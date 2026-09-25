package v1

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Login rate-limit defaults. Mirror the v1.x web flow knobs so the
// SPA login surface fails the same way the HTML form does. Tests
// override LoginWindow / LoginMaxFailures via the package-level
// vars; in production, cmd/gosidian.main wires them from
// cfg.Webauth.LoginWindow / cfg.Webauth.LoginMaxFailures so a single
// tunable controls both audiences.
var (
	LoginWindow      = 15 * time.Minute
	LoginMaxFailures = 5
)

// loginCleanupEvery determines how often (in failed attempts) the
// limiter sweeps stale entries. Cheap, no goroutine — happens
// in-line on the failing request itself.
const loginCleanupEvery = 100

// accountLimiterKey is the bucket a username's second-factor failures land
// in. The same loginLimiter holds both per-IP and per-account buckets; the
// prefix keeps the two namespaces apart (an IP never starts with "user:").
// The account bucket is fed only by "right password, wrong code" failures,
// so a distributed guess at a 6-digit code is capped per account while a
// stranger spamming bad passwords cannot lock anyone out (IMP-062).
func accountLimiterKey(username string) string {
	return "user:" + username
}

// loginLimiter is a per-IP failed-login counter. Same shape as the
// v1.x server.loginLimiter but lives here so the api/v1 package
// stays self-contained (importing internal/server back into
// internal/api/v1 would invert the layering).
//
// Memory bound: an entry exists only for an IP with at least one failure
// inside the window. allowed() never creates entries, expired entries are
// deleted the next time they are consulted, and a full sweep runs at least
// once per window even when no failure triggers the periodic one — so a
// stream of probes that abort before authentication costs nothing.
type loginLimiter struct {
	mu        sync.Mutex
	attempts  map[string][]time.Time
	hits      int
	lastSweep time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{attempts: map[string][]time.Time{}, lastSweep: time.Now()}
}

// allowed reports whether the IP is below the failure threshold for
// the current window.
func (l *loginLimiter) allowed(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.sweepIfDue(now)
	ts, ok := l.attempts[ip]
	if !ok {
		return true
	}
	cutoff := now.Add(-LoginWindow)
	recent := ts[:0]
	for _, t := range ts {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) == 0 {
		delete(l.attempts, ip)
		return true
	}
	l.attempts[ip] = recent
	return len(recent) < LoginMaxFailures
}

// registerFail records a failed attempt and triggers periodic
// cleanup of stale IPs.
func (l *loginLimiter) registerFail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.attempts[ip] = append(l.attempts[ip], now)
	l.hits++
	if l.hits%loginCleanupEvery == 0 {
		l.sweep(now)
	}
}

// reset clears the IP's failure history. Called after a successful
// login so a typo'd attempt right before doesn't count against the
// next failed login burst.
func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, ip)
}

// sweepIfDue runs the full sweep once per window regardless of traffic, so
// entries left by a burst that then went quiet do not wait for the next
// failure to be reclaimed. Caller must hold mu.
func (l *loginLimiter) sweepIfDue(now time.Time) {
	if now.Sub(l.lastSweep) < LoginWindow {
		return
	}
	l.sweep(now)
}

// sweep prunes IPs with no recent attempts. Caller must hold mu.
func (l *loginLimiter) sweep(now time.Time) {
	l.lastSweep = now
	cutoff := now.Add(-LoginWindow)
	for ip, ts := range l.attempts {
		recent := ts[:0]
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

// ClientIP is the exported form of clientIP for sibling packages that rate
// limit unauthenticated endpoints (the OAuth server): the peer address, or
// the X-Forwarded-For origin when the peer is a trusted proxy.
func ClientIP(r *http.Request) string { return clientIP(r) }

// trustedProxies holds the networks whose X-Forwarded-For header is
// believed. Written once at startup, read on every login attempt.
var trustedProxies atomic.Pointer[[]*net.IPNet]

// SetTrustedProxies configures the reverse proxies whose X-Forwarded-For
// header clientIP honours. Entries are CIDRs or bare IPs. The default (empty)
// makes clientIP ignore the header and key on the peer address: without a
// proxy in front the header is attacker-controlled, and honouring it would
// let a brute-forcer pick a fresh rate-limit bucket on every request.
func SetTrustedProxies(cidrs []string) error {
	var nets []*net.IPNet
	for _, raw := range cidrs {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if !strings.Contains(raw, "/") {
			ip := net.ParseIP(raw)
			if ip == nil {
				return fmt.Errorf("trusted proxy %q: not an IP address or CIDR", raw)
			}
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			raw = fmt.Sprintf("%s/%d", ip, bits)
		}
		_, n, err := net.ParseCIDR(raw)
		if err != nil {
			return fmt.Errorf("trusted proxy %q: %w", raw, err)
		}
		nets = append(nets, n)
	}
	trustedProxies.Store(&nets)
	return nil
}

func isTrustedProxy(host string) bool {
	nets := trustedProxies.Load()
	if nets == nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, n := range *nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP returns the address the login limiter keys on: the peer address,
// or — only when that peer is a trusted proxy — the rightmost
// X-Forwarded-For hop that is not itself a trusted proxy. Walking from the
// right is what makes the value trustworthy: each proxy appends the address
// it saw, so everything left of the last untrusted hop was written by the
// client and can say anything.
func clientIP(r *http.Request) string {
	remote := r.RemoteAddr
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	if !isTrustedProxy(remote) {
		return remote
	}
	xff := r.Header.Get("X-Forwarded-For")
	if xff == "" {
		return remote
	}
	hops := strings.Split(xff, ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop := strings.TrimSpace(hops[i])
		if hop != "" && !isTrustedProxy(hop) {
			return hop
		}
	}
	// Every hop is one of our proxies: the request originated inside the
	// trusted perimeter and the leftmost entry is the best identity we have.
	return strings.TrimSpace(hops[0])
}
