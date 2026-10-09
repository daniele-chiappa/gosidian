package vault

import (
	"io/fs"
	"path/filepath"
)

// WalkExport calls fn, in lexical order, for every file an export copies
// from dir ("" for the whole vault), with its vault-relative path. That is
// every file the vault holds, not only notes and attachments (.canvas,
// .base, a PDF outside attachments/…), so the archive opened in Obsidian is
// the vault itself. Skipped, as List skips them: hidden files and folders
// (.gosidian with the trash and the default state dir, .git, .obsidian),
// node_modules and a state dir set inside the vault under a visible name
// (SetStateDir). Also skipped: symlinks, which could point outside the
// vault, and anything that is not a regular file.
func (v *Vault) WalkExport(dir string, fn func(rel string, info fs.FileInfo) error) error {
	root := v.Root
	if dir != "" {
		r, err := v.Rel(dir)
		if err != nil {
			return err
		}
		root = filepath.Join(v.Root, filepath.FromSlash(r))
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return skipVanished(root, path, err)
		}
		if d.IsDir() {
			if v.skipDir(root, path) {
				return fs.SkipDir
			}
			return nil
		}
		if isHidden(d.Name()) || !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return skipVanished(root, path, err)
		}
		rel, err := filepath.Rel(v.Root, path)
		if err != nil {
			return err
		}
		return fn(filepath.ToSlash(rel), info)
	})
}
