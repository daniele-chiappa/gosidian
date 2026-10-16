package chatbot

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// ExpandFolders makes the scopes of a block's folders: a scope for each
// folder under root (the project's folders.path, as a path of the vault),
// down f.Levels levels, labelled with its name. Its filter keeps the chunks
// whose meta f.Key is that folder or one under it, at any depth: a
// document deeper than the levels still belongs to its folders. folders
// are the folders under root, at any depth; taken the names of the
// block's own scopes, which these do not reuse.
func ExpandFolders(f Folders, root string, folders []string, taken map[string]bool) []Scope {
	root = strings.Trim(root, "/")
	subs := map[string]map[string]bool{}
	for _, d := range folders {
		d = strings.Trim(d, "/")
		if !strings.HasPrefix(d, root+"/") {
			continue
		}
		// Every folder between root and d is one, with or without notes.
		for p := d; p != root; p = path.Dir(p) {
			parent := path.Dir(p)
			if subs[parent] == nil {
				subs[parent] = map[string]bool{}
			}
			subs[parent][p] = true
		}
	}
	sorted := func(dir string) []string {
		out := make([]string, 0, len(subs[dir]))
		for d := range subs[dir] {
			out = append(out, d)
		}
		sort.Strings(out)
		return out
	}
	var under func(dir string) []string
	under = func(dir string) []string {
		out := []string{dir}
		for _, d := range sorted(dir) {
			out = append(out, under(d)...)
		}
		return out
	}
	used := map[string]bool{}
	for n := range taken {
		used[n] = true
	}
	var build func(dir string, level int) []Scope
	build = func(dir string, level int) []Scope {
		var out []Scope
		for _, d := range sorted(dir) {
			sc := Scope{
				Name:   folderScopeName(strings.TrimPrefix(d, root+"/"), used),
				Label:  path.Base(d),
				Filter: map[string]any{"meta": map[string]any{f.Key: map[string]any{"in": under(d)}}},
			}
			if level < f.Levels {
				sc.Children = build(d, level+1)
			}
			out = append(out, sc)
		}
		return out
	}
	return build(root, 1)
}

// folderScopeName is the name of a folder's scope: its path under the
// root as a slug (Sales/Invoices: sales-invoices), with a piece
// of its hash when that is too long or taken.
func folderScopeName(rel string, used map[string]bool) string {
	n := slug(rel)
	if n == "" || n[0] < 'a' || n[0] > 'z' {
		n = "f-" + n
	}
	if len(n) > 48 || used[n] {
		sum := sha256.Sum256([]byte(rel))
		h := hex.EncodeToString(sum[:])[:6]
		if len(n) > 41 {
			n = strings.TrimRight(n[:41], "-")
		}
		n += "-" + h
	}
	used[n] = true
	return n
}

// slug makes a name of a text: lower-case letters without their accents,
// digits, and - between the words.
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		switch {
		case unicode.Is(unicode.Mn, r):
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		default:
			dash = true
		}
	}
	return b.String()
}
