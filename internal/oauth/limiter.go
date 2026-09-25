package oauth

import (
	"sync"
	"time"
)

// ipLimiter counts events per peer inside a sliding window — the same shape
// as the API's login limiter, kept local so this package does not import
// internal/api/v1 (which imports this package). An entry exists only for a
// peer with at least one event in the window; a sweep runs once per window.
type ipLimiter struct {
	mu        sync.Mutex
	window    time.Duration
	max       int
	events    map[string][]time.Time
	lastSweep time.Time
}

func newIPLimiter(window time.Duration, max int) *ipLimiter {
	return &ipLimiter{window: window, max: max, events: map[string][]time.Time{}, lastSweep: time.Now()}
}

// allow records one event for ip and reports whether ip is still under the
// cap. Every request to a limited endpoint counts, successful or not: these
// endpoints are unauthenticated by nature and a burst of any kind is abuse.
func (l *ipLimiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.lastSweep) >= l.window {
		l.sweep(now)
	}
	cutoff := now.Add(-l.window)
	recent := l.events[ip][:0]
	for _, t := range l.events[ip] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= l.max {
		l.events[ip] = recent
		return false
	}
	l.events[ip] = append(recent, now)
	return true
}

func (l *ipLimiter) sweep(now time.Time) {
	l.lastSweep = now
	cutoff := now.Add(-l.window)
	for ip, ts := range l.events {
		recent := ts[:0]
		for _, t := range ts {
			if t.After(cutoff) {
				recent = append(recent, t)
			}
		}
		if len(recent) == 0 {
			delete(l.events, ip)
		} else {
			l.events[ip] = recent
		}
	}
}
