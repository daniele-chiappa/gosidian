package vault

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// INFolder is the folder of a project that holds files to work on, under
// their own names and in their own tree: what a pipeline takes in (IMP-172,
// ADR-037). It exists in the projects whose use_in flag is on.
const INFolder = "IN"

// ErrNotIN is returned for a path that is not in the IN folder of a project
// that has one, or that the IN folder cannot hold: a link, a hidden entry.
var ErrNotIN = errors.New("not a path in an IN folder")

// SetINProjects tells the vault which projects have an IN folder: has is
// their use_in flag. Inside one nothing is a note: no walk lists, indexes,
// watches or zips what it holds (but a zip of the IN folder itself), the
// note and attachment operations refuse its paths, and the IN functions of
// this file are the only way in. Call it before the first scan.
func (v *Vault) SetINProjects(has func(project string) bool) { v.hasIN = has }

// InArea reports whether the cleaned vault-relative path rel is the IN
// folder of a project that has one, or lies in it.
func (v *Vault) InArea(rel string) bool {
	if v.hasIN == nil {
		return false
	}
	project, rest, ok := strings.Cut(rel, "/")
	if !ok || project == "" {
		return false
	}
	if rest != INFolder && !strings.HasPrefix(rest, INFolder+"/") {
		return false
	}
	return v.hasIN(project)
}

// INRoot is the vault-relative path of a project's IN folder.
func INRoot(project string) string { return project + "/" + INFolder }

// INEntry is a file or a folder of an IN folder.
type INEntry struct {
	Path    string // vault-relative
	Dir     bool
	Size    int64
	ModTime time.Time
}

func entryOf(rel string, info fs.FileInfo) INEntry {
	e := INEntry{Path: rel, Dir: info.IsDir(), ModTime: info.ModTime()}
	if !e.Dir {
		e.Size = info.Size()
	}
	return e
}

// checkIN cleans rel as Rel does and returns it with its absolute path when
// it is in the IN folder of a project that has one.
func (v *Vault) checkIN(rel string) (string, string, error) {
	r, err := v.Rel(rel)
	if err != nil {
		return "", "", err
	}
	if !v.InArea(r) {
		return "", "", fmt.Errorf("%w: %q", ErrNotIN, r)
	}
	return r, filepath.Join(v.Root, filepath.FromSlash(r)), nil
}

// statIN is the entry at abs, refused when it is a link, sits behind one,
// or is neither a file nor a folder.
func (v *Vault) statIN(r, abs string) (INEntry, error) {
	st, err := os.Lstat(abs)
	if err != nil {
		return INEntry{}, err
	}
	if !st.Mode().IsRegular() && !st.IsDir() {
		return INEntry{}, fmt.Errorf("%w: %q is not a file or a folder", ErrNotIN, r)
	}
	if !v.resolvesInside(abs) {
		return INEntry{}, fmt.Errorf("%w: %q is reached through a link", ErrNotIN, r)
	}
	return entryOf(r, st), nil
}

// INStat describes a file or a folder of an IN folder.
func (v *Vault) INStat(rel string) (INEntry, error) {
	r, abs, err := v.checkIN(rel)
	if err != nil {
		return INEntry{}, err
	}
	return v.statIN(r, abs)
}

// INReadDir lists the files and folders right inside a folder of an IN
// folder, by name. Hidden entries, links and what is neither a file nor a
// folder are left out.
func (v *Vault) INReadDir(rel string) ([]INEntry, error) {
	r, abs, err := v.checkIN(rel)
	if err != nil {
		return nil, err
	}
	if e, err := v.statIN(r, abs); err != nil {
		return nil, err
	} else if !e.Dir {
		return nil, fmt.Errorf("%w: %q is not a folder", ErrNotIN, r)
	}
	des, err := os.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	out := make([]INEntry, 0, len(des))
	for _, d := range des {
		if isHidden(d.Name()) || (!d.Type().IsRegular() && !d.IsDir()) {
			continue
		}
		info, err := d.Info()
		if err != nil {
			continue // gone meanwhile
		}
		out = append(out, entryOf(r+"/"+d.Name(), info))
	}
	return out, nil
}

// INWalk calls fn, in lexical order, for every file under a folder of an IN
// folder (rel itself when it is a file). Hidden files and folders, links and
// what is not a regular file are left out; a file or a folder removed while
// the walk runs is skipped.
func (v *Vault) INWalk(rel string, fn func(INEntry) error) error {
	r, abs, err := v.checkIN(rel)
	if err != nil {
		return err
	}
	if _, err := v.statIN(r, abs); err != nil {
		return err
	}
	return filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return skipVanished(abs, path, err)
		}
		if path != abs && isHidden(d.Name()) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return skipVanished(abs, path, err)
		}
		p, err := filepath.Rel(v.Root, path)
		if err != nil {
			return err
		}
		return fn(entryOf(filepath.ToSlash(p), info))
	})
}

