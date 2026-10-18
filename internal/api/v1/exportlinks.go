package v1

import (
	"archive/zip"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/authz"
)

// Exports with their attachments (IMP-204): a note in a zip keeps its
// images. Its references to files of the vault (/vault-files/… as the target
// of a markdown link or image, an ![[embed]] of an image, the src or href of
// an HTML note) become paths relative to the note inside the archive, and the
// files they name go into it: where they already are when the export holds
// them (a project's attachments/), else into attachments/ at the top of the
// export, once each. A reference the account may not read, or that names no
// file, stays as written. The stored note does not change.

var (
	// The target of a markdown link or image, [x](/vault-files/…) or
	// ![x](</vault-files/…>).
	exportMdLinkRe = regexp.MustCompile(`(\]\(\s*<?)(/vault-files/[^)\s>]+)`)
	// The src or href of an HTML note.
	exportHTMLRe = regexp.MustCompile(`(\b(?:src|href)\s*=\s*)(["'])(/vault-files/[^"']+)(["'])`)
)

// exportFiles is what one export knows of the files its notes name: their
// names in the archive, and the attachments it adds from outside the folder.
type exportFiles struct {
	r     *Router
	p     authz.Principal
	dir   string // the exported folder or note; "" for the vault
	strip string // the prefix of vault paths that names in the archive drop
	top   string // where the added attachments go: <top>attachments/
	extra map[string]string
	names map[string]string // name in the archive → vault path, of the added ones
	order []string
}

func newExportFiles(r *Router, p authz.Principal, dir, strip, top string) *exportFiles {
	return &exportFiles{r: r, p: p, dir: dir, strip: strip, top: top, extra: map[string]string{}, names: map[string]string{}}
}

// isExportNote says whether a file of an export is a note whose references
// are rewritten, and how to read them.
func isExportNote(rel string) (format string, ok bool) {
	switch strings.ToLower(path.Ext(rel)) {
	case ".md":
		return "markdown", true
	case ".html":
		return "html", true
	}
	return "", false
}

// target is the name in the archive of the file a /vault-files/ URL names,
// and adds it to the export when it lies outside the exported folder; ""
// when the reference stays as written. The gates are those of the
// /vault-files/ endpoint and of an inlined image (vaultURLToDataURI): only a
// file under an attachments/ folder, only one the account can read.
func (x *exportFiles) target(u string) string {
	raw := strings.TrimPrefix(u, "/vault-files/")
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	rel, err := url.PathUnescape(raw)
	if err != nil {
		return ""
	}
	clean, err := x.r.deps.Vault.Rel(rel)
	if err != nil || !strings.Contains("/"+clean, "/attachments/") || !x.r.canSee(x.p, clean) {
		return ""
	}
	abs, err := x.r.deps.Vault.Abs(clean)
	if err != nil {
		return ""
	}
	if info, err := os.Stat(abs); err != nil || !info.Mode().IsRegular() {
		return ""
	}
	if x.dir == "" || strings.HasPrefix(clean, x.dir+"/") {
		return strings.TrimPrefix(clean, x.strip) // the walk puts it there
	}
	if name, ok := x.extra[clean]; ok {
		return name
	}
	// Attachments are named by their content, so two with one name are the
	// same file; one of another project with a name already taken, or the
	// name of a file the folder holds itself, gets its project in front.
	name := x.top + "attachments/" + path.Base(clean)
	if other, taken := x.names[name]; (taken && other != clean) || x.inFolder(name, clean) {
		name = x.top + "attachments/" + projectOf(clean) + "-" + path.Base(clean)
	}
	x.extra[clean] = name
	x.names[name] = clean
	x.order = append(x.order, clean)
	return name
}

// inFolder says whether the folder exported holds another file than clean
// at name (a name in the archive): an added attachment must not take its
// place.
func (x *exportFiles) inFolder(name, clean string) bool {
	if x.dir == "" || x.strip+name == clean {
		return false
	}
	abs, err := x.r.deps.Vault.Abs(x.strip + name)
	if err != nil {
		return false
	}
	_, err = os.Lstat(abs)
	return err == nil
}

