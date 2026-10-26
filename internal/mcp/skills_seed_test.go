package mcp

import (
	"io/fs"
	"path"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/frontmatter"
	"github.com/gosidian/gosidian/internal/lint"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/scaffold"
)

// The skills gosidian ships for the global project are skill notes with a
// valid frontmatter once seeded, small enough for memory_get to serve whole
// and under the lint's skill-oversize limit (IMP-210).
func TestEmbeddedSkills_AreValidSkillNotes(t *testing.T) {
	entries, err := fs.ReadDir(EmbeddedSkillsFS(), EmbeddedSkillsRoot)
	if err != nil || len(entries) == 0 {
		t.Fatalf("no embedded skills: %v", err)
	}
	for _, e := range entries {
		body, err := fs.ReadFile(EmbeddedSkillsFS(), path.Join(EmbeddedSkillsRoot, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		note := strings.ReplaceAll(string(body), scaffold.GlobalPlaceholder, "global")
		p := "global/skills/" + e.Name()
		raw := parser.FrontmatterRawForPath(p, []byte(note))
		if err := frontmatter.YAMLError(raw); err != nil {
			t.Errorf("%s: frontmatter: %v", e.Name(), err)
		}
		fm := parser.ParseFrontmatterFields(raw)
		tags, _ := fm["tags"].([]string)
		if fm["type"] != "skill" || len(tags) < 2 || tags[0] != "global" || tags[1] != "type:skill" || fm["title"] == nil || fm["description"] == nil {
			t.Errorf("%s: frontmatter %v", e.Name(), fm)
		}
		if len(note) > getBodySoftCap || len(note) > lint.DefaultSkillOversizeBytes {
			t.Errorf("%s: %d bytes, over what memory_get serves whole or skill-oversize", e.Name(), len(note))
		}
	}
}
