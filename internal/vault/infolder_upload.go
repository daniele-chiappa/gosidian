package vault

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// INUploads is the hidden folder at the root of an IN folder that holds
// the uploads on their way (IMP-172 phase 5), one folder per upload: on the
// IN folder's filesystem, so that a file that arrived whole takes its place
// with a rename. Hidden, it is left out by the scan, the listings, the
// zips, /vault-files/ and the watcher, as any hidden entry.
const INUploads = ".uploads"

// ValidUploadID reports whether id can name an upload's folder: 16 to 32
// lower-case hex characters.
func ValidUploadID(id string) bool {
	return len(id) >= 16 && len(id) <= 32 && strings.Trim(id, "0123456789abcdef") == ""
}

// inUploadsDir is the absolute path of a project's uploads folder, checked
// for links: the project's IN folder must be there, the project need not
// have use_in on (a session of a project whose IN folder closed is still
// removed).
func (v *Vault) inUploadsDir(project string) (string, error) {
	clean, err := v.CheckExistingProject(project)
	if err != nil {
		return "", err
	}
	in := filepath.Join(v.Root, clean, INFolder)
	st, err := os.Lstat(in)
	if err != nil {
		return "", err
	}
	if !st.IsDir() || !v.resolvesInside(in) {
		return "", fmt.Errorf("%w: %s/%s is not a folder of the vault", ErrNotIN, clean, INFolder)
	}
	return filepath.Join(in, INUploads), nil
}

// INStaging returns the absolute path of an upload's folder in a project's
// IN folder, <project>/IN/.uploads/<id>, made with create.
func (v *Vault) INStaging(project, id string, create bool) (string, error) {
	if !ValidUploadID(id) {
		return "", fmt.Errorf("%w: %q is no upload", ErrNotIN, id)
	}
	if create && (v.hasIN == nil || !v.hasIN(project)) {
		return "", fmt.Errorf("%w: the project %s has no IN folder", ErrNotIN, project)
	}
	up, err := v.inUploadsDir(project)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(up, id)
	if create {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !st.IsDir() || !v.resolvesInside(dir) {
		return "", fmt.Errorf("%w: the folder of the upload %s is reached through a link", ErrNotIN, id)
	}
	return dir, nil
}

// INStagingIDs lists the upload folders a project's IN folder holds.
func (v *Vault) INStagingIDs(project string) ([]string, error) {
	up, err := v.inUploadsDir(project)
	if err != nil {
		return nil, err
	}
	des, err := os.ReadDir(up)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, d := range des {
		if d.IsDir() && ValidUploadID(d.Name()) {
			out = append(out, d.Name())
		}
	}
	return out, nil
}

// INRemoveStaging removes an upload's folder with what it holds, and the
// uploads folder when it is left empty. A folder already gone is no error.
func (v *Vault) INRemoveStaging(project, id string) error {
	dir, err := v.INStaging(project, id, false)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	_ = os.Remove(filepath.Dir(dir)) // only when empty
	return nil
}

// INPlace moves the file at staged, a path relative to an upload's folder,
// to the vault path to, in the IN folder of the same project, making the
// folders it needs. With replace a file there is replaced; else the target
// must not exist. A folder is never replaced.
func (v *Vault) INPlace(project, id, staged, to string, replace bool) (INEntry, error) {
	dir, err := v.INStaging(project, id, false)
	if err != nil {
		return INEntry{}, err
	}
	s := path.Clean("/" + filepath.ToSlash(staged))
	if s == "/" {
		return INEntry{}, fmt.Errorf("%w: no file of the upload named", ErrNotIN)
	}
	sabs := filepath.Join(dir, filepath.FromSlash(s))
	sst, err := os.Lstat(sabs)
	if err != nil {
		return INEntry{}, err
	}
	if !sst.Mode().IsRegular() {
		return INEntry{}, fmt.Errorf("%w: %s of the upload is not a file", ErrNotIN, s)
	}
	tr, tabs, err := v.checkIN(to)
	if err != nil {
		return INEntry{}, err
	}
	if tp, _, _ := strings.Cut(tr, "/"); tp != project || tr == INRoot(project) {
		return INEntry{}, fmt.Errorf("%w: %q is not a file of the IN folder of %s", ErrNotIN, tr, project)
	}
	if st, err := os.Lstat(tabs); err == nil {
		switch {
		case st.IsDir():
			return INEntry{}, fmt.Errorf("%q is a folder", tr)
		case !replace:
			return INEntry{}, fmt.Errorf("%q already exists", tr)
		case !st.Mode().IsRegular():
			return INEntry{}, fmt.Errorf("%w: %q is not a file", ErrNotIN, tr)
		}
	}
	if err := os.MkdirAll(filepath.Dir(tabs), 0o755); err != nil {
		return INEntry{}, err
	}
	if !v.resolvesInside(filepath.Dir(tabs)) {
		return INEntry{}, fmt.Errorf("%w: %q is reached through a link", ErrNotIN, tr)
	}
	if err := renameInDir(sabs, tabs); err != nil {
		return INEntry{}, err
	}
	return v.statIN(tr, tabs)
}

// INFreeName returns the vault path to when nothing is there, else the
// first free of "name (2).ext", "name (3).ext" and on, as a file manager
// names a copy.
func (v *Vault) INFreeName(to string) (string, error) {
	tr, tabs, err := v.checkIN(to)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(tabs); errors.Is(err, fs.ErrNotExist) {
		return tr, nil
	}
	dir, name := path.Split(tr)
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if stem == "" {
		stem, ext = name, ""
	}
	for i := 2; i < 10000; i++ {
		c := fmt.Sprintf("%s%s (%d)%s", dir, stem, i, ext)
		if _, err := os.Lstat(filepath.Join(v.Root, filepath.FromSlash(c))); errors.Is(err, fs.ErrNotExist) {
			return c, nil
		}
	}
	return "", fmt.Errorf("no free name for %q", tr)
}