// rewrite is a note's content with its references to the vault's files
// made relative to note (its name in the archive), and how many it changed.
func (x *exportFiles) rewrite(note, content, format string) (string, int) {
	n := 0
	link := func(u string) string {
		t := x.target(u)
		if t == "" {
			return ""
		}
		n++
		return relativeLink(note, t)
	}
	if format == "html" {
		content = exportHTMLRe.ReplaceAllStringFunc(content, func(m string) string {
			sub := exportHTMLRe.FindStringSubmatch(m)
			if l := link(sub[3]); l != "" {
				return sub[1] + sub[2] + l + sub[4]
			}
			return m
		})
		return content, n
	}
	res := previewResolver{r: x.r, p: x.p}
	content = mdEmbedRe.ReplaceAllStringFunc(content, func(m string) string {
		sub := mdEmbedRe.FindStringSubmatch(m)
		target, alias := splitEmbed(sub[1])
		if u := res.ResolveImage(target); u != "" {
			if l := link(u); l != "" {
				return "![" + alias + "](" + l + ")"
			}
		}
		return m
	})
	content = exportMdLinkRe.ReplaceAllStringFunc(content, func(m string) string {
		sub := exportMdLinkRe.FindStringSubmatch(m)
		if l := link(sub[2]); l != "" {
			return sub[1] + l
		}
		return m
	})
	return content, n
}

// relativeLink is the path from the note at from to the file at to, both
// names in the archive, escaped for a markdown link or an HTML attribute.
func relativeLink(from, to string) string {
	rel, err := filepath.Rel(path.Dir(from), to)
	if err != nil {
		return to
	}
	return (&url.URL{Path: filepath.ToSlash(rel)}).EscapedPath()
}

// addExtras writes the attachments the notes named from outside the
// export, after its own files.
func (x *exportFiles) addExtras(zw *zip.Writer) (int, error) {
	n := 0
	for _, clean := range x.order {
		abs, err := x.r.deps.Vault.Abs(clean)
		if err != nil {
			continue
		}
		info, err := os.Stat(abs)
		if err != nil {
			continue
		}
		if err := addZipFile(zw, abs, x.extra[clean], info); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// addZipBytes writes data into the archive under name, with the time of
// info: a note whose references were rewritten.
func addZipBytes(zw *zip.Writer, name string, info fs.FileInfo, data []byte) error {
	fh, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	fh.Name = name
	fh.Method = zip.Deflate
	fh.UncompressedSize64 = uint64(len(data))
	dst, err := zw.CreateHeader(fh)
	if err != nil {
		return err
	}
	_, err = dst.Write(data)
	return err
}

// exportNote answers GET /notes/{path}?export=zip: a note that names
// attachments comes as a zip, the note at its root with its references made
// relative and the files under attachments/; one that names none comes as
// the file it is. It is a read of the note and of its images, like ?inline,
// not a bulk export: no export limit, no audit.
func (r *Router) exportNote(w http.ResponseWriter, req *http.Request, rel string, content []byte) {
	format, ok := isExportNote(rel)
	base := path.Base(rel)
	h := w.Header()
	attach.SetInertHeaders(h)
	h.Set("Cache-Control", "private, no-store")
	if ok {
		x := newExportFiles(r, principalFromContext(req), rel, path.Dir(rel)+"/", "")
		if out, n := x.rewrite(base, string(content), format); n > 0 && len(x.order) > 0 {
			name := strings.TrimSuffix(base, path.Ext(base)) + ".zip"
			h.Set("Content-Type", "application/zip")
			h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
			w.WriteHeader(http.StatusOK)
			zw := zip.NewWriter(w)
			info := fileInfoNow{name: base, size: int64(len(out))}
			err := addZipBytes(zw, base, info, []byte(out))
			if err == nil {
				_, err = x.addExtras(zw)
			}
			if err == nil {
				err = zw.Close()
			}
			if err != nil {
				// The 200 is out: abort, so the client does not keep a
				// truncated archive (as streamExport does).
				panic(http.ErrAbortHandler)
			}
			return
		}
	}
	ctype := "text/markdown; charset=utf-8"
	if format == "html" {
		ctype = "text/html; charset=utf-8"
	}
	h.Set("Content-Type", ctype)
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": base}))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

// fileInfoNow is the fs.FileInfo of a file made for an archive: a note
// rewritten for a download, dated now.
type fileInfoNow struct {
	name string
	size int64
}

func (f fileInfoNow) Name() string       { return f.name }
func (f fileInfoNow) Size() int64        { return f.size }
func (f fileInfoNow) Mode() fs.FileMode  { return 0o644 }
func (f fileInfoNow) ModTime() time.Time { return time.Now() }
func (f fileInfoNow) IsDir() bool        { return false }
func (f fileInfoNow) Sys() any           { return nil }
