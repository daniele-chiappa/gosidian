// Package trash implements an opt-in soft-delete bin for vault notes and
// projects. When the user enables it via [trash] in config.toml the
// HTTP/MCP delete handlers route through Bin.Discard* instead of removing
// files from disk. A Bin scoped to a specific vault keeps everything
// under <vault>/.gosidian/trash/<timestamp>-<sanitized-path>/.
package trash

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/gosidian/gosidian/internal/vault"
)

// isNotePath reports whether p carries a recognised note extension
// (flag-independent: over-collecting a disabled extension only makes the
// caller's index cleanup a no-op, while hardcoding ".md" left .html notes
// stale in the index — BUG-023).
func isNotePath(p string) bool {
	ext := strings.ToLower(filepath.Ext(p))
	for _, e := range vault.NoteExtensions() {
		if ext == e {
			return true
		}
	}
	return false
}

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

// projectMetaFile is the sidecar DiscardProject keeps inside a trashed
// project folder: the caller's record of the project (its access, IMP-124),
// handed back by Restore and gone with the entry on purge or expiry.
const projectMetaFile = ".gosidian-trash-project.json"

// DiscardProject moves an entire project directory (recursively) into trash.
// A non-nil meta is stored with it (see ProjectMeta and Restore); it is
// written before the move, so the project is trashed with its meta or not
// at all.
func (b *Bin) DiscardProject(name string, meta []byte) (string, []string, error) {
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
		if !isNotePath(p) {
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
	metaPath := filepath.Join(src, projectMetaFile)
	if meta != nil {
		// The name is reserved: a file (or symlink) already there is not
		// overwritten, or followed.
		if _, err := os.Lstat(metaPath); err == nil {
			return "", nil, fmt.Errorf("project folder holds a file named %s", projectMetaFile)
		}
		f, err := os.OpenFile(metaPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return "", nil, err
		}
		_, werr := f.Write(meta)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			_ = os.Remove(metaPath)
			return "", nil, werr
		}
	}
	if err := os.Rename(src, dst); err != nil {
		if meta != nil {
			_ = os.Remove(metaPath)
		}
		return "", nil, err
	}
	return id, notes, nil
}

// ProjectMeta returns the meta stored with a trashed project, or nil when
// the entry has none (a note, or a project trashed without one).
func (b *Bin) ProjectMeta(id string) ([]byte, error) {
	if err := checkID(id); err != nil {
		return nil, err
	}
	path := filepath.Join(b.dir, id, projectMetaFile)
	st, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", projectMetaFile)
	}
	return os.ReadFile(path)
}

// Entry is one item in the trash listing.
type Entry struct {
	ID          string // filename inside the trash dir
	OriginPath  string // best-effort original vault-relative path
	DiscardedAt time.Time
	IsDir       bool
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
// .md paths that were restored (single entry for notes, multi for projects)
// and, for a project, the meta stored by DiscardProject (nil if none); the
// meta file does not come back into the vault.
func (b *Bin) Restore(id string) ([]string, []byte, error) {
	if err := checkID(id); err != nil {
		return nil, nil, err
	}
	src := filepath.Join(b.dir, id)
	st, err := os.Stat(src)
	if err != nil {
		return nil, nil, err
	}
	_, origin := parseID(id)
	if origin == "" {
		return nil, nil, errors.New("cannot determine original path from id")
	}
	// The origin is decoded from the id (%2F back to "/"), which checkID
	// does not see: "..%2F" would lead out of the vault (BUG-083). The
	// check sits next to the join on purpose; ValidOrigin is the same test
	// for callers.
	rel := filepath.FromSlash(origin)
	if !filepath.IsLocal(rel) || hiddenSegment(origin) {
		return nil, nil, fmt.Errorf("invalid origin %q in trash id", origin)
	}
	dst := filepath.Join(b.vaultRoot, rel)
	if _, err := os.Stat(dst); err == nil {
		return nil, nil, errors.New("destination already exists")
	}
	var meta []byte
	if st.IsDir() {
		if meta, err = b.ProjectMeta(id); err != nil {
			return nil, nil, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, nil, err
	}
	if err := os.Rename(src, dst); err != nil {
		return nil, nil, err
	}
	if meta != nil {
		_ = os.Remove(filepath.Join(dst, projectMetaFile))
	}

	// Collect restored note paths (file = single, dir = walk).
	var restored []string
	if st.IsDir() {
		_ = filepath.WalkDir(dst, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if !isNotePath(p) {
				return nil
			}
			rel, _ := filepath.Rel(b.vaultRoot, p)
			restored = append(restored, filepath.ToSlash(rel))
			return nil
		})
	} else {
		restored = append(restored, origin)
	}
	return restored, meta, nil
}

// Purge deletes a single trashed entry permanently.
func (b *Bin) Purge(id string) error {
	if err := checkID(id); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(b.dir, id))
}

// checkID refuses an id that is not a single name inside the trash
// directory. The HTTP router already cleans dot segments out of the path;
// this keeps Purge's RemoveAll inside the bin whoever calls it.
func checkID(id string) error {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\\x00") {
		return errors.New("invalid trash id")
	}
	return nil
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

// ValidOrigin reports whether origin, decoded from a trash id, is a place
// inside the vault a restore may write: relative, without "..", and without
// hidden segments, as vault paths are (BUG-083). Entries are made from
// validated vault paths, so only an entry put in the trash by hand fails it.
func ValidOrigin(origin string) bool {
	return origin != "" && filepath.IsLocal(filepath.FromSlash(origin)) && !hiddenSegment(origin)
}

// hiddenSegment reports whether a slash-separated path has an empty or
// hidden segment.
func hiddenSegment(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

// Origin returns the vault-relative path an entry was discarded from (for a
// project, its name), or "" when the id does not carry one.
func Origin(id string) string {
	_, origin := parseID(id)
	return origin
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
