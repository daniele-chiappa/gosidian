package mcp

import (
	"testing"

	"github.com/gosidian/gosidian/internal/parser"
)

// A markdown note created by an append gets a minimal frontmatter unless the
// text brings its own; other files are left as they are (IMP-119).
func TestWithMinimalFrontmatter(t *testing.T) {
	for _, c := range []struct{ rel, text, want string }{
		{"proj/log.md", "entry\n", "---\ntitle: \"log\"\ntags:\n  - \"proj\"\n---\n\nentry\n"},
		{"proj/sub/My: note.md", "x", "---\ntitle: \"My: note\"\ntags:\n  - \"proj\"\n---\n\nx"},
		{"root.md", "x", "---\ntitle: \"root\"\n---\n\nx"},
		{"proj/own.md", "---\ntitle: mine\n---\nbody", "---\ntitle: mine\n---\nbody"},
		{"proj/bom.md", "\ufeff\n---\ntitle: mine\n---\nbody", "---\ntitle: mine\n---\nbody"},
		{"proj/rule.md", "---\n## entry\n", "---\ntitle: \"rule\"\ntags:\n  - \"proj\"\n---\n\n---\n## entry\n"},
		{"proj/page.html", "<p>x</p>", "<p>x</p>"},
	} {
		if got := withMinimalFrontmatter(c.rel, c.text); got != c.want {
			t.Errorf("%s: got %q, want %q", c.rel, got, c.want)
		}
	}
	// A project name with YAML-significant characters stays one tag.
	got := withMinimalFrontmatter("a,b #c/log.md", "x")
	tags, _ := parser.ParseFrontmatterFields(parser.ExtractFrontmatterRaw([]byte(got)))["tags"].([]string)
	if len(tags) != 1 || tags[0] != "a,b #c" {
		t.Errorf("tags of %q = %q, want the project name as one tag", got, tags)
	}
}
