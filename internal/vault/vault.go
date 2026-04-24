package vault

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/index"
)

type Vault struct {
	Root  string
	cache *loadCache
}

// New returns a Vault rooted at the given directory. A 128-entry LRU load
// cache is enabled by default; call SetCacheSize to override (0 disables).
func New(root string) *Vault {
	return &Vault{
		Root:  root,
		cache: newLoadCache(128),
	}
}

// SetCacheSize resizes (or disables, with 0) the load cache. Existing entries
// are dropped on resize.
func (v *Vault) SetCacheSize(n int) {
	if n <= 0 {
		v.cache = nil
		return
	}
	v.cache = newLoadCache(n)
}

// Rel returns a cleaned vault-relative path. Rejects any path that contains
// a ".." segment (attempted escape).
func (v *Vault) Rel(p string) (string, error) {
	raw := filepath.ToSlash(p)
	for _, seg := range strings.Split(raw, "/") {
		if seg == ".." {
			return "", errors.New("invalid path")
		}
	}
	clean := filepath.Clean("/" + raw)
	clean = strings.TrimPrefix(clean, "/")
	if clean == "" || clean == "." {
		return "", errors.New("empty path")
	}
	return clean, nil
}

func (v *Vault) Abs(rel string) (string, error) {
	r, err := v.Rel(rel)
	if err != nil {
		return "", err
	}
	return filepath.Join(v.Root, filepath.FromSlash(r)), nil
}

func (v *Vault) Load(rel string) (*Note, error) {
	r, err := v.Rel(rel)
	if err != nil {
		return nil, err
	}
	// Stat first so we can validate any cache entry against the current
	// filesystem state. A cache hit saves the full file read.
	full := filepath.Join(v.Root, r)
	st, err := os.Stat(full)
	if err != nil {
		return nil, err
	}
	if v.cache != nil {
		if n := v.cache.Get(r, st.ModTime(), st.Size()); n != nil {
			return n, nil
		}
	}
	note, err := loadNote(v.Root, r)
	if err != nil {
		return nil, err
	}
	if v.cache != nil {
		v.cache.Put(r, note)
	}
	return note, nil
}

func (v *Vault) Delete(rel string) error {
	abs, err := v.Abs(rel)
	if err != nil {
		return err
	}
	if v.cache != nil {
		// We need the cleaned relative path for the cache key; reuse Rel.
		if r, rErr := v.Rel(rel); rErr == nil {
			v.cache.Invalidate(r)
		}
	}
	return os.Remove(abs)
}

func (v *Vault) Save(rel string, content []byte) error {
	abs, err := v.Abs(rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(abs, content, 0o644); err != nil {
		return err
	}
	if v.cache != nil {
		if r, rErr := v.Rel(rel); rErr == nil {
			v.cache.Invalidate(r)
		}
	}
	return nil
}

// List returns all markdown note paths, sorted.
func (v *Vault) List() ([]string, error) {
	var out []string
	err := filepath.WalkDir(v.Root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != v.Root && (strings.HasPrefix(name, ".") || name == "node_modules") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
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

// ScanInto enumerates all markdown notes and upserts them into the index.
func (v *Vault) ScanInto(idx *index.Index) error {
	paths, err := v.List()
	if err != nil {
		return err
	}
	for _, p := range paths {
		n, err := loadNote(v.Root, p)
		if err != nil {
			return fmt.Errorf("load %s: %w", p, err)
		}
		if err := idx.Upsert(toIndexNote(n)); err != nil {
			return fmt.Errorf("index %s: %w", p, err)
		}
	}
	return nil
}

// Project describes a top-level directory inside the vault. Projects are
// derived from the filesystem — no metadata is stored separately.
type Project struct {
	Name      string
	NoteCount int
}

// Projects returns all top-level directories under the vault root, sorted by
// name. Hidden directories and bookkeeping folders are skipped.
func (v *Vault) Projects() ([]Project, error) {
	entries, err := os.ReadDir(v.Root)
	if err != nil {
		return nil, err
	}
	var out []Project
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") || name == "node_modules" {
			continue
		}
		count, _ := v.countNotesIn(name)
		out = append(out, Project{Name: name, NoteCount: count})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (v *Vault) countNotesIn(dir string) (int, error) {
	n := 0
	root := filepath.Join(v.Root, dir)
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
		if strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			n++
		}
		return nil
	})
	return n, err
}

