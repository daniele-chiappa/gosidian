// Package frontmatter edits the YAML frontmatter of a markdown note one key at
// a time. It rewrites only the lines of the keys it sets or removes, so
// everything else (order, comments, quoting, blank lines, line endings) stays
// as written.
//
// The vault reads frontmatter twice: the index and most tools through the
// line-based reader of package parser, database schemas through yaml.v3
// (IMP-138). A value is written only in a form that both read back
// unchanged, and SetKeys checks it with both before touching the note.
package frontmatter

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/gosidian/gosidian/internal/parser"
)

// Field is a key to set and its new value: a string, a bool, a number, nil
// (an empty value) or a list of strings ([]string, or []any of strings, bools
// and numbers as JSON decodes it).
type Field struct {
	Key   string
	Value any
}

// ErrUnterminated reports a frontmatter block with no closing ---.
var ErrUnterminated = errors.New("frontmatter block is not closed")

// ComplexError reports a key whose current value is YAML that a one-line edit
// would damage: a nested map, a block scalar, a value spread over lines in
// another way, an anchor, or a key that appears twice.
type ComplexError struct{ Key string }

func (e *ComplexError) Error() string {
	return fmt.Sprintf("frontmatter key %q holds YAML that cannot be edited line by line", e.Key)
}

// ValueError reports a key or a value that cannot be written so that every
// reader reads it back unchanged.
type ValueError struct{ Key, Reason string }

func (e *ValueError) Error() string {
	return fmt.Sprintf("frontmatter key %q: %s", e.Key, e.Reason)
}

