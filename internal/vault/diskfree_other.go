//go:build !linux

package vault

// FreeBytes is -1 where the space left is not read: no reserve holds.
func FreeBytes(string) (int64, error) { return -1, nil }
