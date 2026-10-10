package vault

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ReadAttachment reads an attachment's bytes for the server's own use (a
// pipeline node sending an image to a model, IMP-167): only under an
// attachments/ folder, a regular file, no link along the way out of the
// vault, at most max bytes. The vault layer still never returns attachment
// bytes as a note (ADR-021).
func (v *Vault) ReadAttachment(rel string, max int64) ([]byte, error) {
	r, err := v.Rel(rel)
	if err != nil {
		return nil, err
	}
	if !strings.Contains("/"+r, "/attachments/") {
		return nil, fmt.Errorf("%q is not an attachment (it is not under attachments/)", r)
	}
	full := filepath.Join(v.Root, filepath.FromSlash(r))
	st, err := os.Lstat(full)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || !v.resolvesInside(full) {
		return nil, fs.ErrNotExist
	}
	if st.Size() > max {
		return nil, fmt.Errorf("%q is %d bytes, over %d", r, st.Size(), max)
	}
	return os.ReadFile(full)
}
