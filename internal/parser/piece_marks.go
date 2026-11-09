package parser

import (
	"regexp"
	"strconv"
	"strings"
)

// The pieces of a note (IMP-226): a comment on a line of its own numbers the
// piece of text that follows it, and "escluso" keeps the piece out of the
// note's design. A reader never sees them, and they are no words of the note.

var pieceMarkRe = regexp.MustCompile(`^ {0,3}<!--\s*([0-9]{1,6})(\s+escluso)?\s*-->\s*$`)

// PieceMark reads a line that numbers a piece: its number, from 1, and
// whether the piece stays out of the design.
func PieceMark(line string) (n int, excluded, ok bool) {
	m := pieceMarkRe.FindStringSubmatch(line)
	if m == nil {
		return 0, false, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n == 0 {
		return 0, false, false
	}
	return n, m[2] != "", true
}

// WithoutPieceMarks is s without the lines that number its pieces, outside
// code: the note as a normal markdown.
func WithoutPieceMarks(s string) string {
	if !strings.Contains(s, "<!--") {
		return s
	}
	var out strings.Builder
	out.Grow(len(s))
	var fences Fences
	for _, line := range strings.SplitAfter(s, "\n") {
		if !fences.Code(line) {
			if _, _, ok := PieceMark(line); ok {
				continue
			}
		}
		out.WriteString(line)
	}
	return out.String()
}
