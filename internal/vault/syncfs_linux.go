package vault

import (
	"os"

	"golang.org/x/sys/unix"
)

// canSyncFS says that a batch's writes can be synced together, at its end.
const canSyncFS = true

// syncFS brings to disk every file written on the filesystem that holds
// dir, in one call: one journal commit instead of one per file.
func syncFS(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return unix.Syncfs(int(f.Fd()))
}

// deviceOf is the filesystem dir is on.
func deviceOf(dir string) (uint64, error) {
	var st unix.Stat_t
	if err := unix.Stat(dir, &st); err != nil {
		return 0, &os.PathError{Op: "stat", Path: dir, Err: err}
	}
	return uint64(st.Dev), nil //nolint:unconvert // Dev's type differs by architecture
}
