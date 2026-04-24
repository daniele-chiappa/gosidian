// Package trash implements an opt-in soft-delete bin for vault notes and
// projects. When the user enables it via [trash] in config.toml the
// HTTP/MCP delete handlers route through Bin.Discard* instead of removing
// files from disk. A Bin scoped to a specific vault keeps everything
// under <vault>/.gosidian/trash/<timestamp>-<sanitized-path>/.
package trash

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Bin operates on a vault root + a hidden trash directory inside it.
type Bin struct {
	vaultRoot string
	dir       string
	retention time.Duration
}

// New builds a Bin pinned to the given vault. The trash directory is
// created on first write. retention < 0 disables auto-pruning.
func New(vaultRoot string, retention time.Duration) *Bin {
	return &Bin{
		vaultRoot: vaultRoot,
		dir:       filepath.Join(vaultRoot, ".gosidian", "trash"),
		retention: retention,
	}
}

// DiscardNote moves a single note into the trash. Returns the trash-relative
// id (timestamp-prefixed name) so callers can audit it.
func (b *Bin) DiscardNote(rel string) (string, error) {
	src := filepath.Join(b.vaultRoot, filepath.FromSlash(rel))
	if _, err := os.Stat(src); err != nil {
		return "", err
	}
	id := newID(rel)
	dst := filepath.Join(b.dir, id)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if err := os.Rename(src, dst); err != nil {
		// fallback: copy + remove (cross-device)
		if err := copyFile(src, dst); err != nil {
			return "", err
		}
		_ = os.Remove(src)
	}
	return id, nil
}

// DiscardProject moves an entire project directory (recursively) into trash.
func (b *Bin) DiscardProject(name string) (string, []string, error) {
	src := filepath.Join(b.vaultRoot, name)
	st, err := os.Stat(src)
	if err != nil {
		return "", nil, err
	}
	if !st.IsDir() {
		return "", nil, errors.New("not a directory")
	}

	// Collect note paths so the caller can clean the index afterwards.
	var notes []string
	_ = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(p), ".md") {
			return nil
		}
		rel, _ := filepath.Rel(b.vaultRoot, p)
		notes = append(notes, filepath.ToSlash(rel))
		return nil
	})

	id := newID(name)
	dst := filepath.Join(b.dir, id)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", nil, err
	}
	if err := os.Rename(src, dst); err != nil {
		return "", nil, err
	}
	return id, notes, nil
}

// Entry is one item in the trash listing.
type Entry struct {
	ID         string    // filename inside the trash dir
	OriginPath string    // best-effort original vault-relative path
	DiscardedAt time.Time
	IsDir      bool
}

// List returns all current trash entries, newest first.
func (b *Bin) List() ([]Entry, error) {
	entries, err := os.ReadDir(b.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		id := e.Name()
		ts, origin := parseID(id)
		info, _ := e.Info()
		isDir := false
		if info != nil {
			isDir = info.IsDir()
		}
		out = append(out, Entry{
			ID:          id,
			OriginPath:  origin,
			DiscardedAt: ts,
			IsDir:       isDir,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].DiscardedAt.After(out[j].DiscardedAt)
	})
	return out, nil
}

// Restore moves an entry back to its original location. The caller is
// expected to reindex what comes back. Returns the list of vault-relative
// .md paths that were restored (single entry for notes, multi for projects).
func (b *Bin) Restore(id string) ([]string, error) {
	src := filepath.Join(b.dir, id)
	st, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	_, origin := parseID(id)
	if origin == "" {
		return nil, errors.New("cannot determine original path from id")
	}
	dst := filepath.Join(b.vaultRoot, filepath.FromSlash(origin))
	if _, err := os.Stat(dst); err == nil {
		return nil, errors.New("destination already exists")
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, err
	}
	if err := os.Rename(src, dst); err != nil {
		return nil, err
	}

	// Collect restored note paths (file = single, dir = walk).
	var restored []string
	if st.IsDir() {
		_ = filepath.WalkDir(dst, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if !strings.EqualFold(filepath.Ext(p), ".md") {
				return nil
			}
			rel, _ := filepath.Rel(b.vaultRoot, p)
			restored = append(restored, filepath.ToSlash(rel))
			return nil
		})
	} else {
		restored = append(restored, origin)
	}
	return restored, nil
}

// Purge deletes a single trashed entry permanently.
func (b *Bin) Purge(id string) error {
	return os.RemoveAll(filepath.Join(b.dir, id))
}

// PurgeAll empties the trash.
func (b *Bin) PurgeAll() error {
	entries, err := os.ReadDir(b.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(b.dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// PruneExpired removes entries older than the bin's retention window. A
// non-positive retention means "never prune". Returns the number of removed
// items. Best run at server startup.
func (b *Bin) PruneExpired() (int, error) {
	if b.retention <= 0 {
		return 0, nil
	}
	cutoff := time.Now().Add(-b.retention)
	entries, err := b.List()
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, e := range entries {
		if e.DiscardedAt.Before(cutoff) {
			if err := b.Purge(e.ID); err != nil {
				return removed, err
			}
			removed++
		}
	}
	return removed, nil
}

// newID produces "<unix-nano>__<sanitized-original>" so we can reconstruct
// both the discard timestamp and the source path on restore.
func newID(originalPath string) string {
	ts := time.Now().UTC().UnixNano()
	clean := strings.NewReplacer(
		"/", "%2F",
		"\\", "%5C",
		":", "%3A",
		" ", "%20",
	).Replace(originalPath)
	return formatNano(ts) + "__" + clean
}

func parseID(id string) (time.Time, string) {
	parts := strings.SplitN(id, "__", 2)
	if len(parts) != 2 {
		return time.Time{}, ""
	}
	ns, err := parseNano(parts[0])
	if err != nil {
		return time.Time{}, ""
	}
	origin := strings.NewReplacer(
		"%2F", "/",
		"%5C", "\\",
		"%3A", ":",
		"%20", " ",
	).Replace(parts[1])
	return time.Unix(0, ns).UTC(), origin
}

func formatNano(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		n = -n
		neg = true
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func parseNano(s string) (int64, error) {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errors.New("invalid nanos")
		}
		n = n*10 + int64(c-'0')
	}
	return n, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