// DeleteProject removes a top-level project directory and everything inside
// it, recursively. It returns the list of vault-relative paths of the .md
// notes that were removed so the caller can purge them from the index.
func (v *Vault) DeleteProject(name string) ([]string, error) {
	clean, err := sanitizeProjectName(name)
	if err != nil {
		return nil, err
	}
	abs := filepath.Join(v.Root, clean)
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%q is not a directory", clean)
	}

	var notes []string
	_ = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			return nil
		}
		rel, relErr := filepath.Rel(v.Root, path)
		if relErr != nil {
			return nil
		}
		notes = append(notes, filepath.ToSlash(rel))
		return nil
	})

	if err := os.RemoveAll(abs); err != nil {
		return nil, err
	}
	return notes, nil
}

// CreateProject creates a directory at <vault>/<sanitized name> and returns the
// sanitized name. It returns an error if the sanitized name would be invalid or
// if the directory already exists.
func (v *Vault) CreateProject(name string) (string, error) {
	clean, err := sanitizeProjectName(name)
	if err != nil {
		return "", err
	}
	abs := filepath.Join(v.Root, clean)
	if _, err := os.Stat(abs); err == nil {
		return "", fmt.Errorf("project %q already exists", clean)
	}
	if err := os.Mkdir(abs, 0o755); err != nil {
		return "", err
	}
	return clean, nil
}

func sanitizeProjectName(name string) (string, error) {
	clean := strings.TrimSpace(name)
	if clean == "" {
		return "", errors.New("project name is empty")
	}
	if len(clean) > 100 {
		return "", errors.New("project name too long")
	}
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".") {
		return "", errors.New("invalid project name")
	}
	for _, ch := range []string{"/", "\\", ":"} {
		if strings.Contains(clean, ch) {
			return "", fmt.Errorf("project name cannot contain %q", ch)
		}
	}
	return clean, nil
}

// AttachmentInfo describes a single file inside an attachments/ directory.
type AttachmentInfo struct {
	Path    string // vault-relative, e.g. "project/attachments/abc.png"
	Size    int64
	ModTime time.Time
}

// Exists reports whether the vault-relative path points to an existing file,
// without loading or parsing it.
func (v *Vault) Exists(rel string) bool {
	abs, err := v.Abs(rel)
	if err != nil {
		return false
	}
	_, err = os.Stat(abs)
	return err == nil
}

// ListAttachments walks attachments/ directories and returns metadata for each
// file whose extension is in the provided allowed set. When project is
// non-empty only that project's attachments/ is scanned; otherwise all
// top-level projects plus the root attachments/ are included. Results are
// capped at 1000 entries.
func (v *Vault) ListAttachments(project string, allowedExt map[string]bool) ([]AttachmentInfo, error) {
	const maxResults = 1000
	var dirs []string
	if project != "" {
		dirs = []string{filepath.Join(v.Root, project, "attachments")}
	} else {
		// Root-level attachments
		dirs = append(dirs, filepath.Join(v.Root, "attachments"))
		// Per-project attachments
		entries, err := os.ReadDir(v.Root)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "node_modules" {
				continue
			}
			dirs = append(dirs, filepath.Join(v.Root, e.Name(), "attachments"))
		}
	}

	var out []AttachmentInfo
	for _, dir := range dirs {
		if _, err := os.Stat(dir); err != nil {
			continue // directory doesn't exist, skip
		}
		_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if len(out) >= maxResults {
				return fs.SkipAll
			}
			ext := strings.ToLower(filepath.Ext(d.Name()))
			if !allowedExt[ext] {
				return nil
			}
			rel, relErr := filepath.Rel(v.Root, path)
			if relErr != nil {
				return nil
			}
			info, infoErr := d.Info()
			if infoErr != nil {
				return nil
			}
			out = append(out, AttachmentInfo{
				Path:    filepath.ToSlash(rel),
				Size:    info.Size(),
				ModTime: info.ModTime(),
			})
			return nil
		})
	}
	return out, nil
}

