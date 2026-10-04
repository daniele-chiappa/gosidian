package views

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// Values in the text (IMP-127 iteration 2, Q6): an inline code span
// `=count(<folders> where <condition> and …)` stands for the number of notes
// the conditions select, computed when the note is read like a view. The
// folders are those of a view's from (comma-separated); the conditions are a
// view's, in the "field op value" form, joined by `and`; without `where` it
// counts every note of the folders.
//
//	Open bugs: `=count(proj/docs/bugs where status in [open, in-progress])`
//
// The web UI shows the number; an agent gets the number followed by the
// expression, "3 (`=count(…)`)", so it sees what produces it.

// Value is an inline value of a note: Start and End are the byte offsets of
// its code span, backticks included; Expr is the text between the
// parentheses.
type Value struct {
	Start, End int
	Expr       string
}

var (
	valueRe = regexp.MustCompile("`=count\\(([^`]*)\\)`")
	// valueCopyRe is the form an agent reads, "3 (`=count(…)`)" or
	// "⚠️ count: <reason> (`=count(…)`)", copied back into a note by
	// mistake: it stands for the code span alone.
	valueCopyRe = regexp.MustCompile("(?:-?\\d+|⚠️ count: [^\\n]*?) \\((`=count\\([^`]*\\)`)\\)")
)

// FindValues returns the inline values of body, in order, outside fenced
// code blocks (a sample of the syntax in a code block stays as written).
func FindValues(body []byte) []Value {
	var out []Value
	var fence string
	off := 0
	for off < len(body) {
		end := len(body)
		if i := bytes.IndexByte(body[off:], '\n'); i >= 0 {
			end = off + i + 1
		}
		line := body[off:end]
		if m := fenceRe.FindStringSubmatch(strings.TrimRight(string(line), "\r\n")); m != nil {
			switch {
			case fence == "":
				fence = m[1]
			case m[2] == "" && strings.HasPrefix(m[1], fence[:1]) && len(m[1]) >= len(fence):
				fence = ""
			}
			off = end
			continue
		}
		if fence == "" {
			for _, sp := range codeSpans(line) {
				if expr, ok := countExpr(line[sp[0]:sp[1]]); ok {
					out = append(out, Value{Start: off + sp[0], End: off + sp[1], Expr: expr})
				}
			}
		}
		off = end
	}
	return out
}

// codeSpans returns the byte ranges of the inline code spans of a line,
// delimiters included, as CommonMark pairs them: a run of n backticks
// opens a span that the next run of exactly n backticks closes.
func codeSpans(line []byte) [][2]int {
	var out [][2]int
	run := func(i int) int {
		n := 0
		for i+n < len(line) && line[i+n] == '`' {
			n++
		}
		return n
	}
	for i := 0; i < len(line); {
		if line[i] != '`' {
			i++
			continue
		}
		n := run(i)
		closed := false
		for j := i + n; j < len(line); {
			if line[j] != '`' {
				j++
				continue
			}
			m := run(j)
			if m == n {
				out = append(out, [2]int{i, j + m})
				i, closed = j+m, true
				break
			}
			j += m
		}
		if !closed {
			i += n
		}
	}
	return out
}

// countExpr reads a code span as a value: one backtick on each side and
// =count(…) inside. A span of two backticks or more shows the syntax as
// text, as “ `=count(…)` “ does in prose.
func countExpr(span []byte) (string, bool) {
	if len(span) < 2 || span[1] == '`' {
		return "", false
	}
	if m := valueRe.FindSubmatch(span); m != nil && len(m[0]) == len(span) {
		return string(m[1]), true
	}
	return "", false
}

// ParseCount reads the expression of a `=count(…)` value as a count view.
func ParseCount(expr string, c Context) (*Spec, error) {
	from, conds, _ := strings.Cut(expr, " where ")
	s := &Spec{As: "count", Limit: 1}
	for _, f := range strings.Split(from, ",") {
		if f = strings.Trim(strings.TrimSpace(f), "/"); f != "" {
			s.From = append(s.From, f)
		}
	}
	if len(s.From) == 0 {
		return nil, errors.New("count of what: write =count(<folder> where <condition>)")
	}
	if strings.TrimSpace(conds) != "" {
		for _, part := range splitAnd(conds) {
			cond, err := parseCond(part, c)
			if err != nil {
				return nil, err
			}
			s.Where = append(s.Where, cond)
		}
	}
	return s, nil
}

// splitAnd splits conditions on " and ", outside brackets and quotes, so
// `title = "salt and pepper"` and `status in [a, b]` stay whole.
func splitAnd(s string) []string {
	var out []string
	depth, quote, start := 0, byte(0), 0
	for i := 0; i < len(s); i++ {
		switch ch := s[i]; {
		case quote != 0:
			if ch == quote {
				quote = 0
			}
		case ch == '"' || ch == '\'':
			quote = ch
		case ch == '[':
			depth++
		case ch == ']':
			if depth > 0 {
				depth--
			}
		case depth == 0 && strings.HasPrefix(s[i:], " and "):
			out = append(out, strings.TrimSpace(s[start:i]))
			start = i + len(" and ")
			i += len(" and ") - 1
		}
	}
	return append(out, strings.TrimSpace(s[start:]))
}

// ExpandValues returns body with every inline value computed: for an agent
// (keepSpec) "N (`=count(…)`)", for the web UI the number in a
// <span class="gosidian-count"> whose title is the expression. A value that
// does not compute shows the reason instead. The second value hashes the
// results ("" when body has no values).
func ExpandValues(body []byte, keepSpec bool, c Context, q QueryFunc) ([]byte, string) {
	body = valueCopyRe.ReplaceAll(body, []byte("$1"))
	vals := FindValues(body)
	if len(vals) == 0 {
		return body, ""
	}
	h := sha256.New()
	var out bytes.Buffer
	last := 0
	for _, v := range vals {
		span := string(body[v.Start:v.End])
		n, err := countValue(v.Expr, c, q)
		var res string
		switch {
		case err != nil && keepSpec:
			res = fmt.Sprintf("⚠️ count: %s (%s)", strings.ReplaceAll(err.Error(), "\n", " "), span)
		case err != nil:
			res = fmt.Sprintf(`<span class="gosidian-count gosidian-count-error" title="%s">⚠️ %s</span>`,
				html.EscapeString(err.Error()), html.EscapeString(span[1:len(span)-1]))
		case keepSpec:
			res = strconv.Itoa(n) + " (" + span + ")"
		default:
			res = fmt.Sprintf(`<span class="gosidian-count" title="%s">%d</span>`, html.EscapeString(span[1:len(span)-1]), n)
		}
		h.Write([]byte(res))
		out.Write(body[last:v.Start])
		out.WriteString(res)
		last = v.End
	}
	out.Write(body[last:])
	return out.Bytes(), hex.EncodeToString(h.Sum(nil))[:16]
}

func countValue(expr string, c Context, q QueryFunc) (int, error) {
	s, err := ParseCount(expr, c)
	if err != nil {
		return 0, err
	}
	r, err := Run(s, q)
	if err != nil {
		return 0, err
	}
	return r.Total, nil
}
