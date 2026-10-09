package parser

import "strings"

// Fences tells, line after line, which lines belong to a fenced code block,
// fence lines included, as CommonMark and the renderer pair them: a run of
// three or more backticks or tildes opens a block, and only a run of the
// same character, at least as long and with nothing after it, closes it.
// Toggled by any ``` line, a ```` block showing a ``` sample was cut in two
// at it, and the "## …" line after it was taken for a heading: sections and
// embeds ended inside the code (BUG-114, S4-9). The zero value is ready.
type Fences struct{ open string }

// Code reports whether line is a fence line or inside a fenced block.
func (f *Fences) Code(line string) bool {
	trim := strings.TrimSpace(line)
	run := fenceRun(trim)
	switch {
	case f.open == "":
		if run == "" {
			return false
		}
		f.open = run
	case run != "" && run[0] == f.open[0] && len(run) >= len(f.open) && strings.TrimSpace(trim[len(run):]) == "":
		f.open = ""
	}
	return true
}

// fenceRun is the run of backticks or tildes a trimmed line starts with,
// when it is three long or more; "" otherwise.
func fenceRun(s string) string {
	if len(s) < 3 || s[0] != '`' && s[0] != '~' {
		return ""
	}
	n := 1
	for n < len(s) && s[n] == s[0] {
		n++
	}
	if n < 3 {
		return ""
	}
	return s[:n]
}
