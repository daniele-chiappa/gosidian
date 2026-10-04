// Package pkgimport reads a package of files, a folder or a .zip, and plans
// its import into the vault (IMP-116): which files become notes and which
// attachments, where each one goes, its links rewritten for the vault, and
// what is left out and why. It writes nothing; the caller checks the plan
// (access, limits, existing notes) and applies it all or not at all.
package pkgimport

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Entry is a file of a package: its path inside the package, slash
// separated and clean, and its bytes.
type Entry struct {
	Path string
	Data []byte
}

// Skipped is a file left out of a package, and why.
type Skipped struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Limits caps a package: its files and its total size unpacked.
type Limits struct {
	MaxFiles int
	MaxBytes int64
}

// ErrUnsafe reports a package that names a path outside itself (an
// absolute path, or one that climbs with ..): such a package is refused
// whole, since its other paths cannot be trusted either.
var ErrUnsafe = errors.New("unsafe path in package")

// ErrTooLarge reports a package past its limits.
var ErrTooLarge = errors.New("package too large")

// skipName reports a file or folder a package never imports: hidden ones
// (.git, .DS_Store) and the __MACOSX folder a macOS zip adds.
func skipName(name string) bool {
	return strings.HasPrefix(name, ".") || name == "__MACOSX"
}