// INOpen opens a file of an IN folder for reading.
func (v *Vault) INOpen(rel string) (*os.File, INEntry, error) {
	r, abs, err := v.checkIN(rel)
	if err != nil {
		return nil, INEntry{}, err
	}
	e, err := v.statIN(r, abs)
	if err != nil {
		return nil, INEntry{}, err
	}
	if e.Dir {
		return nil, INEntry{}, fmt.Errorf("%w: %q is a folder", ErrNotIN, r)
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, INEntry{}, err
	}
	return f, e, nil
}

// INRead reads a file of an IN folder whole, when it is at most max bytes.
func (v *Vault) INRead(rel string, max int64) ([]byte, error) {
	f, e, err := v.INOpen(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if e.Size > max {
		return nil, fmt.Errorf("%q is %d bytes, at most %d", e.Path, e.Size, max)
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%q is more than %d bytes", e.Path, max)
	}
	return data, nil
}

// INMove moves a file or a folder of an IN folder to another place of the
// same IN folder, making the folders it needs; the target must not exist.
// The folders the move left empty go, the IN folder itself stays.
func (v *Vault) INMove(from, to string) (INEntry, error) {
	fr, fabs, err := v.checkIN(from)
	if err != nil {
		return INEntry{}, err
	}
	tr, tabs, err := v.checkIN(to)
	if err != nil {
		return INEntry{}, err
	}
	fp, _, _ := strings.Cut(fr, "/")
	tp, _, _ := strings.Cut(tr, "/")
	if fp != tp {
		return INEntry{}, fmt.Errorf("%w: %q and %q are in two projects", ErrNotIN, fr, tr)
	}
	root := INRoot(fp)
	if fr == root || tr == root {
		return INEntry{}, fmt.Errorf("%w: the IN folder itself does not move", ErrNotIN)
	}
	if tr == fr || strings.HasPrefix(tr, fr+"/") {
		return INEntry{}, fmt.Errorf("%w: %q cannot move into itself", ErrNotIN, fr)
	}
	if _, err := v.statIN(fr, fabs); err != nil {
		return INEntry{}, err
	}
	if _, err := os.Lstat(tabs); err == nil {
		return INEntry{}, fmt.Errorf("%q already exists", tr)
	}
	if err := os.MkdirAll(filepath.Dir(tabs), 0o755); err != nil {
		return INEntry{}, err
	}
	if !v.resolvesInside(filepath.Dir(tabs)) {
		return INEntry{}, fmt.Errorf("%w: %q is reached through a link", ErrNotIN, tr)
	}
	if err := renameInDir(fabs, tabs); err != nil {
		return INEntry{}, err
	}
	PruneEmptyParents(v.Root, fr)
	return v.statIN(tr, tabs)
}

// INMkdir makes a folder of an IN folder, with the folders above it.
func (v *Vault) INMkdir(rel string) error {
	r, abs, err := v.checkIN(rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return err
	}
	_, err = v.statIN(r, abs)
	return err
}

// INRemove removes a file or a folder of an IN folder, with all it holds,
// for good: the trash, when it is on, is the caller's (trash.Bin). The IN
// folder itself stays.
func (v *Vault) INRemove(rel string) error {
	r, abs, err := v.checkIN(rel)
	if err != nil {
		return err
	}
	project, _, _ := strings.Cut(r, "/")
	if r == INRoot(project) {
		return fmt.Errorf("%w: the IN folder itself is not removed", ErrNotIN)
	}
	e, err := v.statIN(r, abs)
	if err != nil {
		return err
	}
	if e.Dir {
		err = os.RemoveAll(abs)
	} else {
		err = os.Remove(abs)
	}
	if err != nil {
		return err
	}
	PruneEmptyParents(v.Root, r)
	return nil
}

// EnsureIN makes the IN folder of a project when it is missing. The
// project's use_in flag is the caller's.
func (v *Vault) EnsureIN(project string) error {
	clean, err := v.CheckExistingProject(project)
	if err != nil {
		return err
	}
	abs := filepath.Join(v.Root, clean, INFolder)
	st, err := os.Lstat(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return os.Mkdir(abs, 0o755)
	case err != nil:
		return err
	case !st.IsDir():
		return fmt.Errorf("%s/%s is there and is not a folder", clean, INFolder)
	case !v.resolvesInside(abs):
		return fmt.Errorf("%s/%s is a link", clean, INFolder)
	}
	return nil
}

// NotesInIN lists the notes a project's folder named IN holds, at most max,
// sorted: turning use_in on would hide them from the index.
func (v *Vault) NotesInIN(project string, max int) ([]string, error) {
	clean, err := v.CheckExistingProject(project)
	if err != nil {
		return nil, err
	}
	abs := filepath.Join(v.Root, clean, INFolder)
	if st, err := os.Lstat(abs); err != nil || !st.IsDir() {
		return nil, nil
	}
	var out []string
	err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return skipVanished(abs, path, err)
		}
		if d.IsDir() {
			if path != abs && isHidden(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}
		if isHidden(d.Name()) || !v.IsNoteFile(d.Name()) {
			return nil
		}
		if len(out) >= max {
			return fs.SkipAll
		}
		rel, err := filepath.Rel(v.Root, path)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(out)
	return out, err
}
