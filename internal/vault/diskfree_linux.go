package vault

import (
	"os"

	"golang.org/x/sys/unix"
)

// FreeBytes is the space left for the server on the filesystem that holds
// dir: the blocks an unprivileged writer may still take.
func FreeBytes(dir string) (int64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, &os.PathError{Op: "statfs", Path: dir, Err: err}
	}
	return int64(st.Bavail) * int64(st.Bsize), nil //nolint:unconvert // the field types differ by architecture
}
