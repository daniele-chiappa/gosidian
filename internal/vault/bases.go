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
	r, err := v.Rel(rel)
	if err != nil {
		return nil, err
	}
	if !IsBaseFile(r) {
		return nil, fmt.Errorf("%w: %q", ErrNotBase, r)
	}
	full := filepath.Join(v.Root, filepath.FromSlash(r))
	st, err := os.Stat(full)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, fs.ErrNotExist
	}
	if st.Size() > MaxBaseBytes {
		return nil, fmt.Errorf("%w: %q is %d bytes, over %d", ErrNotBase, r, st.Size(), MaxBaseBytes)
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

// ListBases returns the paths of the .base files of the vault, or of the
// project when one is given, sorted, with the filters of List: hidden
// folders and files are skipped. A project that does not exist has none.
func (v *Vault) ListBases(project string) ([]string, error) {
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
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules") {
				return fs.SkipDir
			}
			return nil
		}
		if isHidden(d.Name()) || !IsBaseFile(d.Name()) || !d.Type().IsRegular() {
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
