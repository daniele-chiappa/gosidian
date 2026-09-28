// Package ratelimit holds the sliding-window counter shared by the
// endpoints that cap how often a peer or an account may call them: the
// OAuth endpoints (per client IP) and the zip exports (per account). It is
// its own package so that neither internal/oauth nor internal/api/v1 has
// to import the other for it.
package ratelimit

import (
	"sync"
	"time"
)

// Window counts events per key inside a sliding window. An entry exists
// only for a key with at least one event in the window; a sweep runs once
// per window.
type Window struct {
	mu        sync.Mutex
	window    time.Duration
	max       int
	events    map[string][]time.Time
	lastSweep time.Time
}

// New returns a Window allowing max events per key within window.
func New(window time.Duration, max int) *Window {
	return &Window{window: window, max: max, events: map[string][]time.Time{}, lastSweep: time.Now()}
}

// Allow records one event for key and reports whether key is still under
// the cap. A refused event is not recorded, so a key that keeps retrying
// gets through again as soon as its oldest event leaves the window.
func (l *Window) Allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.lastSweep) >= l.window {
		l.sweep(now)
	}
	cutoff := now.Add(-l.window)
	recent := l.events[key][:0]
	for _, t := range l.events[key] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= l.max {
		l.events[key] = recent
		return false
	}
	l.events[key] = append(recent, now)
	return true
}

func (l *Window) sweep(now time.Time) {
	l.lastSweep = now
	cutoff := now.Add(-l.window)
	for key, ts := range l.events {
		recent := ts[:0]
		for _, t := range ts {
			if t.After(cutoff) {
				recent = append(recent, t)
			}
		}
		if len(recent) == 0 {
			delete(l.events, key)
		} else {
			l.events[key] = recent
		}
	}
}
