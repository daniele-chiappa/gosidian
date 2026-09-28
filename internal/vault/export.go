package vault

import (
	"io/fs"
	"path/filepath"
	"strings"
)

// WalkExport calls fn, in lexical order, for every file an export copies
// from dir ("" for the whole vault), with its vault-relative path. That is
// every file the vault holds, not only notes and attachments (.canvas,
// .base, a PDF outside attachments/…), so the archive opened in Obsidian is
// the vault itself. Skipped, as List skips them: hidden files and folders
// (.gosidian with the trash and the default state dir, .git, .obsidian) and
// node_modules. Also skipped: symlinks, which could point outside the vault,
// anything that is not a regular file, and the directories in exclude
// (absolute paths: a state dir set inside the vault under a visible name).
func (v *Vault) WalkExport(dir string, exclude []string, fn func(rel string, info fs.FileInfo) error) error {
	root := v.Root
	if dir != "" {
		r, err := v.Rel(dir)
		if err != nil {
			return err
		}
		root = filepath.Join(v.Root, filepath.FromSlash(r))
	}
	skip := make(map[string]bool, len(exclude))
	for _, e := range exclude {
		if a, err := filepath.Abs(e); e != "" && err == nil {
			skip[a] = true
		}
	}
	excluded := func(path string) bool {
		if len(skip) == 0 {
			return false
		}
		a, err := filepath.Abs(path)
		return err == nil && skip[a]
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") || name == "node_modules" || excluded(path)) {
				return fs.SkipDir
			}
			return nil
		}
		if isHidden(name) || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(v.Root, path)
		if err != nil {
			return err
		}
		return fn(filepath.ToSlash(rel), info)
	})
}
