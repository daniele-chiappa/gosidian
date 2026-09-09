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
	"sync"
	"time"

	"github.com/gosidian/gosidian/internal/index"
)

type Vault struct {
	Root       string
	cache      *loadCache
	htmlNotes  bool
	mediaNotes bool
	tableNotes bool
	locks      sync.Map // canonical rel path → *sync.Mutex, see LockPath
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

// SetHTMLNotes toggles whether single-file .html files are treated as
// first-class notes (enumerated, indexed, served, rendered in a sandboxed
// iframe). Default false. Wired from [vault] html_notes / GOSIDIAN_VAULT_HTML_NOTES.
// See ADR-011 for the security model.
func (v *Vault) SetHTMLNotes(on bool) { v.htmlNotes = on }

// HTMLNotesEnabled reports whether .html notes are active.
func (v *Vault) HTMLNotesEnabled() bool { return v.htmlNotes }

// SetMediaNotes toggles whether markdown notes whose frontmatter declares
// `type: image` + a `media:` pointer are resolved as image media notes (the
// pointer is checked and exposed as a structured MediaRef on read). Default
// false. Wired from [vault] media_notes / GOSIDIAN_VAULT_MEDIA_NOTES.
// See ADR-013. Note: media notes are plain .md files, so this flag does NOT
// change noteExtensions — only the media overlay surfaced by MediaRefForNote.
func (v *Vault) SetMediaNotes(on bool) { v.mediaNotes = on }

// MediaNotesEnabled reports whether image media notes are resolved.
func (v *Vault) MediaNotesEnabled() bool { return v.mediaNotes }

// SetTableNotes toggles whether markdown notes whose frontmatter declares
// `type: table` + a `media:` pointer to a .csv attachment are resolved as
// table notes (rendered as a table in the SPA). Default false. Wired from
// [vault] table_notes / GOSIDIAN_VAULT_TABLE_NOTES. See ADR-016. Like media
// notes, table notes are plain .md files — noteExtensions is untouched.
func (v *Vault) SetTableNotes(on bool) { v.tableNotes = on }

// TableNotesEnabled reports whether CSV table notes are resolved.
func (v *Vault) TableNotesEnabled() bool { return v.tableNotes }

// noteExtensions are the file extensions gosidian recognises as notes. Markdown
// is always a note; .html is gated by the htmlNotes feature flag (IsNoteFile).
var noteExtensions = []string{".md", ".html"}

// NoteExtensions returns the recognised note-file extensions, flag-independent
// (lowercase, with leading dot). For callers outside the vault (trash cleanup,
// preview link resolution) that must not hardcode ".md" — over-matching a
// disabled extension is harmless there, missing one is a stale-index bug.
func NoteExtensions() []string { return append([]string(nil), noteExtensions...) }

// IsNoteFile reports whether name (a path or basename) is a note file: always
// true for .md, true for .html only when the html-notes feature is enabled.
func (v *Vault) IsNoteFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md":
		return true
	case ".html":
		return v.htmlNotes
	}
	return false
}

// stripNoteExt removes a trailing note extension (.md/.html, case-insensitive)
// from p, leaving any other suffix untouched. Used for basename-based
// wikilink rewriting where the link text carries no extension.
func stripNoteExt(p string) string {
	low := strings.ToLower(p)
	for _, e := range noteExtensions {
		if strings.HasSuffix(low, e) {
			return p[:len(p)-len(e)]
		}
	}
	return p
}

// ErrNotNote is returned by Save, Delete and RenameNote when the path is not a
// note file (ADR-021): the vault layer mutates only notes; attachment bytes go
// through SaveAttachment/DeleteAttachment.
var ErrNotNote = errors.New("not a note file")

// ErrNotAttachment is returned by SaveAttachment/DeleteAttachment when the
// path is outside an attachments/ directory or its extension is not allowed.
var ErrNotAttachment = errors.New("not an attachment path")

