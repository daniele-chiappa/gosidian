package vault

import (
	"bytes"
	"cmp"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/gosidian/gosidian/internal/index"
)

// vaultFileRegex finds the links to attachments as attach.MarkdownRef
// writes them: /vault-files/ and the file's vault path.
var vaultFileRegex = regexp.MustCompile(`/vault-files/([^\s()<>"'\]]+)`)

// frontmatterBlockRegex is the frontmatter at the top of a markdown note.
var frontmatterBlockRegex = regexp.MustCompile(`(?s)\A---\r?\n.*?\r?\n---(?:\r?\n|\z)`)

// pointerLineRegex is a frontmatter's pointer to a file, maybe quoted: the
// media: of an image note, the fields of a note's design.
var pointerLineRegex = regexp.MustCompile(`(?m)^(?:media|layout_file|layout_preview|layout_pdf):[ \t]*["']?([^"'\r\n]*?)["']?[ \t]*\r?$`)

// pdfsLineRegex is the list of the PDFs a note's design keeps, and
// pdfItemRegex the path of each of its items, "label: path" (IMP-229).
var (
	pdfsLineRegex = regexp.MustCompile(`(?m)^layout_pdfs:[^\r\n]*`)
	pdfItemRegex  = regexp.MustCompile(`: ([^"',\]\s]+)`)
)

// projectRef is a reference of a note to a file of a project: where the
// project's name is in the note's source, and the file's path under the
// project. A wiki-link may name a note without its extension.
type projectRef struct {
	start, end int
	rest       string
	note       bool
}

// projectRefs finds, in a note's source, the references to files of project
// from: the wiki-links and embeds whose target starts with from/, in the body
// and the frontmatter, the links /vault-files/from/… and the frontmatter's
// pointers to files, outside code (BUG-153). The project's name in a wiki-link
// is matched without case, as the index resolves it; in a file's path, as
// the disk has it.
func projectRefs(body []byte, from string) []projectRef {
	code := codeSpans(body)
	inCode := func(pos int) bool {
		for _, c := range code {
			if c[0] <= pos && pos < c[1] {
				return true
			}
		}
		return false
	}
	var refs []projectRef
	for _, m := range wikiLinkRegex.FindAllSubmatchIndex(body, -1) {
		if inCode(m[0]) {
			continue
		}
		inner := string(body[m[2]:m[3]])
		target, _ := splitWikiLink(inner)
		if i := strings.IndexByte(target, '#'); i >= 0 {
			target = target[:i]
		}
		lead := len(inner) - len(strings.TrimLeft(inner, " \t"))
		if len(target) <= len(from)+1 || target[len(from)] != '/' || !strings.EqualFold(target[:len(from)], from) {
			continue
		}
		start := m[2] + lead
		refs = append(refs, projectRef{start: start, end: start + len(from), rest: target[len(from)+1:], note: true})
	}
	for _, m := range vaultFileRegex.FindAllSubmatchIndex(body, -1) {
		if inCode(m[0]) {
			continue
		}
		p := string(body[m[2]:m[3]])
		if !strings.HasPrefix(p, from+"/") {
			continue
		}
		rest, err := url.PathUnescape(p[len(from)+1:])
		if err != nil {
			continue
		}
		refs = append(refs, projectRef{start: m[2], end: m[2] + len(from), rest: rest})
	}
	if fm := frontmatterBlockRegex.FindIndex(body); fm != nil {
		for _, m := range pointerLineRegex.FindAllSubmatchIndex(body[:fm[1]], -1) {
			p := string(body[m[2]:m[3]])
			if strings.HasPrefix(p, from+"/") {
				refs = append(refs, projectRef{start: m[2], end: m[2] + len(from), rest: p[len(from)+1:]})
			}
		}
		for _, line := range pdfsLineRegex.FindAllIndex(body[:fm[1]], -1) {
			for _, m := range pdfItemRegex.FindAllSubmatchIndex(body[line[0]:line[1]], -1) {
				start := line[0] + m[2]
				p := string(body[start : line[0]+m[3]])
				if strings.HasPrefix(p, from+"/") {
					refs = append(refs, projectRef{start: start, end: start + len(from), rest: p[len(from)+1:]})
				}
			}
		}
	}
	return refs
}

// names reports whether r names a file there under project, a note maybe
// without its extension.
func (r projectRef) names(project string, exists func(rel string) bool) bool {
	p := project + "/" + r.rest
	if exists(p) {
		return true
	}
	if r.note {
		for _, e := range noteExtensions {
			if exists(p + e) {
				return true
			}
		}
	}
	return false
}

// rewriteProjectRefs rewrites, in a note's source, the references to files
// of project from into references to the same files of project to, where
// exists says the file is there under to: a link that led nowhere, or by
// name elsewhere, stays as written. Fragments, aliases and extensions are
// kept, only the project's name changes.
func rewriteProjectRefs(body []byte, from, to string, exists func(rel string) bool) []byte {
	refs := projectRefs(body, from)
	var out bytes.Buffer
	last := 0
	for _, r := range sortedRefs(refs) {
		if r.start < last || !r.names(to, exists) {
			continue
		}
		out.Write(body[last:r.start])
		out.WriteString(to)
		last = r.end
	}
	if last == 0 {
		return body
	}
	out.Write(body[last:])
	return out.Bytes()
}