var (
	keyRe = regexp.MustCompile(`^[a-zA-Z_][\w-]*$`)
	// keyLineRe matches a top-level key the way the index reads it
	// (index.fieldKeyRe), so the line SetKeys replaces is the one the index
	// takes the value from.
	keyLineRe = regexp.MustCompile(`^([a-zA-Z_][\w-]*):[ \t]*(.*)$`)
	// dateRe is a date or datetime the vault writes unquoted by convention,
	// although yaml.v3 decodes it as a timestamp rather than a string.
	dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}([T ]\d{2}:\d{2}(:\d{2})?)?$`)
)

// SetKeys returns content with the keys of set written and the keys of unset
// removed. A key already present is replaced in place, a new one goes last in
// the frontmatter; a note without frontmatter gets one when set is not empty.
// Nothing is changed when any key fails: the error is a *ValueError, a
// *ComplexError or ErrUnterminated.
func SetKeys(content []byte, set []Field, unset []string) ([]byte, error) {
	type write struct{ key, line string }
	var writes []write
	seen := map[string]bool{}
	for _, f := range set {
		if !keyRe.MatchString(f.Key) {
			return nil, &ValueError{f.Key, "not a valid key"}
		}
		if seen[f.Key] {
			return nil, &ValueError{f.Key, "set twice"}
		}
		seen[f.Key] = true
		line, err := encodeLine(f.Key, f.Value)
		if err != nil {
			return nil, err
		}
		writes = append(writes, write{f.Key, line})
	}
	for _, k := range unset {
		if !keyRe.MatchString(k) {
			return nil, &ValueError{k, "not a valid key"}
		}
		if seen[k] {
			return nil, &ValueError{k, "both set and removed"}
		}
	}

	text := string(content)
	lines := strings.Split(text, "\n")
	if strings.TrimRight(lines[0], "\r") != "---" {
		if len(writes) == 0 {
			return content, nil
		}
		eol := "\n"
		if strings.Contains(text, "\r\n") {
			eol = "\r\n"
		}
		var b strings.Builder
		b.WriteString("---" + eol)
		for _, w := range writes {
			b.WriteString(w.line + eol)
		}
		b.WriteString("---" + eol)
		b.WriteString(text)
		return []byte(b.String()), nil
	}
	cr := ""
	if strings.HasSuffix(lines[0], "\r") {
		cr = "\r"
	}
	closing := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			closing = i
			break
		}
	}
	if closing < 0 {
		return nil, ErrUnterminated
	}

	byKey := map[string][]*entry{}
	for _, e := range parseEntries(lines, closing) {
		byKey[e.key] = append(byKey[e.key], e)
	}
	replace := map[int]string{}
	drop := map[int]bool{}
	var added []string
	for _, w := range writes {
		es := byKey[w.key]
		switch {
		case len(es) > 1:
			return nil, &ComplexError{w.key}
		case len(es) == 0:
			added = append(added, w.line+cr)
		default:
			e := es[0]
			if !e.editable(lines) {
				return nil, &ComplexError{w.key}
			}
			replace[e.start] = w.line + cr
			for i := e.start + 1; i < e.end; i++ {
				drop[i] = true
			}
		}
	}
	for _, k := range unset {
		es := byKey[k]
		if len(es) > 1 {
			return nil, &ComplexError{k}
		}
		if len(es) == 0 {
			continue
		}
		e := es[0]
		if e.orphans || strings.HasPrefix(e.inline, "&") {
			return nil, &ComplexError{k}
		}
		for i := e.start; i < e.end; i++ {
			drop[i] = true
		}
	}

	out := make([]string, 0, len(lines)+len(added))
	var raw []string
	for i, l := range lines {
		if i == closing {
			out = append(out, added...)
			raw = append(raw, added...)
		}
		if drop[i] {
			continue
		}
		if r, ok := replace[i]; ok {
			l = r
		}
		out = append(out, l)
		if i > 0 && i < closing {
			raw = append(raw, l)
		}
	}
	// The lines written are checked one by one; this catches an edit that
	// breaks a frontmatter that was valid YAML as a whole.
	if validYAML(strings.Join(lines[1:closing], "\n")) && !validYAML(strings.Join(raw, "\n")) {
		return nil, fmt.Errorf("frontmatter edit would leave invalid YAML")
	}
	return []byte(strings.Join(out, "\n")), nil
}

// entry is a top-level key of the frontmatter and the lines of its value.
type entry struct {
	key        string
	start, end int    // lines [start, end): the key line and its continuation
	inline     string // the value written on the key line, trimmed
	orphans    bool   // indented lines follow a blank line or a comment
}

// parseEntries lists the top-level keys between the opening line and
// closing. Indented lines and "- item" lines continue the key above them; a
// blank line or a comment ends it.
func parseEntries(lines []string, closing int) []*entry {
	var out []*entry
	var cur, last *entry
	for i := 1; i < closing; i++ {
		line := strings.TrimRight(lines[i], "\r")
		if m := keyLineRe.FindStringSubmatch(line); m != nil {
			cur = &entry{key: m[1], start: i, end: i + 1, inline: strings.TrimSpace(m[2])}
			last = cur
			out = append(out, cur)
			continue
		}
		continuation := strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "-")
		switch {
		case continuation && cur != nil:
			cur.end = i + 1
		case continuation && last != nil:
			// the value may go on past a blank line: not safe to rewrite
			last.orphans = true
		default:
			cur = nil
		}
	}
	return out
}

// editable reports whether the value of e can be replaced by a single line
// without damaging the YAML: a one-line value, nothing, or a block list of
// plain scalars.
func (e *entry) editable(lines []string) bool {
	if e.orphans {
		return false
	}
	cont := lines[e.start+1 : e.end]
	if e.inline != "" {
		if len(cont) > 0 {
			return false // a multi-line flow value or a block scalar
		}
		switch e.inline[0] {
		case '&', '|', '>':
			return false
		}
		return true
	}
	for _, l := range cont {
		t := strings.TrimSpace(strings.TrimRight(l, "\r"))
		if !strings.HasPrefix(t, "- ") {
			return false // a nested map, a comment or a nested list
		}
		item := strings.TrimSpace(t[2:])
		if item == "" || strings.ContainsAny(item[:1], "-[{|>&*!?") || strings.Contains(item, ": ") {
			return false
		}
	}
	return true
}

// encodeLine writes key and value as one frontmatter line and checks that
// both readers read the value back.
func encodeLine(key string, v any) (string, error) {
	var enc string
	var want any // what the readers must return: string, bool, float64, []string or nil
	switch x := v.(type) {
	case nil:
	case bool:
		enc, want = strconv.FormatBool(x), x
	case int:
		enc, want = strconv.Itoa(x), float64(x)
	case int64:
		enc, want = strconv.FormatInt(x, 10), float64(x)
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return "", &ValueError{key, "not a finite number"}
		}
		enc, want = strconv.FormatFloat(x, 'f', -1, 64), x
	case string:
		enc, want = scalar(x), x
	case []string:
		var err error
		if enc, err = list(key, x); err != nil {
			return "", err
		}
		want = x
	case []any:
		items := make([]string, len(x))
		for i, e := range x {
			switch y := e.(type) {
			case string:
				items[i] = y
			case bool:
				items[i] = strconv.FormatBool(y)
			case float64:
				items[i] = strconv.FormatFloat(y, 'f', -1, 64)
			default:
				return "", &ValueError{key, fmt.Sprintf("list items must be strings, not %T", e)}
			}
		}
		var err error
		if enc, err = list(key, items); err != nil {
			return "", err
		}
		want = items
	default:
		return "", &ValueError{key, fmt.Sprintf("unsupported value of type %T", v)}
	}
	line := key + ":"
	if enc != "" {
		line += " " + enc
	}
	if !readsBack(key, line, want) {
		return "", &ValueError{key, "the value cannot be written so that every reader reads it back"}
	}
	return line, nil
}

// scalar writes s plain when YAML reads it back as the same string (a date
// may stay plain, by vault convention), and double-quoted otherwise.
func scalar(s string) string {
	if s != "" && s == strings.TrimSpace(s) &&
		!strings.ContainsAny(s[:1], "-?:,[]{}#&*!|>'\"%@`") &&
		!strings.ContainsAny(s, "\n\r\t") &&
		!strings.Contains(s, ": ") && !strings.Contains(s, " #") && !strings.HasSuffix(s, ":") &&
		yamlReads("k: "+s, s) {
		return s
	}
	return quote(s)
}

