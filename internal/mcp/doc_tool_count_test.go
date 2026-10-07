package mcp

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// The tool count written in the docs, the README and server.json follows the
// tools registered: it had drifted to 57 and 58 while 60 were registered.
func TestDocs_ToolCount(t *testing.T) {
	s, _, _ := newTestServer(t)
	n := s.ToolCount()
	root := filepath.Join("..", "..")
	files := map[string]*regexp.Regexp{
		"docs/mcp/tools.md":        regexp.MustCompile(`\*\*(\d+) tools\*\*`),
		"docs/mcp/overview.md":     regexp.MustCompile(`(?:exposes|all) (\d+) (?:typed )?tools`),
		"docs/mcp/client-setup.md": regexp.MustCompile(`should see (\d+) tools`),
		"docs/README.md":           regexp.MustCompile(`all (\d+) typed tools`),
		"README.md":                regexp.MustCompile(`(\d+) typed tools`),
		"server.json":              regexp.MustCompile(`a (\d+)-tool MCP server`),
	}
	for file, re := range files {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			t.Errorf("%s: %v", file, err)
			continue
		}
		ms := re.FindAllStringSubmatch(string(b), -1)
		if len(ms) == 0 {
			t.Errorf("%s: no tool count found", file)
		}
		for _, m := range ms {
			if got, _ := strconv.Atoi(m[1]); got != n {
				t.Errorf("%s says %d tools, %d are registered", file, got, n)
			}
		}
	}
}
