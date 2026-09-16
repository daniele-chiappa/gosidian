package mcp

import "strings"

// yamlQuote renders s as a safe single-line double-quoted YAML scalar, so a
// caller-supplied value with ":", quotes or newlines cannot break the
// frontmatter it is written into.
func yamlQuote(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", " ")
	return "\"" + s + "\""
}
