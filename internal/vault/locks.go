package vault

import (
	"path"
	"slices"
	"sync"
)

// LockPath acquires an exclusive in-process lock for the canonical form of
// the given vault-relative path and returns the unlock func. Callers wrap
// their whole load→validate→save sequence (optimistic-locking checks
// included) so that if_match/If-Match behaves as a true compare-and-swap
// instead of a check-then-write race between concurrent agents.
//
// The lock only serializes writers inside this process (MCP tools and the
// HTTP API share the same Vault); git sync asks PathBusy whether a writer
// holds it, and direct file edits on the host are out of scope. Entries are
// never evicted: the key set is bounded by the number of distinct note
// paths and a bare mutex is tiny, so eviction bookkeeping would cost more
// than it saves.
func (v *Vault) LockPath(path string) (unlock func()) {
	m := v.pathMutex(v.lockKey(path))
	m.Lock()
	return m.Unlock
}

// LockPaths takes the locks of several paths, each once, in the order of
// their canonical forms: two callers naming the same paths the other way
// round never wait for each other forever. A rename locks both its ends, a
// project renamed both its names (BUG-147). The unlock func lets them all
// go.
func (v *Vault) LockPaths(paths ...string) (unlock func()) {
	keys := make([]string, len(paths))
	for i, p := range paths {
		keys[i] = v.lockKey(p)
	}
	slices.Sort(keys)
	keys = slices.Compact(keys)
	held := make([]*sync.Mutex, len(keys))
	for i, k := range keys {
		held[i] = v.pathMutex(k)
		held[i].Lock()
	}
	return func() {
		for i := len(held) - 1; i >= 0; i-- {
			held[i].Unlock()
		}
	}
}

// PathBusy says whether a writer holds the lock of rel now, or of a folder
// above it: a project renamed or deleted holds its name's. Git sync asks it
// of the files it commits, and leaves a busy one for the commit after the
// writer's record (BUG-147). Asking holds a lock for no longer than the
// asking; a path never locked is free.
func (v *Vault) PathBusy(rel string) bool {
	for p := v.lockKey(rel); p != "." && p != "/"; p = path.Dir(p) {
		mu, ok := v.locks.Load(p)
		if !ok {
			continue
		}
		m := mu.(*sync.Mutex)
		if !m.TryLock() {
			return true
		}
		m.Unlock()
	}
	return false
}

// lockKey is the canonical form of p that its lock goes by.
func (v *Vault) lockKey(p string) string {
	if r, err := v.Rel(p); err == nil {
		return r
	}
	return p
}

func (v *Vault) pathMutex(key string) *sync.Mutex {
	mu, _ := v.locks.LoadOrStore(key, &sync.Mutex{})
	return mu.(*sync.Mutex)
}
