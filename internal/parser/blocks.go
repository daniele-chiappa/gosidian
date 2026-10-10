package parser

import (
	"bytes"
	"regexp"
	"strings"
)

// FencedBlock is one fenced block of a note (```view, ```pipeline,
// ```diagram): Start and End are the byte offsets of the whole fence (End
// just past its closing line), Spec its content.
type FencedBlock struct {
	Start, End int
	Spec       string
}

var blockFenceRe = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})\\s*([^`\\s]*)")

// FindFenced returns the fenced blocks of body whose info string is lang,
// in order: one inside another fence (a code sample showing a block) is
// not a block. It lives here, not in views, so the index can find the
// ```diagram blocks (views imports index).
func FindFenced(body []byte, lang string) []FencedBlock {
	var out []FencedBlock
	var fence string // the open fence's marker, "" outside a fence
	var cur *FencedBlock
	off := 0
	for off < len(body) {
		end := len(body)
		if i := bytes.IndexByte(body[off:], '\n'); i >= 0 {
			end = off + i + 1
		}
		line := strings.TrimRight(string(body[off:end]), "\r\n")
		m := blockFenceRe.FindStringSubmatch(line)
		switch {
		case fence == "" && m != nil:
			fence = m[1]
			if m[2] == lang {
				cur = &FencedBlock{Start: off}
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