// wikiLinkRegex matches [[anything]]; target/alias splitting is delegated to
// splitWikiLink so we honor \| as a literal pipe (markdown-table escape).
var wikiLinkRegex = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

// splitWikiLink mirrors parser.parseWikiLinkInner — duplicated to keep the
// vault package free of a cross-import on internal/parser.
func splitWikiLink(content string) (target, alias string) {
	content = strings.ReplaceAll(content, `\|`, "|")
	parts := strings.SplitN(content, "|", 2)
	target = strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		alias = strings.TrimSpace(parts[1])
	}
	return target, alias
}

// RenameNote renames a note from its current vault-relative path to a new
// one. Beyond the filesystem rename it also:
//   - reindexes the note under its new path
//   - rewrites wiki-links in every note that referenced the old name, so
//     [[OldBase]] becomes [[NewBase]] (and [[OldBase|alias]] → [[NewBase|alias]])
//
// Returns the list of notes whose content was rewritten (useful for the
// caller to report or test). Wiki-link rewriting is best-effort:
// basename-based matching covers the common case; fully-qualified targets
// like [[folder/OldBase]] are caught when they end with the old basename.
func (v *Vault) RenameNote(idx *index.Index, from, to string) ([]string, error) {
	fromRel, err := v.Rel(from)
	if err != nil {
		return nil, fmt.Errorf("invalid source: %w", err)
	}
	toRel, err := v.Rel(to)
	if err != nil {
		return nil, fmt.Errorf("invalid target: %w", err)
	}
	if !strings.HasSuffix(strings.ToLower(toRel), ".md") {
		toRel += ".md"
	}
	if fromRel == toRel {
		return nil, nil
	}

	fromAbs := filepath.Join(v.Root, filepath.FromSlash(fromRel))
	toAbs := filepath.Join(v.Root, filepath.FromSlash(toRel))

	if _, err := os.Stat(fromAbs); err != nil {
		return nil, fmt.Errorf("source not found: %w", err)
	}
	if _, err := os.Stat(toAbs); err == nil {
		return nil, fmt.Errorf("target %q already exists", toRel)
	}

	// Find notes that currently link to the source, BEFORE the rename, so we
	// can rewrite their content. Uses the existing backlinks query.
	backs, err := idx.Backlinks(fromRel)
	if err != nil {
		return nil, fmt.Errorf("lookup backlinks: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(toAbs), 0o755); err != nil {
		return nil, err
	}
	if err := os.Rename(fromAbs, toAbs); err != nil {
		return nil, err
	}

	oldBase := strings.TrimSuffix(filepath.Base(fromRel), ".md")
	newBase := strings.TrimSuffix(filepath.Base(toRel), ".md")

	var rewritten []string
	for _, b := range backs {
		if b.Path == fromRel {
			continue // self-reference guard, shouldn't happen
		}
		note, err := v.Load(b.Path)
		if err != nil {
			continue
		}
		newBody := rewriteWikiLinks(note.Content, oldBase, newBase, fromRel, toRel)
		if !bytesEqual(newBody, note.Content) {
			if err := v.Save(b.Path, newBody); err != nil {
				return rewritten, fmt.Errorf("rewrite %s: %w", b.Path, err)
			}
			// Reindex the updated referrer.
			if updated, err := v.Load(b.Path); err == nil {
				_ = idx.Upsert(toIndexNote(updated))
			}
			rewritten = append(rewritten, b.Path)
		}
	}

	// Reindex the moved note under its new path and purge the old entry.
	if err := idx.Delete(fromRel); err != nil {
		return rewritten, fmt.Errorf("index delete old: %w", err)
	}
	if moved, err := v.Load(toRel); err == nil {
		if err := idx.Upsert(toIndexNote(moved)); err != nil {
			return rewritten, fmt.Errorf("index upsert new: %w", err)
		}
	}
	return rewritten, nil
}

// rewriteWikiLinks returns a new body with every wiki-link pointing at
// oldBase (or at the old full path) rewritten to newBase. Alias text is
// preserved. Targets outside the match set are left untouched.
func rewriteWikiLinks(body []byte, oldBase, newBase, oldRel, newRel string) []byte {
	oldRelNoExt := strings.TrimSuffix(oldRel, ".md")
	newRelNoExt := strings.TrimSuffix(newRel, ".md")

	replaced := wikiLinkRegex.ReplaceAllFunc(body, func(match []byte) []byte {
		sub := wikiLinkRegex.FindSubmatch(match)
		target, alias := splitWikiLink(string(sub[1]))

		// Decide what to replace the target with.
		replacement := ""
		switch {
		case strings.EqualFold(target, oldBase):
			replacement = newBase
		case strings.EqualFold(target, oldRel), strings.EqualFold(target, oldRelNoExt):
			replacement = newRelNoExt
		case strings.HasSuffix(strings.ToLower(target), "/"+strings.ToLower(oldBase)):
			// Folder-qualified target like [[sub/OldBase]] — swap the tail.
			replacement = target[:len(target)-len(oldBase)] + newBase
		}
		if replacement == "" {
			return match // no change
		}
		if alias != "" {
			return []byte("[[" + replacement + "|" + alias + "]]")
		}
		return []byte("[[" + replacement + "]]")
	})
	return replaced
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// MoveNote moves a note from its current location to the given target
// project. Internally it computes the new vault-relative path
// (<toProject>/<basename>) and delegates to RenameNote — which keeps the
// wiki-link rewrite + index reindex logic in one place. An empty toProject
// moves the note to the vault root.
func (v *Vault) MoveNote(idx *index.Index, from, toProject string) ([]string, error) {
	fromRel, err := v.Rel(from)
	if err != nil {
		return nil, fmt.Errorf("invalid source: %w", err)
	}
	base := filepath.Base(fromRel)
	target := base
	if toProject = strings.TrimSpace(toProject); toProject != "" {
		clean, err := sanitizeProjectName(toProject)
		if err != nil {
			return nil, fmt.Errorf("invalid project: %w", err)
		}
		target = clean + "/" + base
	}
	return v.RenameNote(idx, fromRel, target)
}

// RenameProject renames a top-level project directory and reindexes all its
// notes under the new prefix. Wiki-links by basename keep resolving because
// the note filenames don't change; full-path wiki-links are rewritten to use
// the new prefix.
func (v *Vault) RenameProject(idx *index.Index, from, to string) error {
	fromClean, err := sanitizeProjectName(from)
	if err != nil {
		return fmt.Errorf("source name: %w", err)
	}
	toClean, err := sanitizeProjectName(to)
	if err != nil {
		return fmt.Errorf("target name: %w", err)
	}
	if fromClean == toClean {
		return nil
	}

	fromAbs := filepath.Join(v.Root, fromClean)
	toAbs := filepath.Join(v.Root, toClean)

	st, err := os.Stat(fromAbs)
	if err != nil {
		return fmt.Errorf("source: %w", err)
	}
	if !st.IsDir() {
		return fmt.Errorf("%q is not a directory", fromClean)
	}
	if _, err := os.Stat(toAbs); err == nil {
		return fmt.Errorf("target %q already exists", toClean)
	}

	// Snapshot the notes that live in the project before the rename: we'll
	// need to shift their paths in the index afterwards.
	oldNotes, err := idx.NotesByPrefix(fromClean)
	if err != nil {
		return err
	}

	if err := os.Rename(fromAbs, toAbs); err != nil {
		return err
	}

	// Rebuild index entries under the new prefix.
	for _, n := range oldNotes {
		newPath := toClean + strings.TrimPrefix(n.Path, fromClean)
		note, err := v.Load(newPath)
		if err != nil {
			continue
		}
		if err := idx.Upsert(toIndexNote(note)); err != nil {
			return fmt.Errorf("index upsert %s: %w", newPath, err)
		}
		if err := idx.Delete(n.Path); err != nil {
			return fmt.Errorf("index delete %s: %w", n.Path, err)
		}
	}
	return nil
}

func toIndexNote(n *Note) index.NoteDoc {
	return index.NoteDoc{
		Path:    n.Path,
		Title:   n.Title,
		Body:    string(n.Content),
		ModTime: n.ModTime.Unix(),
		Size:    n.Size,
	}
}
