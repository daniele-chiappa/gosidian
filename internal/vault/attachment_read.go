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
	r, full, st, err := v.attachmentFile(rel)
	if err != nil {
		return nil, err
	}
	if st.Size() > max {
		return nil, fmt.Errorf("%q is %d bytes, over %d", r, st.Size(), max)
	}
	return os.ReadFile(full)
}

// OpenAttachment opens an attachment for reading, under the rules of
// ReadAttachment: /vault-files/ serves it (BUG-118: a link under an
// attachments/ folder led out of the vault), a plugin fetches it by
// reference. It returns the file, what it is and its cleaned path.
func (v *Vault) OpenAttachment(rel string) (*os.File, fs.FileInfo, string, error) {
	r, full, st, err := v.attachmentFile(rel)
	if err != nil {
		return nil, nil, "", err
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, nil, "", err
	}
	return f, st, r, nil
}

// attachmentFile is the attachment at rel: its cleaned path, its absolute
// path and what it is, refused when it is not under an attachments/ folder
// (or is in an IN folder), not a regular file, or reached through a link.
func (v *Vault) attachmentFile(rel string) (string, string, fs.FileInfo, error) {
	r, err := v.Rel(rel)
	if err != nil {
		return "", "", nil, err
	}
	if !strings.Contains("/"+r, "/attachments/") || v.InArea(r) {
		return "", "", nil, fmt.Errorf("%q is not an attachment (it is not under attachments/)", r)
	}
	full := filepath.Join(v.Root, filepath.FromSlash(r))
	st, err := os.Lstat(full)
	if err != nil {
		return "", "", nil, err
	}
	if !st.Mode().IsRegular() || !v.resolvesInside(full) {
		return "", "", nil, fs.ErrNotExist
	}
	return r, full, st, nil
}
