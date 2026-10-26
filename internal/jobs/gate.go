package jobs

import (
	"context"
	"sort"
	"sync"
	"time"
)

// The resources of the steps (IMP-196): a plugin, a provider of language
// models, or a resource several of them share, such as one GPU. Each has a
// limit of calls at once; a step or a call that would pass one waits, the
// step in the queue, without a worker.

// gateLimitTTL is how long the gate keeps a resource's limit before it
// asks again: the claim checks every step it reads.
const gateLimitTTL = 5 * time.Second

// Gate counts the calls under way of each resource. The process is the
// only one using the jobs database, so the count in memory is the count:
// after a restart nothing runs, and the steps that ran are queued again.
type Gate struct {
	limit func(resource string) int
	now   func() time.Time

	mu     sync.Mutex
	used   map[string]int
	limits map[string]cachedLimit
	// freed is closed and made again at every release, for Take.
	freed chan struct{}
	// onRelease is told that a place was freed (the runner's Wake).
	onRelease func()
}

type cachedLimit struct {
	n  int
	at time.Time
}

// NewGate is a gate whose resources have the limits limit gives: 0 or
// less for none.
func NewGate(limit func(resource string) int) *Gate {
	return &Gate{limit: limit, now: time.Now, used: map[string]int{}, limits: map[string]cachedLimit{}, freed: make(chan struct{})}
}

// SetLimit sets where the limits come from, once the plugins and the
// providers are known; the limits read before are forgotten.
func (g *Gate) SetLimit(limit func(resource string) int) {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.limit, g.limits = limit, map[string]cachedLimit{}
	g.mu.Unlock()
}

// Limit is the limit of resource, 0 for none.
func (g *Gate) Limit(resource string) int {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.limitLocked(resource)
}

func (g *Gate) limitLocked(resource string) int {
	now := g.now()
	if c, ok := g.limits[resource]; ok && now.Sub(c.at) < gateLimitTTL {
		return c.n
	}
	n := 0
	if g.limit != nil {
		n = max(g.limit(resource), 0)
	}
	g.limits[resource] = cachedLimit{n: n, at: now}
	return n
}

// Full is the first of resources with no place left, "" when each has one.
func (g *Gate) Full(resources []string) string {
	if g == nil {
		return ""
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.fullLocked(resources)
}

func (g *Gate) fullLocked(resources []string) string {
	for _, r := range resources {
		if n := g.limitLocked(r); n > 0 && g.used[r] >= n {
			return r
		}
	}
	return ""
}

// TryTake takes a place in each of resources, or in none of them when one
// is full.
func (g *Gate) TryTake(resources []string) bool {
	if g == nil || len(resources) == 0 {
		return true
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.fullLocked(resources) != "" {
		return false
	}
	for _, r := range resources {
		g.used[r]++
	}
	return true
}

// Take waits for a place in each of resources until ctx ends.
func (g *Gate) Take(ctx context.Context, resources []string) error {
	if g == nil || len(resources) == 0 {
		return nil
	}
	for {
		g.mu.Lock()
		if g.fullLocked(resources) == "" {
			for _, r := range resources {
				g.used[r]++
			}
			g.mu.Unlock()
			return nil
		}
		freed := g.freed
		g.mu.Unlock()
		// A limit may change too: look again now and then.
		t := time.NewTimer(gateLimitTTL)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-freed:
		case <-t.C:
		}
		t.Stop()
	}
}

// Release gives back the places of resources.
func (g *Gate) Release(resources []string) {
	if g == nil || len(resources) == 0 {
		return
	}
	g.mu.Lock()
	for _, r := range resources {
		if g.used[r] > 0 {
			g.used[r]--
		}
		if g.used[r] == 0 {
			delete(g.used, r)
		}
	}
	close(g.freed)
	g.freed = make(chan struct{})
	on := g.onRelease
	g.mu.Unlock()
	if on != nil {
		on()
	}
}

// Running is the places taken now, by resource, in name order.
func (g *Gate) Running() []ResourceUse {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]ResourceUse, 0, len(g.used))
	for r, n := range g.used {
		out = append(out, ResourceUse{Resource: r, Running: n, Limit: g.limitLocked(r)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Resource < out[j].Resource })
	return out
}

// ResourceUse is a resource's places taken and its limit (0: none).
type ResourceUse struct {
	Resource string `json:"resource"`
	Running  int    `json:"running"`
	Limit    int    `json:"limit"`
}