// Rel returns a cleaned vault-relative path. Rejects any path that contains
// a ".." segment (attempted escape) and any hidden segment — one starting
// with "." — so the machine-owned .gosidian/ credential store, the .git/
// metadata and dotfiles are never addressable through the vault layer, by
// any consumer (HTTP notes API, /vault-files/, MCP tools and resources,
// attachments). Invariant (ADR-020, BUG-029): the vault layer addresses only
// what the scanner indexes; List/ScanInto/ListAttachments/watcher skip the
// same hidden entries. The error is deliberately the same generic one as for
// "..": callers turn it into 400/404 without revealing what exists.
func (v *Vault) Rel(p string) (string, error) {
	raw := filepath.ToSlash(p)
	for _, seg := range strings.Split(raw, "/") {
		if seg == ".." || isHidden(seg) {
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

// isHidden reports whether a single path segment is a hidden entry: it starts
// with "." and is not the "." no-op segment. ".." is handled separately.
func isHidden(seg string) bool {
	return len(seg) > 1 && seg[0] == '.' && seg != ".."
}

func (v *Vault) Abs(rel string) (string, error) {
	r, err := v.Rel(rel)
	if err != nil {
		return "", err
	}
	return filepath.Join(v.Root, filepath.FromSlash(r)), nil
}

// Load reads a note. Only note files are readable through the vault layer
// (ADR-021, IMP-080): attachment bytes are served by /vault-files/ and
// described by the attachment tools, never returned as Note.Content.
func (v *Vault) Load(rel string) (*Note, error) {
	r, err := v.Rel(rel)
	if err != nil {
		return nil, err
	}
	if !v.IsNoteFile(r) {
		return nil, fmt.Errorf("%w: %q", ErrNotNote, r)
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

// Delete removes a note. Like Save it refuses non-note paths (ADR-021);
// attachments are removed with DeleteAttachment.
func (v *Vault) Delete(rel string) error {
	abs, err := v.Abs(rel)
	if err != nil {
		return err
	}
	if !v.IsNoteFile(abs) {
		return fmt.Errorf("%w: %q", ErrNotNote, rel)
	}
	if v.cache != nil {
		// We need the cleaned relative path for the cache key; reuse Rel.
		if r, rErr := v.Rel(rel); rErr == nil {
			v.cache.Invalidate(r)
		}
	}
	return os.Remove(abs)
}

// Save writes a note. Only note files (IsNoteFile) can be written through the
// vault layer (ADR-021); attachment bytes go through SaveAttachment.
func (v *Vault) Save(rel string, content []byte) error {
	abs, err := v.Abs(rel)
	if err != nil {
		return err
	}
	if !v.IsNoteFile(abs) {
		return fmt.Errorf("%w: %q", ErrNotNote, rel)
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

// SaveAttachment writes attachment bytes. The path must sit inside an
// attachments/ directory and carry an extension present in allowedExt (the
// caller passes attach.ExtSet(); vault must not import attach). Together with
// Save's note-only rule this is the only way non-note bytes enter the vault
// (ADR-021).
func (v *Vault) SaveAttachment(rel string, content []byte, allowedExt map[string]bool) error {
	r, err := v.Rel(rel)
	if err != nil {
		return err
	}
	if err := checkAttachmentPath(r, allowedExt); err != nil {
		return err
	}
	abs := filepath.Join(v.Root, filepath.FromSlash(r))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(abs, content, 0o644)
}

// DeleteAttachment removes an attachment file under the same confinement as
// SaveAttachment.
func (v *Vault) DeleteAttachment(rel string, allowedExt map[string]bool) error {
	r, err := v.Rel(rel)
	if err != nil {
		return err
	}
	if err := checkAttachmentPath(r, allowedExt); err != nil {
		return err
	}
	return os.Remove(filepath.Join(v.Root, filepath.FromSlash(r)))
}

func checkAttachmentPath(r string, allowedExt map[string]bool) error {
	if !strings.Contains("/"+r, "/attachments/") {
		return fmt.Errorf("%w: %q is not inside an attachments/ directory", ErrNotAttachment, r)
	}
	if ext := strings.ToLower(filepath.Ext(r)); !allowedExt[ext] {
		return fmt.Errorf("%w: extension %q is not allowed", ErrNotAttachment, ext)
	}
	return nil
}

// List returns all note paths, sorted.
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
		// Hidden files are not notes even with a note extension: keep the
		// index aligned with what Rel can address (ADR-020).
		if isHidden(d.Name()) || !v.IsNoteFile(d.Name()) {
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
//
// ModTime is the directory's last-modification timestamp, used as a
// proxy for "last activity" (fs birth time isn't preserved by rsync,
// git checkout, or container layer copy, so it would be misleading).
type Project struct {
	Name      string
	NoteCount int
	ModTime   time.Time
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
		var modTime time.Time
		if info, err := e.Info(); err == nil {
			modTime = info.ModTime()
		}
		out = append(out, Project{Name: name, NoteCount: count, ModTime: modTime})
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
		if v.IsNoteFile(d.Name()) {
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
		if !v.IsNoteFile(d.Name()) {
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
			if isHidden(d.Name()) { // e.g. macOS ._resource forks: unaddressable via Rel
				return nil
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
	// Preserve the source's note extension when the target carries none, so
	// renaming `note.html` to `renamed` keeps it a `.html` note. An explicit
	// extension on the target (e.g. switching .md→.html) is honoured as-is.
	if filepath.Ext(toRel) == "" {
		toRel += filepath.Ext(fromRel)
	}
	// ADR-021: a rename never turns a note into a non-note (or vice versa).
	if !v.IsNoteFile(fromRel) || !v.IsNoteFile(toRel) {
		return nil, fmt.Errorf("%w: %q -> %q", ErrNotNote, fromRel, toRel)
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

	oldBase := stripNoteExt(filepath.Base(fromRel))
	newBase := stripNoteExt(filepath.Base(toRel))

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
	oldRelNoExt := stripNoteExt(oldRel)
	newRelNoExt := stripNoteExt(newRel)

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
