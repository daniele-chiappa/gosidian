package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// A shipped skill lands in the global project's skills folder with the
// project's tag, once: an edited copy is never overwritten (IMP-210).
func TestSeedSkillsInto(t *testing.T) {
	efs := fstest.MapFS{
		"skills/a.md":      {Data: []byte("---\ntags: [{{GLOBAL}}, type:skill]\n---\n\nUse `{{ID}}` in a template.\n")},
		"skills/notes.txt": {Data: []byte("not a skill")},
	}
	dir := filepath.Join(t.TempDir(), "shared", "skills")
	got, err := SeedSkillsInto(dir, efs, "skills", "shared")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "a.md" {
		t.Fatalf("seeded %v", got)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "a.md"))
	if !strings.Contains(string(body), "tags: [shared, type:skill]") || !strings.Contains(string(body), "`{{ID}}`") {
		t.Errorf("body:\n%s", body)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err == nil {
		t.Error("a file that is no note was seeded")
	}
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := SeedSkillsInto(dir, efs, "skills", "shared"); err != nil || len(got) != 0 {
		t.Fatalf("second seed: %v %v", got, err)
	}
	if body, _ := os.ReadFile(filepath.Join(dir, "a.md")); string(body) != "edited" {
		t.Errorf("an edited skill was overwritten: %q", body)
	}
}