// ReadDir reads a package from a folder. Symbolic links are not followed:
// they are left out, as are hidden files and folders.
func ReadDir(root string, lim Limits) ([]Entry, []Skipped, error) {
	var out []Entry
	var skipped []Skipped
	var total int64
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if skipName(d.Name()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			skipped = append(skipped, Skipped{Path: rel, Reason: "symbolic link, not followed"})
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			skipped = append(skipped, Skipped{Path: rel, Reason: "not a regular file"})
			return nil
		}
		if len(out)+1 > lim.MaxFiles {
			return fmt.Errorf("%w: more than %d files", ErrTooLarge, lim.MaxFiles)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if total += info.Size(); total > lim.MaxBytes {
			return fmt.Errorf("%w: more than %d bytes", ErrTooLarge, lim.MaxBytes)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out = append(out, Entry{Path: rel, Data: data})
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return out, skipped, nil
}

// ReadZip reads a package from the bytes of a .zip. A folder that wraps
// every entry (the zip of a folder) is dropped, so the package's files are
// what the folder held. Entries are read up to the limits, never past them,
// whatever sizes the zip declares.
func ReadZip(data []byte, lim Limits) ([]Entry, []Skipped, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, nil, fmt.Errorf("not a zip: %w", err)
	}
	var out []Entry
	var skipped []Skipped
	var total int64
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, `\`, "/")
		if strings.HasPrefix(name, "/") || hasDotDot(name) || strings.Contains(name, ":") {
			return nil, nil, fmt.Errorf("%w: %q", ErrUnsafe, f.Name)
		}
		name = path.Clean(name)
		if f.FileInfo().IsDir() || name == "." {
			continue
		}
		if hiddenPath(name) {
			continue
		}
		if f.Mode()&fs.ModeSymlink != 0 {
			skipped = append(skipped, Skipped{Path: name, Reason: "symbolic link, not followed"})
			continue
		}
		if !f.Mode().IsRegular() {
			skipped = append(skipped, Skipped{Path: name, Reason: "not a regular file"})
			continue
		}
		if len(out)+1 > lim.MaxFiles {
			return nil, nil, fmt.Errorf("%w: more than %d files", ErrTooLarge, lim.MaxFiles)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, nil, fmt.Errorf("zip entry %q: %w", f.Name, err)
		}
		// Read at most what is left of the budget, plus one byte to tell
		// a package past it: a declared size proves nothing.
		b, err := io.ReadAll(io.LimitReader(rc, lim.MaxBytes-total+1))
		rc.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("zip entry %q: %w", f.Name, err)
		}
		if total += int64(len(b)); total > lim.MaxBytes {
			return nil, nil, fmt.Errorf("%w: more than %d bytes unpacked", ErrTooLarge, lim.MaxBytes)
		}
		out = append(out, Entry{Path: name, Data: b})
	}
	if w := wrapper(out, skipped); w != "" {
		for i := range out {
			out[i].Path = strings.TrimPrefix(out[i].Path, w+"/")
		}
		for i := range skipped {
			skipped[i].Path = strings.TrimPrefix(skipped[i].Path, w+"/")
		}
	}
	return out, skipped, nil
}

func hasDotDot(name string) bool {
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

func hiddenPath(name string) bool {
	for _, seg := range strings.Split(name, "/") {
		if skipName(seg) {
			return true
		}
	}
	return false
}

// wrapper returns the folder every entry sits in, "" when there is none.
func wrapper(es []Entry, sk []Skipped) string {
	top := ""
	for _, p := range append(entryPaths(es), skippedPaths(sk)...) {
		dir, _, ok := strings.Cut(p, "/")
		if !ok || top != "" && dir != top {
			return ""
		}
		top = dir
	}
	return top
}

func entryPaths(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Path
	}
	return out
}

func skippedPaths(sk []Skipped) []string {
	out := make([]string, len(sk))
	for i, s := range sk {
		out[i] = s.Path
	}
	return out
}

// Options tell Build what the vault accepts.
type Options struct {
	Project string // the project of Dest: the tags of an added frontmatter
	Dest    string // vault folder the package goes into
	// IsNote reports a file that becomes a note (.md, .html when enabled).
	IsNote func(name string) bool
	// AttachmentPath returns the vault path an attachment is stored at
	// (content-addressed), or an error when the vault refuses the file
	// (extension, content). Nil leaves every other file out.
	AttachmentPath func(name string, data []byte) (string, error)
}

// Note is a note of the plan: where it goes and its final text.
type Note struct {
	Src  string `json:"src"`
	Path string `json:"path"`
	// FrontmatterAdded reports a note that had none: it gets a title and
	// the project's tag, as every note of the vault has.
	FrontmatterAdded bool     `json:"frontmatter_added,omitempty"`
	Unresolved       []string `json:"unresolved,omitempty"`
	Data             []byte   `json:"-"`
}

// Attachment is an attachment of the plan.
type Attachment struct {
	Src  string `json:"src"`
	Path string `json:"path"`
	Data []byte `json:"-"`
}

// Plan is what importing a package would write.
type Plan struct {
	Notes       []Note       `json:"notes"`
	Attachments []Attachment `json:"attachments,omitempty"`
	Skipped     []Skipped    `json:"skipped,omitempty"`
}

// Build plans the import of entries under o.Dest: notes keep their path
// inside the package, attachments go where the vault stores them, and the
// links between files of the package are rewritten for the vault (see
// rewriteMarkdown and rewriteHTML). Two files whose paths differ only by
// case are an error, since they would collide on some file systems.
func Build(entries []Entry, skipped []Skipped, o Options) (*Plan, error) {
	p := &Plan{Skipped: append([]Skipped(nil), skipped...)}
	notes := map[string]string{}  // package path → vault path
	attach := map[string]string{} // package path → vault path
	seen := map[string]string{}
	var noteEntries []Entry
	for _, e := range entries {
		low := strings.ToLower(e.Path)
		if prev, dup := seen[low]; dup {
			return nil, fmt.Errorf("%q and %q differ only by case", prev, e.Path)
		}
		seen[low] = e.Path
		switch {
		case o.IsNote != nil && o.IsNote(e.Path):
			notes[e.Path] = o.Dest + "/" + e.Path
			noteEntries = append(noteEntries, e)
		case o.AttachmentPath != nil:
			vp, err := o.AttachmentPath(e.Path, e.Data)
			if err != nil {
				p.Skipped = append(p.Skipped, Skipped{Path: e.Path, Reason: err.Error()})
				continue
			}
			attach[e.Path] = vp
			p.Attachments = append(p.Attachments, Attachment{Src: e.Path, Path: vp, Data: e.Data})
		default:
			p.Skipped = append(p.Skipped, Skipped{Path: e.Path, Reason: "not a note"})
		}
	}
	for _, e := range noteEntries {
		n := Note{Src: e.Path, Path: notes[e.Path]}
		var text string
		if strings.EqualFold(path.Ext(e.Path), ".html") {
			text, n.Unresolved = rewriteHTML(string(e.Data), e.Path, notes, attach)
			text, n.FrontmatterAdded = withHTMLFrontmatter(text, e.Path, o.Project)
		} else {
			text, n.Unresolved = rewriteMarkdown(string(e.Data), e.Path, notes, attach)
			text, n.FrontmatterAdded = withMarkdownFrontmatter(text, e.Path, o.Project)
		}
		n.Data = []byte(text)
		p.Notes = append(p.Notes, n)
	}
	sort.Slice(p.Notes, func(i, j int) bool { return p.Notes[i].Path < p.Notes[j].Path })
	sort.Slice(p.Skipped, func(i, j int) bool { return p.Skipped[i].Path < p.Skipped[j].Path })
	return p, nil
}

// external reports a link target that is not a file of the package: a URL
// with a scheme, a path from the server root, or an anchor in the page.
var schemeRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

func external(t string) bool {
	return t == "" || schemeRe.MatchString(t) || strings.HasPrefix(t, "/") || strings.HasPrefix(t, "#")
}

// target resolves a relative link of the package file src: the package path
// it names and its fragment ("#…", or ""); ok is false when it climbs out of
// the package.
func target(src, link string) (p, frag string, ok bool) {
	if i := strings.IndexByte(link, '#'); i >= 0 {
		link, frag = link[:i], link[i:]
	}
	if i := strings.IndexByte(link, '?'); i >= 0 {
		link = link[:i]
	}
	if dec, err := url.PathUnescape(link); err == nil {
		link = dec
	}
	p = path.Join(path.Dir(src), link)
	if p == ".." || strings.HasPrefix(p, "../") {
		return "", "", false
	}
	return p, frag, true
}

// mdLinkRe matches an inline markdown link or image: [text](target) or
// ![alt](target), the target optionally in <> and followed by a "title".
var mdLinkRe = regexp.MustCompile(`(!?)\[([^\]\n]*)\]\(\s*<?([^)\s>]+)>?(\s+"[^"\n]*")?\s*\)`)

// rewriteMarkdown rewrites the links of a markdown note of the package: a
// link to a note of the package becomes a wikilink to its vault path, with
// the fragment and the link text ([[dest/sub/x#part|text]]); a link to an
// attachment points at the vault file (/vault-files/…). Links in code are
// left alone. A relative link to a file that is not in the package stays
// as written and is listed as unresolved.
func rewriteMarkdown(text, src string, notes, attach map[string]string) (string, []string) {
	var unresolved []string
	lines := strings.SplitAfter(text, "\n")
	fence := ""
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			marker := trim[:3]
			switch {
			case fence == "":
				fence = marker
			case marker == fence:
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		lines[i] = outsideCode(line, func(s string) string {
			return mdLinkRe.ReplaceAllStringFunc(s, func(m string) string {
				g := mdLinkRe.FindStringSubmatch(m)
				bang, label, link := g[1], g[2], g[3]
				if external(link) {
					return m
				}
				p, frag, ok := target(src, link)
				if !ok {
					unresolved = append(unresolved, link)
					return m
				}
				if vp, isNote := notes[p]; isNote && bang == "" {
					w := "[[" + strings.TrimSuffix(vp, ".md") + frag
					if label = strings.TrimSpace(strings.NewReplacer("|", "-", "[", "(", "]", ")").Replace(label)); label != "" {
						w += "|" + label
					}
					return w + "]]"
				}
				if vp, isAttach := attach[p]; isAttach {
					return bang + "[" + label + "](/vault-files/" + vp + ")"
				}
				unresolved = append(unresolved, link)
				return m
			})
		})
	}
	return strings.Join(lines, ""), unresolved
}

// outsideCode applies fn to the parts of line that are not inline code.
func outsideCode(line string, fn func(string) string) string {
	parts := strings.Split(line, "`")
	if len(parts)%2 == 0 {
		// An unclosed backtick: no code span on this line.
		return fn(line)
	}
	for i := 0; i < len(parts); i += 2 {
		parts[i] = fn(parts[i])
	}
	return strings.Join(parts, "`")
}

// htmlAttrRe matches an href or src attribute.
var htmlAttrRe = regexp.MustCompile(`(?i)(\s(?:href|src)\s*=\s*)(["'])([^"']*)(["'])`)

// rewriteHTML rewrites the links of an HTML note of the package. A link to
// an attachment points at the vault file (/vault-files/…), which the web UI
// inlines for an image. A link to a note of the package stays relative:
// the notes keep their paths inside the package, and the web UI resolves
// it against the note's own (BUG-091). A relative link to a file that is
// not in the package is listed as unresolved.
func rewriteHTML(text, src string, notes, attach map[string]string) (string, []string) {
	var unresolved []string
	out := htmlAttrRe.ReplaceAllStringFunc(text, func(m string) string {
		g := htmlAttrRe.FindStringSubmatch(m)
		link := g[3]
		if external(link) {
			return m
		}
		p, _, ok := target(src, link)
		if !ok {
			unresolved = append(unresolved, link)
			return m
		}
		if vp, isAttach := attach[p]; isAttach {
			return g[1] + g[2] + "/vault-files/" + vp + g[4]
		}
		if _, isNote := notes[p]; !isNote {
			unresolved = append(unresolved, link)
		}
		return m
	})
	return out, unresolved
}

var (
	mdFrontmatterRe = regexp.MustCompile(`\A---\r?\n`)
	h1Re            = regexp.MustCompile(`(?m)^#[ \t]+(.+?)[ \t#]*$`)
	htmlTitleRe     = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>|<h1[^>]*>(.*?)</h1>`)
	tagRe           = regexp.MustCompile(`<[^>]+>`)
)

// minimalFrontmatter is the YAML of an added frontmatter: the title and the
// project's tag, both quoted (JSON strings are YAML).
func minimalFrontmatter(title, project string) string {
	t, _ := json.Marshal(title)
	p, _ := json.Marshal(project)
	return "---\ntitle: " + string(t) + "\ntags: [" + string(p) + "]\n---\n"
}

func stem(src string) string {
	return strings.TrimSuffix(path.Base(src), path.Ext(src))
}

// withMarkdownFrontmatter gives a markdown note without frontmatter a title
// (its first heading, or its file name) and the project's tag.
func withMarkdownFrontmatter(text, src, project string) (string, bool) {
	if mdFrontmatterRe.MatchString(text) {
		return text, false
	}
	title := stem(src)
	if m := h1Re.FindStringSubmatch(text); m != nil {
		title = strings.TrimSpace(m[1])
	}
	return minimalFrontmatter(title, project) + "\n" + text, true
}

// withHTMLFrontmatter does the same for an HTML note, in the leading
// comment where HTML notes keep it (ADR-011); the title comes from <title>,
// else the first <h1>, else the file name.
func withHTMLFrontmatter(text, src, project string) (string, bool) {
	trim := strings.TrimLeft(text, " \t\r\n\ufeff")
	if strings.HasPrefix(trim, "<!--") {
		if rest := strings.TrimLeft(trim[4:], " \t\r\n"); strings.HasPrefix(rest, "---") {
			return text, false
		}
	}
	if mdFrontmatterRe.MatchString(trim) {
		return text, false
	}
	title := stem(src)
	if m := htmlTitleRe.FindStringSubmatch(text); m != nil {
		t := m[1]
		if t == "" {
			t = m[2]
		}
		if t = strings.TrimSpace(tagRe.ReplaceAllString(t, "")); t != "" {
			title = t
		}
	}
	return "<!--\n" + minimalFrontmatter(title, project) + "-->\n" + text, true
}