// sortedRefs orders refs by their place in the source: the wiki-links, the
// file links and the media pointer are found one kind after the other.
func sortedRefs(refs []projectRef) []projectRef {
	out := slices.Clone(refs)
	slices.SortFunc(out, func(a, b projectRef) int { return cmp.Compare(a.start, b.start) })
	return out
}

// exists reports whether rel is a file of the vault.
func (v *Vault) exists(rel string) bool {
	clean, err := v.Rel(rel)
	if err != nil {
		return false
	}
	st, err := os.Stat(filepath.Join(v.Root, filepath.FromSlash(clean)))
	return err == nil && !st.IsDir()
}

// ProjectReferrers lists the notes that name a file of project from (its
// own included), as projectRefs reads them, with a reference to a file that
// is there: the notes a rename of the project rewrites. It reads every note
// of the vault, as Unreferenced does, hidden folders and the trash left out.
func (v *Vault) ProjectReferrers(from string) ([]string, error) {
	needle := []byte(strings.ToLower(from) + "/")
	var out []string
	err := filepath.WalkDir(v.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != v.Root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "node_modules") {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !v.IsNoteFile(d.Name()) {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil || !bytes.Contains(bytes.ToLower(b), needle) {
			return nil
		}
		for _, r := range projectRefs(b, from) {
			if r.names(from, v.exists) {
				rel, err := filepath.Rel(v.Root, p)
				if err == nil {
					out = append(out, filepath.ToSlash(rel))
				}
				break
			}
		}
		return nil
	})
	return out, err
}

// RenameProjectRewriting renames project from to to as RenameProject does,
// and rewrites the references to the project's files in the notes
// referrers names, by their paths before the rename (ProjectReferrers): its
// own notes under the new name, the others where they are (BUG-153). It
// returns the notes rewritten, by their paths after the rename. Every note
// is reindexed once it is rewritten, so their links resolve.
func (v *Vault) RenameProjectRewriting(idx *index.Index, from, to string, referrers []string) ([]string, error) {
	fromClean, err := v.CheckExistingProject(from)
	if err != nil {
		return nil, fmt.Errorf("source name: %w", err)
	}
	toClean, err := v.CheckProject(to)
	if err != nil {
		return nil, fmt.Errorf("target name: %w", err)
	}
	if fromClean == toClean {
		return nil, nil
	}
	fromAbs := filepath.Join(v.Root, fromClean)
	toAbs := filepath.Join(v.Root, toClean)
	st, err := os.Stat(fromAbs)
	if err != nil {
		return nil, fmt.Errorf("source: %w", err)
	}
	if !st.IsDir() {
		return nil, fmt.Errorf("%q is not a directory", fromClean)
	}
	if _, err := os.Stat(toAbs); err == nil {
		return nil, fmt.Errorf("target %q already exists", toClean)
	}

	// Snapshot the notes that live in the project before the rename: we'll
	// need to shift their paths in the index afterwards.
	oldNotes, err := idx.NotesByPrefix(fromClean)
	if err != nil {
		return nil, err
	}
	if err := os.Rename(fromAbs, toAbs); err != nil {
		return nil, err
	}

	// The references first, so the project's notes are indexed rewritten.
	var rewritten, outside []string
	var rerr error
	for _, r := range referrers {
		p := r
		if strings.HasPrefix(r, fromClean+"/") {
			p = toClean + strings.TrimPrefix(r, fromClean)
		}
		note, err := v.Load(p)
		if err != nil {
			continue
		}
		body := rewriteProjectRefs(note.Content, fromClean, toClean, v.exists)
		if bytes.Equal(body, note.Content) {
			continue
		}
		if err := v.Save(p, body); err != nil {
			rerr = fmt.Errorf("rewrite %s: %w", p, err)
			break
		}
		rewritten = append(rewritten, p)
		if p == r {
			outside = append(outside, p)
		}
	}

	// Rebuild index entries under the new prefix.
	for _, n := range oldNotes {
		newPath := toClean + strings.TrimPrefix(n.Path, fromClean)
		note, err := v.Load(newPath)
		if err != nil {
			continue
		}
		if err := idx.Upsert(toIndexNote(note)); err != nil {
			return rewritten, fmt.Errorf("index upsert %s: %w", newPath, err)
		}
		if err := idx.Delete(n.Path); err != nil {
			return rewritten, fmt.Errorf("index delete %s: %w", n.Path, err)
		}
	}
	for _, p := range outside {
		if note, err := v.Load(p); err == nil {
			_ = idx.Upsert(toIndexNote(note))
		}
	}
	return rewritten, rerr
}
