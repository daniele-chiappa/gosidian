package vault

import (
	"container/list"
	"sync"
	"time"
)

// loadCache is an LRU cache for notes returned by Vault.Load. Entries are
// validated against the filesystem mtime + size on each Get, so external
// writes (via the editor, fsnotify watcher, git pull) are never served stale.
// Writes through the vault invalidate the corresponding entry explicitly.
type loadCache struct {
	mu      sync.Mutex
	max     int
	entries map[string]*cacheEntry
	lru     *list.List // front = most recently used
}

type cacheEntry struct {
	path string
	note *Note
	elem *list.Element
}

func newLoadCache(max int) *loadCache {
	if max <= 0 {
		max = 128
	}
	return &loadCache{
		max:     max,
		entries: make(map[string]*cacheEntry),
		lru:     list.New(),
	}
}

// Get returns the cached note if it exists AND its stored mtime/size match
// the filesystem values passed in. On stale, the entry is evicted (so the
// caller can put the fresh value). Returns nil on miss.
func (c *loadCache) Get(path string, currentModTime time.Time, currentSize int64) *Note {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[path]
	if !ok {
		return nil
	}
	if !e.note.ModTime.Equal(currentModTime) || e.note.Size != currentSize {
		// Stale — evict and miss.
		c.lru.Remove(e.elem)
		delete(c.entries, path)
		return nil
	}
	c.lru.MoveToFront(e.elem)
	return e.note
}

// Put inserts or updates an entry and evicts the oldest if over capacity.
func (c *loadCache) Put(path string, note *Note) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[path]; ok {
		e.note = note
		c.lru.MoveToFront(e.elem)
		return
	}
	e := &cacheEntry{path: path, note: note}
	e.elem = c.lru.PushFront(e)
	c.entries[path] = e
	for c.lru.Len() > c.max {
		back := c.lru.Back()
		if back == nil {
			break
		}
		victim := back.Value.(*cacheEntry)
		c.lru.Remove(back)
		delete(c.entries, victim.path)
	}
}

// Invalidate drops any entry for path. Safe to call on a missing path.
func (c *loadCache) Invalidate(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[path]; ok {
		c.lru.Remove(e.elem)
		delete(c.entries, path)
	}
}

// Len is for tests / metrics.
func (c *loadCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lru.Len()
}