// list writes items as an inline flow list. The line-based reader splits it
// on commas and trims quotes, spaces and a leading # from each item, so items
// it would read differently are refused.
func list(key string, items []string) (string, error) {
	parts := make([]string, len(items))
	for i, it := range items {
		switch {
		case it == "" || it != strings.TrimSpace(it):
			return "", &ValueError{key, "a list item cannot be empty or start or end with a space"}
		case strings.Contains(it, ","):
			return "", &ValueError{key, "a list item cannot contain a comma"}
		case strings.HasPrefix(it, "#"):
			return "", &ValueError{key, "a list item cannot start with #"}
		}
		if !strings.ContainsAny(it[:1], "-?:,[]{}#&*!|>'\"%@`") && !strings.ContainsAny(it, "[]{},\n\r\t") &&
			!strings.Contains(it, ": ") && !strings.Contains(it, " #") && !strings.HasSuffix(it, ":") &&
			yamlReads("k: ["+it+"]", []string{it}) {
			parts[i] = it
			continue
		}
		if strings.ContainsAny(it, "\"\\\n\r\t") {
			return "", &ValueError{key, "a list item cannot contain quotes, backslashes or line breaks"}
		}
		parts[i] = `"` + it + `"`
	}
	return "[" + strings.Join(parts, ", ") + "]", nil
}

// quote writes s as a YAML double-quoted scalar using only the escapes that
// strconv.Unquote, which the line-based reader applies, decodes the same way.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f || r == 0x85 || r == 0x2028 || r == 0x2029 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// readsBack reports whether both readers return want for key from line.
func readsBack(key, line string, want any) bool {
	return yamlReads(line, want) && lineReads(key, line, want)
}

// yamlReads reports whether yaml.v3 decodes the single key of doc to want.
func yamlReads(doc string, want any) bool {
	var m map[string]any
	if yaml.Unmarshal([]byte(doc), &m) != nil || len(m) != 1 {
		return false
	}
	var got any
	for _, v := range m {
		got = v
	}
	return sameValue(got, want)
}

func sameValue(got, want any) bool {
	switch w := want.(type) {
	case nil:
		return got == nil
	case string:
		switch g := got.(type) {
		case string:
			return g == w
		case time.Time:
			return dateRe.MatchString(w)
		}
		return false
	case bool:
		g, ok := got.(bool)
		return ok && g == w
	case float64:
		switch g := got.(type) {
		case int:
			return float64(g) == w
		case float64:
			return g == w
		}
		return false
	case []string:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return false
		}
		for i := range w {
			if !sameValue(g[i], w[i]) {
				return false
			}
		}
		return true
	}
	return false
}

// lineReads reports whether the line-based reader of package parser returns
// want for key from line, the way the index reads it.
func lineReads(key, line string, want any) bool {
	fields := parser.ParseFrontmatterFields(line)
	switch w := want.(type) {
	case nil:
		_, ok := fields[key]
		return !ok
	case []string:
		got := parser.FrontmatterList(line, key)
		if key == "tags" {
			got, _ = fields["tags"].([]string)
		}
		return len(got) == len(w) && (len(w) == 0 || reflect.DeepEqual(got, w))
	case string:
		return fields[key] == w
	case bool:
		return fields[key] == strconv.FormatBool(w)
	case float64:
		return fields[key] == strconv.FormatFloat(w, 'f', -1, 64)
	}
	return false
}

func validYAML(raw string) bool {
	var m map[string]any
	return yaml.Unmarshal([]byte(raw), &m) == nil
}
