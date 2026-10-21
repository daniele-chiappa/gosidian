package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Durability is when the vault's writes reach the disk ([vault]
// durability, IMP-185). A write is a temporary file renamed over the
// target either way, so a note is never half written; durability says
// whether the bytes are synced before the write returns, which is what
// survives a power loss.
type Durability int

const (
	// DurableBatch syncs each note and attachment written on its own, as
	// DurableAlways, but the writes of a batch, a pipeline step, only once
	// at its end (SyncBatch): an fsync per note was 39% of an import's time.
	// The default.
	DurableBatch Durability = iota
	// DurableAlways syncs every write before it returns.
	DurableAlways
	// DurableNone never syncs: the system writes the files back when it
	// likes, and a power loss can take the last seconds of writes.
	DurableNone
)

// ParseDurability reads a [vault] durability value: always, batch (also
// the empty value) or none.
func ParseDurability(s string) (Durability, error) {
	switch s {
	case "", "batch":
		return DurableBatch, nil
	case "always":
		return DurableAlways, nil
	case "none":
		return DurableNone, nil
	}
	return DurableBatch, fmt.Errorf("%q is no durability (always, batch or none)", s)
}

func (d Durability) String() string {
	switch d {
	case DurableAlways:
		return "always"
	case DurableNone:
		return "none"
	}
	return "batch"
}

// SetDurability sets when the vault's writes reach the disk.
func (v *Vault) SetDurability(d Durability) { v.durability = d }

// batchWrites holds the folders a batch wrote into without syncing, until
// SyncBatch.
type batchWrites struct {
	mu   sync.Mutex
	dirs map[string]bool
}

// syncs says whether a write is synced on its own: batched is a write of a
// batch, which SyncBatch syncs at its end where the platform can.
func (v *Vault) syncs(batched bool) bool {
	switch v.durability {
	case DurableNone:
		return false
	case DurableBatch:
		return !batched || !canSyncFS
	}
	return true
}

// wrote records a batched write that was not synced, for SyncBatch.
func (v *Vault) wrote(abs string, synced bool) {
	if synced || v.durability != DurableBatch {
		return
	}
	v.batch.mu.Lock()
	defer v.batch.mu.Unlock()
	if v.batch.dirs == nil {
		v.batch.dirs = map[string]bool{}
	}
	v.batch.dirs[filepath.Dir(abs)] = true
}

// SyncBatch brings to disk what the batches wrote without syncing: one
// syncfs for each filesystem they wrote into (a project can be a mount of
// its own). Another batch's writes may come along, which does no harm. A
// pipeline step calls it before it counts as done.
func (v *Vault) SyncBatch() error {
	v.batch.mu.Lock()
	dirs := v.batch.dirs
	v.batch.dirs = nil
	v.batch.mu.Unlock()
	if len(dirs) == 0 {
		return nil
	}
	done := map[uint64]bool{}
	for dir := range dirs {
		dev, err := deviceOf(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue // removed since: nothing of it to sync
			}
			v.keepForSync(dirs)
			return err
		}
		if done[dev] {
			continue
		}
		if err := syncFS(dir); err != nil {
			v.keepForSync(dirs)
			return fmt.Errorf("sync %s: %w", dir, err)
		}
		done[dev] = true
	}
	return nil
}

// keepForSync puts dirs back for the next SyncBatch, after one that failed.
func (v *Vault) keepForSync(dirs map[string]bool) {
	v.batch.mu.Lock()
	defer v.batch.mu.Unlock()
	if v.batch.dirs == nil {
		v.batch.dirs = map[string]bool{}
	}
	for d := range dirs {
		v.batch.dirs[d] = true
	}
}
