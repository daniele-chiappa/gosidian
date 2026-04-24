package mcp

import (
	"sync"
	"time"
)

// writeLimiter caps how many writes a token may issue in a rolling 60s window.
// Single mutex with a tiny map; perfectly adequate for a self-hosted MCP
// running tens of agents at most. Lazy cleanup on each Allow() call.
type writeLimiter struct {
	maxPerMinute int
	mu           sync.Mutex
	hits         map[string][]time.Time // token id → recent timestamps
}

func newWriteLimiter(maxPerMinute int) *writeLimiter {
	return &writeLimiter{
		maxPerMinute: maxPerMinute,
		hits:         make(map[string][]time.Time),
	}
}

// WriteLimiterStats is a read-only snapshot of the limiter state for a given
// token. Returned by Stats() so the memory_self_stats tool can expose the
// remaining quota to the calling agent without coupling it to the limiter's
// internal map.
type WriteLimiterStats struct {
	MaxPerMinute int   `json:"max_per_minute"`
	Used         int   `json:"used"`
	Remaining    int   `json:"remaining"`
	OldestUnixS  int64 `json:"oldest_unix_s,omitempty"`
}

// Stats returns a snapshot of the limiter state for a single token. It does
// not mutate anything (no cleanup); callers should treat the numbers as
// point-in-time.
func (l *writeLimiter) Stats(tokenID string) WriteLimiterStats {
	if l == nil {
		return WriteLimiterStats{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-time.Minute)
	var oldest int64
	used := 0
	for _, t := range l.hits[tokenID] {
		if t.After(cutoff) {
			if oldest == 0 || t.Unix() < oldest {
				oldest = t.Unix()
			}
			used++
		}
	}
	remaining := l.maxPerMinute - used
	if remaining < 0 {
		remaining = 0
	}
	return WriteLimiterStats{
		MaxPerMinute: l.maxPerMinute,
		Used:         used,
		Remaining:    remaining,
		OldestUnixS:  oldest,
	}
}

// Allow returns true if a new write may proceed. It records the attempt only
// when allowed (so a denied request doesn't itself extend the window).
func (l *writeLimiter) Allow(tokenID string) bool {
	if l == nil || l.maxPerMinute <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-time.Minute)
	recent := l.hits[tokenID][:0]
	for _, t := range l.hits[tokenID] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= l.maxPerMinute {
		l.hits[tokenID] = recent
		return false
	}
	l.hits[tokenID] = append(recent, time.Now())
	return true
}
