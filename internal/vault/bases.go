package vault

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Obsidian bases (IMP-118): a .base file is the YAML of views Obsidian
// reads over the vault's notes. It is no note: gosidian reads it, shows it
// translated into views (views.BaseMarkdown) and never writes it, so Save,
// Delete and Move keep refusing it (ADR-021).

// MaxBaseBytes caps the .base file read: a base is a few hundred bytes of
// YAML, anything larger is no base.
const MaxBaseBytes = 256 << 10

// ErrNotBase is returned by LoadBase for a path that is not a .base file.
var ErrNotBase = fmt.Errorf("not an Obsidian base")

// IsBaseFile reports whether name (a path or basename) is an Obsidian base.
func IsBaseFile(name string) bool {
	return strings.EqualFold(filepath.Ext(name), ".base")
}

// LoadBase reads the .base file at rel: Content holds its YAML as written.
func (v *Vault) LoadBase(rel string) (*Note, error) {
	return v.loadReadOnly(rel, IsBaseFile, MaxBaseBytes, ErrNotBase)
}

// ListBases returns the paths of the .base files of the vault, or of the
// project when one is given, sorted, with the filters of List: hidden
// folders and files are skipped. A project that does not exist has none.
func (v *Vault) ListBases(project string) ([]string, error) {
	return v.listFiles(project, IsBaseFile)
}

// loadReadOnly reads a file of the vault that gosidian shows without
// writing it (a base, a canvas): is tells its kind by name, max caps its
// size, and notKind is the error for a path of another kind.
func (v *Vault) loadReadOnly(rel string, is func(string) bool, max int64, notKind error) (*Note, error) {
	r, err := v.Rel(rel)
	if err != nil {
		return nil, err
	}
	if !is(r) {
		return nil, fmt.Errorf("%w: %q", notKind, r)
	}
	full := filepath.Join(v.Root, filepath.FromSlash(r))
	st, err := os.Stat(full)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fs.ErrNotExist
	}
	if st.Size() > max {
		return nil, fmt.Errorf("%w: %q is %d bytes, over %d", notKind, r, st.Size(), max)
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	return &Note{
		Path:    r,
		Title:   strings.TrimSuffix(filepath.Base(r), filepath.Ext(r)),
		Content: data,
		ModTime: st.ModTime(),
		Size:    st.Size(),
	}, nil
}

// listFiles returns the paths of the files of the vault, or of the project
// when one is given, that is accepts, sorted, with the filters of List.
func (v *Vault) listFiles(project string, is func(string) bool) ([]string, error) {
	root := v.Root
	if project != "" {
		r, err := v.Rel(project)
		if err != nil {
			return nil, err
		}
		root = filepath.Join(v.Root, filepath.FromSlash(r))
		if st, err := os.Stat(root); err != nil || !st.IsDir() {
			return nil, nil
		}
	}
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if v.skipDir(root, path) {
				return fs.SkipDir
			}
			return nil
		}
		if isHidden(d.Name()) || !is(d.Name()) || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(v.Root, path)
		if err != nil {
			return err
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
