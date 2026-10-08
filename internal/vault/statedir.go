package vault

import (
	"errors"
	"path/filepath"
	"strings"
)

// SetStateDir tells the vault where the server's state dir is: credentials,
// config, project flags, audit log, index (ADR-023). The default,
// <vault>/.gosidian, is hidden by its name already. One set inside the vault
// under a visible name (<vault>/state) would be a folder like any other:
// a project, listed, indexed, zipped and pushed by git with the tokens in
// it (BUG-098). The vault treats it as hidden instead: Rel refuses it, every
// walk skips it, and no project operation creates, moves or removes it or a
// folder holding it. A state dir outside the vault changes nothing. Call it
// before the first scan; dir is an absolute path.
func (v *Vault) SetStateDir(dir string) {
	v.stateRel = ""
	if dir == "" {
		return
	}
	if rel, ok := relInside(v.Root, dir); ok {
		v.stateRel = rel
	}
}

// StateDirInside returns the vault-relative path of the state dir when it
// sits inside the vault, "" otherwise.
func (v *Vault) StateDirInside() string { return v.stateRel }

// InStateDir reports whether the vault-relative path rel is the state dir
// or lies under it.
func (v *Vault) InStateDir(rel string) bool { return v.hides(rel) }

// HoldsStateDir reports whether the vault folder rel (cleaned, as Rel
// returns it) holds the state dir: moving or removing it would take the
// credentials along.
func (v *Vault) HoldsStateDir(rel string) bool {
	return v.stateRel != "" && strings.HasPrefix(v.stateRel, rel+"/")
}

// CheckProject returns name as a top-level project folder name, trimmed, or
// why it cannot be one: the check every project operation applies. Beyond a
// portable folder name, it refuses the folder of the state dir and a folder
// holding it.
func (v *Vault) CheckProject(name string) (string, error) {
	clean, err := sanitizeProjectName(name)
	if err != nil {
		return "", err
	}
	if v.hides(clean) || v.HoldsStateDir(clean) {
		return "", errors.New("invalid project name: the folder holds the server's state directory")
	}
	return clean, nil
}

// hides reports whether the cleaned vault-relative path rel is the state
// dir or lies under it.
func (v *Vault) hides(rel string) bool {
	s := v.stateRel
	return s != "" && (rel == s || strings.HasPrefix(rel, s+"/"))
}

// skipDir reports whether a walk rooted at root leaves out the folder at
// path: a hidden one, node_modules, or the state dir.
func (v *Vault) skipDir(root, path string) bool {
	if path == root {
		return false
	}
	name := filepath.Base(path)
	if strings.HasPrefix(name, ".") || name == "node_modules" {
		return true
	}
	if v.stateRel == "" {
		return false
	}
	rel, err := filepath.Rel(v.Root, path)
	return err == nil && v.hides(filepath.ToSlash(rel))
}

// relInside returns the slash path of dir relative to root when dir lies
// strictly inside it. Both are compared after resolving symlinks, where
// they resolve, so a vault reached through a link still matches.
func relInside(root, dir string) (string, bool) {
	resolve := func(p string) string {
		a, err := filepath.Abs(p)
		if err != nil {
			return filepath.Clean(p)
		}
		if r, err := filepath.EvalSymlinks(a); err == nil {
			return r
		}
		return a
	}
	rel, err := filepath.Rel(resolve(root), resolve(dir))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}
