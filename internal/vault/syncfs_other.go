//go:build !linux

package vault

// canSyncFS is false without syncfs: a batch's writes are synced one by
// one, as with durability always.
const canSyncFS = false

func syncFS(string) error { return nil }

func deviceOf(string) (uint64, error) { return 0, nil }
