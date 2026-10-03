package views

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// Block is one ```view fence of a note: Start and End are the byte offsets
// of the whole fence (End just past its closing line), Spec its content.
type Block struct {
	Start, End int
	Spec       string
}

var fenceRe = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})\\s*([^`\\s]*)")

// FindBlocks returns the view blocks of body, in order. A ```view inside
// another fence (a code sample showing a view) is not a block.
func FindBlocks(body []byte) []Block {
	var out []Block
	var fence string // the open fence's marker, "" outside a fence
	var cur *Block
	off := 0
	for off < len(body) {
		end := len(body)
		if i := bytes.IndexByte(body[off:], '\n'); i >= 0 {
			end = off + i + 1
		}
		line := strings.TrimRight(string(body[off:end]), "\r\n")
		m := fenceRe.FindStringSubmatch(line)
		switch {
		case fence == "" && m != nil:
			fence = m[1]
			if m[2] == "view" {
				cur = &Block{Start: off}
			}
		case fence != "" && m != nil && m[2] == "" && strings.HasPrefix(m[1], fence[:1]) && len(m[1]) >= len(fence):
			if cur != nil {
				cur.End = end
				cur.Spec = string(body[cur.Start:off])
				cur.Spec = cur.Spec[strings.IndexByte(cur.Spec, '\n')+1:]
				out = append(out, *cur)
				cur = nil
			}
			fence = ""
		}
		off = end
	}
	return out
}

// Result markers: what Expand inserts after a block for a reader that keeps
// the spec (an agent). They let Expand drop a result that was copied into
// the file by mistake, instead of showing it twice.
const (
	resultOpen  = "<!-- gosidian:view-result — computed when the note is read; do not edit or copy it into the note -->"
	resultClose = "<!-- /gosidian:view-result -->"
)

var persistedRe = regexp.MustCompile(`(?s)\n?` + regexp.QuoteMeta(resultOpen) + `.*?` + regexp.QuoteMeta(resultClose) + `\n?`)

// Expand returns body with every view block rendered by render. With
// keepSpec the block stays and its result follows it between markers (the
// form for agents, who must see what produces a section); without, the
// block is replaced by its result inside <div class="gosidian-view"
// data-view="N">, N the block's position among the note's views (the form
// for the web UI, whose editors find their view by N). The second
// value hashes the results, so a caller can tell a changed view from an
// unchanged file.
func Expand(body []byte, keepSpec bool, render func(spec string) string) ([]byte, string) {
	body = persistedRe.ReplaceAll(body, []byte("\n"))
	blocks := FindBlocks(body)
	if len(blocks) == 0 {
		return body, ""
	}
	h := sha256.New()
	var out bytes.Buffer
	last := 0
	for i, b := range blocks {
		res := render(b.Spec)
		h.Write([]byte(res))
		if keepSpec {
			out.Write(body[last:b.End])
			out.WriteString(resultOpen + "\n\n" + res + "\n" + resultClose + "\n")
		} else {
			out.Write(body[last:b.Start])
			fmt.Fprintf(&out, "<div class=\"gosidian-view\" data-view=\"%d\">\n\n%s\n</div>\n", i, res)
		}
		last = b.End
	}
	out.Write(body[last:])
	return out.Bytes(), hex.EncodeToString(h.Sum(nil))[:16]
}

// ThisFields turns a note's parsed frontmatter (parser.ParseFrontmatterFields:
// strings and string lists) into the this.<field> values of its views, plus
// this.path (the vault path) and this.name (the file name without extension).
func ThisFields(notePath string, fm map[string]any) map[string][]string {
	out := map[string][]string{"path": {notePath}}
	base := notePath[strings.LastIndexByte(notePath, '/')+1:]
	if i := strings.LastIndexByte(base, '.'); i > 0 {
		base = base[:i]
	}
	out["name"] = []string{base}
	for k, v := range fm {
		switch t := v.(type) {
		case string:
			out[k] = []string{t}
		case []string:
			out[k] = t
		}
	}
	return out
}

// RenderNote expands the view blocks of a note (see Expand), running each
// with q in the context c. A view that fails to parse or run renders as a
// one-line warning instead of breaking the note.
func RenderNote(body []byte, keepSpec bool, c Context, q QueryFunc) ([]byte, string) {
	return Expand(body, keepSpec, func(spec string) string {
		r, err := compute(spec, c, q)
		if err != nil {
			return warning(err)
		}
		return r.Markdown()
	})
}

// RenderNoteData expands the view blocks of a note for the web UI, as
// RenderNote without the spec, and returns the views in the form the
// editors use, in the order of their data-view index.
func RenderNoteData(body []byte, c Context, q QueryFunc) ([]byte, []Data) {
	var data []Data
	out, _ := Expand(body, false, func(spec string) string {
		r, err := compute(spec, c, q)
		if err != nil {
			data = append(data, Data{Error: err.Error()})
			return warning(err)
		}
		data = append(data, r.Data(c))
		return r.Markdown()
	})
	return out, data
}

func warning(err error) string {
	return "> ⚠️ view: " + strings.ReplaceAll(err.Error(), "\n", " ") + "\n"
}
