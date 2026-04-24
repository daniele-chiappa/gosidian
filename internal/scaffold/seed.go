// Package scaffold loads bootstrap templates from the vault and seeds
// built-in presets at first boot.
//
// Layout contract:
//
//	<vault>/.gosidian/templates/<name>/
//	    _template.toml       # name, description, prompt, variables
//	    <files...>           # any file tree, copied verbatim on scaffold
//
// Built-in templates live in the binary under an embedded filesystem
// passed by the caller (the MCP package owns the embed.FS so we don't
// circular-import from here). `SeedTemplates` copies any embedded
// template whose target folder is missing in the vault. It never
// overwrites user edits.
//
// `LoadTemplate` reads a single template (meta + file list) from the
// vault; `ListTemplates` walks the directory.
package scaffold

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// Variable is a single placeholder declared by a template.
type Variable struct {
	Name     string `toml:"name" json:"name"`
	Required bool   `toml:"required,omitempty" json:"required,omitempty"`
	Default  string `toml:"default,omitempty" json:"default,omitempty"`
	Auto     string `toml:"auto,omitempty" json:"auto,omitempty"` // "date" etc.
}

// Meta is the shape of _template.toml.
type Meta struct {
	Name        string     `toml:"name" json:"name"`
	Description string     `toml:"description" json:"description"`
	Prompt      string     `toml:"prompt" json:"prompt"`
	Variables   []Variable `toml:"variables" json:"variables"`
}

// Template is a parsed template ready to be applied.
type Template struct {
	Meta
	Root      string   // absolute path to the template root
	FileCount int      // number of template files (excluding _template.toml)
	Files     []string // relative paths (without _template.toml)
}

const (
	// TemplatesSubdir is where vault-resident templates live under
	// <vault>/.gosidian/.
	TemplatesSubdir = ".gosidian/templates"

	// MetaFilename is the per-template meta file name.
	MetaFilename = "_template.toml"
)

// ErrTemplateNotFound is returned by LoadTemplate when the requested
// template directory does not exist under <vault>/.gosidian/templates/.
var ErrTemplateNotFound = errors.New("template not found")

// TemplatesDir returns <vault>/.gosidian/templates given the vault root.
func TemplatesDir(vaultRoot string) string {
	return filepath.Join(vaultRoot, TemplatesSubdir)
}

// SeedTemplates copies every template from the embedded FS into
// <vault>/.gosidian/templates/ when its target directory is absent.
// Existing template folders are left untouched (idempotent). Returns
// the list of template names that were seeded on this call.
//
// `embedRoot` is the root directory inside `efs` under which the
// templates live (e.g. "assets_templates"). Each child directory of
// embedRoot becomes a template.
func SeedTemplates(vaultRoot string, efs fs.FS, embedRoot string) ([]string, error) {
	targetDir := TemplatesDir(vaultRoot)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir templates dir: %w", err)
	}

	entries, err := fs.ReadDir(efs, embedRoot)
	if err != nil {
		return nil, fmt.Errorf("read embed root %q: %w", embedRoot, err)
	}

	var seeded []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		target := filepath.Join(targetDir, name)
		if _, err := os.Stat(target); err == nil {
			// Template already present on disk — never overwrite.
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return seeded, fmt.Errorf("stat %s: %w", target, err)
		}
		if err := copyEmbedTree(efs, filepath.ToSlash(filepath.Join(embedRoot, name)), target); err != nil {
			return seeded, fmt.Errorf("seed %s: %w", name, err)
		}
		seeded = append(seeded, name)
	}
	sort.Strings(seeded)
	return seeded, nil
}

// copyEmbedTree walks efs starting at src and mirrors the tree under dst.
func copyEmbedTree(efs fs.FS, src, dst string) error {
	return fs.WalkDir(efs, src, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel := strings.TrimPrefix(p, src)
		rel = strings.TrimPrefix(rel, "/")
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := fs.ReadFile(efs, p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// ListTemplates returns every template found in
// <vault>/.gosidian/templates/, sorted alphabetically. Missing dir is
// not an error — it just returns an empty slice.
func ListTemplates(vaultRoot string) ([]Template, error) {
	dir := TemplatesDir(vaultRoot)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []Template
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		// Hide dot-prefixed names so users can soft-delete by renaming.
		if strings.HasPrefix(name, ".") {
			continue
		}
		t, err := LoadTemplate(vaultRoot, name)
		if err != nil {
			// Skip malformed templates but keep listing the rest.
			continue
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// LoadTemplate reads a single template by name and returns its meta + file list.
func LoadTemplate(vaultRoot, name string) (Template, error) {
	root := filepath.Join(TemplatesDir(vaultRoot), name)
	st, err := os.Stat(root)
	if err != nil || !st.IsDir() {
		return Template{}, fmt.Errorf("%w: %s", ErrTemplateNotFound, name)
	}
	metaPath := filepath.Join(root, MetaFilename)
	metaBytes, err := os.ReadFile(metaPath)
	if err != nil {
		return Template{}, fmt.Errorf("read meta %s: %w", metaPath, err)
	}
	var meta Meta
	if _, err := toml.Decode(string(metaBytes), &meta); err != nil {
		return Template{}, fmt.Errorf("parse meta %s: %w", metaPath, err)
	}
	if meta.Name == "" {
		// Fall back to directory name if the TOML omits it.
		meta.Name = name
	}

	// Walk the template tree to list content files.
	var files []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, wErr error) error {
		if wErr != nil {
			return wErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == MetaFilename {
			return nil // skip the meta itself
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return Template{}, err
	}
	sort.Strings(files)
	return Template{
		Meta:      meta,
		Root:      root,
		FileCount: len(files),
		Files:     files,
	}, nil
}

// ReadFile returns the raw bytes of the template file at the given
// relative path. The path must be one of t.Files (caller-validated).
func (t *Template) ReadFile(rel string) ([]byte, error) {
	return os.ReadFile(filepath.Join(t.Root, filepath.FromSlash(rel)))
}
