package mcp

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// tokenLimitFactor sizes a token's write cap against a session's: each MCP
// session of a token may write up to write_per_minute, all its sessions
// together up to this many times that (IMP-141). Agents running side by side
// on one token no longer take each other's budget, and a runaway loop is
// still stopped, by its own session's cap first.
const tokenLimitFactor = 5

// writeLimiter caps writes in a rolling 60s window at two levels: per MCP
// session (maxPerMinute) and per token (maxPerMinute × tokenLimitFactor).
// Writes that arrive without an MCP session (HTTP upload and append, ticket
// redemption, tests) share one session bucket of their token, so for them
// the cap is the one a token had before sessions counted. Single mutex with
// small maps, perfectly adequate for a self-hosted MCP running tens of agents
// at most; buckets left idle are swept once a minute.
type writeLimiter struct {
	maxPerMinute int
	mu           sync.Mutex
	hits         map[string][]time.Time // bucket key → recent timestamps, oldest first
	lastSweep    time.Time
}

func newWriteLimiter(maxPerMinute int) *writeLimiter {
	return &writeLimiter{
		maxPerMinute: maxPerMinute,
		hits:         make(map[string][]time.Time),
	}
}

// tokenMax is the cap shared by all the sessions of one token.
func (l *writeLimiter) tokenMax() int { return l.maxPerMinute * tokenLimitFactor }

func tokenBucket(tokenID string) string            { return "t:" + tokenID }
func sessionBucket(tokenID, session string) string { return "s:" + tokenID + "@" + session }

// WriteLimiterStats is a read-only snapshot of the limiter state for one
// session of a token. Returned by Stats() so the memory_self_stats tool can
// expose the remaining quota to the calling agent without coupling it to the
// limiter's internal maps.
type WriteLimiterStats struct {
	MaxPerMinute int `json:"max_per_minute"` // this session's cap
	Used         int `json:"used"`           // this session's writes in the window
	// Remaining is what may still be written now: the lower of the
	// session's room and the token's.
	Remaining         int   `json:"remaining"`
	OldestUnixS       int64 `json:"oldest_unix_s,omitempty"`
	TokenMaxPerMinute int   `json:"token_max_per_minute"`
	TokenUsed         int   `json:"token_used"`
}

// inWindow counts the timestamps after cutoff and returns the oldest of them.
func inWindow(ts []time.Time, cutoff time.Time) (int, time.Time) {
	n, oldest := 0, time.Time{}
	for _, t := range ts {
		if t.After(cutoff) {
			if n == 0 {
				oldest = t
			}
			n++
		}
	}
	return n, oldest
}

// prune drops, in place, the timestamps not after cutoff.
func prune(ts []time.Time, cutoff time.Time) []time.Time {
	out := ts[:0]
	for _, t := range ts {
		if t.After(cutoff) {
			out = append(out, t)
		}
	}
	return out
}

// Stats returns a snapshot of the limiter state for one session of a token
// ("" for the sessionless bucket). It does not mutate anything (no cleanup);
// callers should treat the numbers as point-in-time.
func (l *writeLimiter) Stats(tokenID, session string) WriteLimiterStats {
	if l == nil {
		return WriteLimiterStats{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-time.Minute)
	used, oldest := inWindow(l.hits[sessionBucket(tokenID, session)], cutoff)
	tokenUsed, _ := inWindow(l.hits[tokenBucket(tokenID)], cutoff)
	remaining := min(l.maxPerMinute-used, l.tokenMax()-tokenUsed)
	if remaining < 0 {
		remaining = 0
	}
	st := WriteLimiterStats{
		MaxPerMinute:      l.maxPerMinute,
		Used:              used,
		Remaining:         remaining,
		TokenMaxPerMinute: l.tokenMax(),
		TokenUsed:         tokenUsed,
	}
	if !oldest.IsZero() {
		st.OldestUnixS = oldest.Unix()
	}
	return st
}

// Allow reports whether a new write of a token's session ("" when there is
// no MCP session) may proceed, and records it when it may. When it may not,
// wait is how long until the full window frees a place, and tokenLevel says
// that the token's shared cap, not the session's, was hit. A denied request
// records nothing, so it does not extend the window.
func (l *writeLimiter) Allow(tokenID, session string) (ok bool, wait time.Duration, tokenLevel bool) {
	if l == nil || l.maxPerMinute <= 0 {
		return true, 0, false
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-time.Minute)
	l.sweep(now, cutoff)
	sk, tk := sessionBucket(tokenID, session), tokenBucket(tokenID)
	sess, tok := prune(l.hits[sk], cutoff), prune(l.hits[tk], cutoff)
	l.hits[sk], l.hits[tk] = sess, tok
	if len(sess) >= l.maxPerMinute {
		return false, sess[0].Add(time.Minute).Sub(now), false
	}
	if len(tok) >= l.tokenMax() {
		return false, tok[0].Add(time.Minute).Sub(now), true
	}
	l.hits[sk], l.hits[tk] = append(sess, now), append(tok, now)
	return true, 0, false
}

// sweep drops the buckets with no write inside the window, at most once a
// minute: a session's bucket outlives the session otherwise.
func (l *writeLimiter) sweep(now, cutoff time.Time) {
	if now.Sub(l.lastSweep) < time.Minute {
		return
	}
	l.lastSweep = now
	for k, ts := range l.hits {
		if len(ts) == 0 || !ts[len(ts)-1].After(cutoff) {
			delete(l.hits, k)
		}
	}
}

// retrySeconds rounds a wait up to whole seconds, at least one: the number an
// agent is told to wait and the Retry-After header of an HTTP refusal.
func retrySeconds(wait time.Duration) int {
	return max(1, int(math.Ceil(wait.Seconds())))
}

// writeCharge is one ingestion's place in the write rate: the first write
// takes it, and the steps that follow (the attachment, the note that shows
// it, the notes of a package) write under it. A ticket's upload comes with
// it taken, by the memory_ingest call that minted the ticket. An upload by
// ticket counted three times, at the mint, at the redeem and at the write,
// and the third refusal lost bytes already uploaded with a ticket already
// spent (IMP-161, S3-7).
type writeCharge struct{ taken atomic.Bool }

type writeChargeKey struct{}

// withWriteCharge starts an ingestion's charge, taken already for the
// upload of a ticket.
func withWriteCharge(ctx context.Context, taken bool) context.Context {
	c := &writeCharge{}
	c.taken.Store(taken)
	return context.WithValue(ctx, writeChargeKey{}, c)
}

func writeChargeOf(ctx context.Context) *writeCharge {
	c, _ := ctx.Value(writeChargeKey{}).(*writeCharge)
	return c
}
