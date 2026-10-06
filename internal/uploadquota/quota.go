// Package uploadquota caps the bytes an account uploads in a sliding window
// (IMP-034). The per-file cap and the write rate limiter bound one upload
// and how many: neither stops an account (or a runaway agent on one of its
// tokens) from filling the disk at the pace they allow. The quota is off by
// default; the operator sets it ([uploads] quota_bytes).
//
// The count lives in memory and starts again at every restart. Bytes count
// as they arrive, also when the attachment was already stored (attachments
// are content-addressed): the quota measures what the account sends.
package uploadquota

import (
	"fmt"
	"sync"
	"time"
)

// DefaultWindow is the window of a quota that does not set one.
const DefaultWindow = 24 * time.Hour

// Quota is the shared upload budget, per account. A nil *Quota, or one
// with max <= 0, never refuses.
type Quota struct {
	max    int64
	window time.Duration
	now    func() time.Time

	mu   sync.Mutex
	used map[string][]entry
}

type entry struct {
	at time.Time
	n  int64
}

// New returns a quota of max bytes per window for each account; max <= 0
// means no limit, window <= 0 the default window.
func New(max int64, window time.Duration) *Quota {
	if window <= 0 {
		window = DefaultWindow
	}
	return &Quota{max: max, window: window, now: time.Now, used: map[string][]entry{}}
}

// Enabled reports whether the quota limits anything.
func (q *Quota) Enabled() bool { return q != nil && q.max > 0 }

// Max is the bytes allowed per window, 0 when there is no limit.
func (q *Quota) Max() int64 {
	if !q.Enabled() {
		return 0
	}
	return q.max
}

// Window is the length of the sliding window.
func (q *Quota) Window() time.Duration {
	if q == nil {
		return DefaultWindow
	}
	return q.window
}

// Key names the account a request counts against: the user, or the token
// itself when it has no owning account (a CLI token).
func Key(userID, tokenID string) string {
	if userID != "" {
		return "user:" + userID
	}
	return "token:" + tokenID
}

// Refusal is a refused reservation: what the account used in the window,
// and how long until n bytes would fit (0 when they never will, n alone
// being over the quota).
type Refusal struct {
	Used, Max, N int64
	Window       time.Duration
	Wait         time.Duration
}

func (r *Refusal) Error() string {
	if r.Wait == 0 {
		return fmt.Sprintf("upload quota: %d bytes are more than the %d bytes an account may upload in %s", r.N, r.Max, r.Window)
	}
	return fmt.Sprintf("upload quota exceeded: this account uploaded %d of %d bytes in the last %s; %d more fit in %ds. Nothing was stored",
		r.Used, r.Max, r.Window, r.N, int((r.Wait+time.Second-1)/time.Second))
}

// Reserve counts n bytes for key when they fit in its window, or returns
// why not. The caller gives them back with Refund when the upload fails.
func (q *Quota) Reserve(key string, n int64) *Refusal {
	if !q.Enabled() || n <= 0 {
		return nil
	}
	now := q.now()
	q.mu.Lock()
	defer q.mu.Unlock()
	entries := q.prune(key, now)
	var sum int64
	for _, e := range entries {
		sum += e.n
	}
	if sum+n <= q.max {
		q.used[key] = append(entries, entry{at: now, n: n})
		return nil
	}
	r := &Refusal{Used: sum, Max: q.max, N: n, Window: q.window}
	if n > q.max {
		return r
	}
	// The oldest uploads leave the window first: n fits once enough of
	// them have.
	freed := int64(0)
	for _, e := range entries {
		freed += e.n
		if sum-freed+n <= q.max {
			r.Wait = e.at.Add(q.window).Sub(now)
			break
		}
	}
	return r
}

// Refund gives back n bytes of key's latest reservations: the upload they
// were for did not happen.
func (q *Quota) Refund(key string, n int64) {
	if !q.Enabled() || n <= 0 {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	entries := q.used[key]
	for i := len(entries) - 1; i >= 0 && n > 0; i-- {
		take := min(n, entries[i].n)
		entries[i].n -= take
		n -= take
	}
	kept := entries[:0]
	for _, e := range entries {
		if e.n > 0 {
			kept = append(kept, e)
		}
	}
	q.used[key] = kept
}

// prune drops key's entries older than the window; q.mu is held.
func (q *Quota) prune(key string, now time.Time) []entry {
	entries := q.used[key]
	cut := 0
	for cut < len(entries) && !entries[cut].at.Add(q.window).After(now) {
		cut++
	}
	entries = entries[cut:]
	if len(entries) == 0 {
		delete(q.used, key)
		return nil
	}
	q.used[key] = entries
	return entries
}
