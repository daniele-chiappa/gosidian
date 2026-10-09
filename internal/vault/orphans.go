package vault

import (
	"io/fs"
	"os"
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
	left := map[string][]string{}
	for _, n := range names {
		f := foldRef(n)
		left[n] = []string{f, strings.ReplaceAll(f, " ", "+")}
	}
	scan := func(root string, skipHidden bool) error {
		if _, err := os.Stat(root); err != nil {
			return nil
		}
		return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || len(left) == 0 {
				if len(left) == 0 {
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
			text := foldRef(string(b))
			decoded := foldRef(percentDecode(string(b)))
			for name, forms := range left {
				for _, f := range forms {
					if strings.Contains(text, f) || strings.Contains(decoded, f) {
						delete(left, name)
						break
					}
				}
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
	out := make(map[string]bool, len(left))
	for n := range left {
		out[n] = true
	}
	return out, nil
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
