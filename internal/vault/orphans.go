package vault

import (
	"bytes"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// Orphan attachments (IMP-033): files under an attachments/ folder that no
// file of the vault names any more — a note deleted or edited, an upload
// never linked.

// referenceExts are the files that may name an attachment: notes, HTML
// notes (whatever the flag), canvases and bases.
var referenceExts = map[string]bool{".md": true, ".html": true, ".htm": true, ".canvas": true, ".base": true}

// ProjectAttachments lists every attachment of project: each file with an
// allowed extension inside an attachments/ folder anywhere under it.
func (v *Vault) ProjectAttachments(project string, allowedExt map[string]bool) ([]AttachmentInfo, error) {
	r, err := v.Rel(project)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(v.Root, filepath.FromSlash(r))
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return nil, nil
	}
	var out []AttachmentInfo
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return skipVanished(root, p, err)
		}
		if d.IsDir() {
			if v.skipDir(root, p) {
				return fs.SkipDir
			}
			return nil
		}
		if isHidden(d.Name()) || !d.Type().IsRegular() || !allowedExt[strings.ToLower(filepath.Ext(d.Name()))] {
			return nil
		}
		rel, err := filepath.Rel(v.Root, p)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if !strings.Contains("/"+rel, "/attachments/") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		out = append(out, AttachmentInfo{Path: rel, Size: info.Size(), ModTime: info.ModTime()})
		return nil
	})
	return out, err
}

// Unreferenced returns, of names (attachment basenames), those that no file
// of the vault names: the notes of every project, canvases and bases, and
// the notes in the trash, so that a restore does not find its image gone. A
// name counts as named however a link writes it: URL-escaped (a space as
// %20 or +, the hex digits in either case), in either Unicode form, in any
// case. Read as written only, a link to Image.PNG, to %c3%a9 or to an é
// typed on a Mac kept no file, and the collection removed it (IMP-163).
func (v *Vault) Unreferenced(names []string) (map[string]bool, error) {
	m := newRefMatcher(names)
	scan := func(root string, skipHidden bool) error {
		if _, err := os.Stat(root); err != nil {
			return nil
		}
		return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || len(m.left) == 0 {
				if len(m.left) == 0 {
					return fs.SkipAll
				}
				return nil
			}
			if d.IsDir() {
				if p != root && (d.Name() == ".git" || d.Name() == "node_modules" || (skipHidden && strings.HasPrefix(d.Name(), "."))) {
					return fs.SkipDir
				}
				return nil
			}
			if !referenceExts[strings.ToLower(filepath.Ext(d.Name()))] || !d.Type().IsRegular() {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			m.match(foldRef(string(b)))
			if bytes.IndexByte(b, '%') >= 0 {
				m.match(foldRef(percentDecode(string(b))))
			}
			return nil
		})
	}
	if err := scan(v.Root, true); err != nil {
		return nil, err
	}
	if err := scan(filepath.Join(v.Root, ".gosidian", "trash"), false); err != nil {
		return nil, err
	}
	return m.left, nil
}

// refMatcher finds which of a set of attachment names some texts name, as
// substrings of the texts folded by foldRef, in time linear in the texts
// (BUG-122: a Contains per name and file took hours on 30,000 attachments
// and 18,500 notes). A name ends with its extension, so a text names it only
// where an occurrence of that extension ends: there the matcher looks the
// stretch before up, at each length a name has, in a map of the names.
type refMatcher struct {
	byLen   map[int]map[string][]string // a form's length → the form → its names
	lengths []int
	exts    []string            // the extensions the forms end with
	plain   map[string][]string // the forms without an extension → their names
	left    map[string]bool     // the names not found yet
}

func newRefMatcher(names []string) *refMatcher {
	m := &refMatcher{byLen: map[int]map[string][]string{}, plain: map[string][]string{}, left: make(map[string]bool, len(names))}
	exts := map[string]bool{}
	for _, n := range names {
		m.left[n] = true
		f := foldRef(n)
		for _, form := range []string{f, strings.ReplaceAll(f, " ", "+")} {
			ext := path.Ext(form)
			if ext == "" || ext == form {
				m.plain[form] = append(m.plain[form], n)
				continue
			}
			exts[ext] = true
			forms := m.byLen[len(form)]
			if forms == nil {
				forms = map[string][]string{}
				m.byLen[len(form)] = forms
				m.lengths = append(m.lengths, len(form))
			}
			forms[form] = append(forms[form], n)
		}
	}
	for e := range exts {
		m.exts = append(m.exts, e)
	}
	return m
}

// match takes the names a folded text names out of left.
func (m *refMatcher) match(text string) {
	for _, ext := range m.exts {
		for i := 0; ; {
			j := strings.Index(text[i:], ext)
			if j < 0 {
				break
			}
			end := i + j + len(ext)
			for _, l := range m.lengths {
				if l <= end {
					for _, n := range m.byLen[l][text[end-l:end]] {
						delete(m.left, n)
					}
				}
			}
			i += j + 1
		}
	}
	for form, ns := range m.plain {
		if strings.Contains(text, form) {
			for _, n := range ns {
				delete(m.left, n)
			}
		}
	}
}

// foldRef is s as Unreferenced compares it: composed (NFC) and lower case.
func foldRef(s string) string { return strings.ToLower(norm.NFC.String(s)) }

// percentDecode decodes the %XX escapes of s and keeps every other byte, a
// lone "%" of the prose among them (url.PathUnescape refuses the whole text
// for one).
func percentDecode(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				b.WriteByte(byte(n))
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
