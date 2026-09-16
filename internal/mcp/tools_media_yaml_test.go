package mcp

import (
	"testing"

	"github.com/gosidian/gosidian/internal/parser"
)

// A caller-supplied title must never corrupt the note's frontmatter: colons,
// quotes and newlines have to round-trip through the frontmatter parser
// (BUG-039).
func TestMediaAndTableNotes_QuoteTitle(t *testing.T) {
	title := "Report: Q3 \"final\"\nmore"
	for name, note := range map[string]string{
		"media": buildMediaNote(title, "p/attachments/a.png", "p", "cap"),
		"table": buildTableNote(title, "p/attachments/t.csv", "p", "cap", []string{"a", "b"}, 1),
	} {
		fields := parser.ParseFrontmatterFields(parser.FrontmatterRawForPath("p/x.md", []byte(note)))
		got, _ := fields["title"].(string)
		if got != `Report: Q3 "final" more` {
			t.Errorf("%s: title round-trip = %q\n%s", name, got, note)
		}
		if ty, _ := fields["type"].(string); ty != name && !(name == "media" && ty == "image") {
			t.Errorf("%s: type field lost: %q\n%s", name, ty, note)
		}
	}
}
