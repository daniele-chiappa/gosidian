package mcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gosidian/gosidian/internal/projects"
)

// A skill that keeps reference notes in the folder named like it carries
// bundle {notes, bytes, oversize} in memory_skills and in the bootstrap's
// available_skills, global skills included; a skill without one carries
// nothing (IMP-115).
func TestSkillBundle_CatalogAndBootstrap(t *testing.T) {
	s, _, _ := newTestServer(t)
	pstore, err := projects.Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.SetProjects(pstore)
	s.SetGlobal(true, "global", "global-private")
	if err := pstore.Set("proj", projects.Flags{UseGlobals: true}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	mkSkill(t, s, ctx, "proj/skills/big.md", "Big", "proj")
	mkSkill(t, s, ctx, "proj/skills/plain.md", "Plain", "proj")
	mkSkill(t, s, ctx, "global/skills/shared.md", "Shared", "global")
	ref := func(path string, size int) {
		t.Helper()
		content := "---\ntitle: ref\ntype: doc\n---\n\n" + strings.Repeat("x", size)
		if res, err := s.handleCreate(ctx, call(map[string]any{"path": path, "content": content})); err != nil || res.IsError {
			t.Fatalf("create %s: %v %s", path, err, resultText(t, res))
		}
	}
	ref("proj/skills/big/references/small.md", 100)
	ref("proj/skills/big/references/huge.md", getBodySoftCap+10)
	ref("global/skills/shared/ref.md", 50)

	res, _ := s.handleSkills(ctx, call(map[string]any{"project": "proj"}))
	var listed struct {
		Skills []skillEntry `json:"skills"`
	}
	if err := json.Unmarshal([]byte(resultText(t, res)), &listed); err != nil {
		t.Fatal(err)
	}
	bundles := map[string]*skillBundle{}
	for _, sk := range listed.Skills {
		bundles[sk.Path] = sk.Bundle
	}
	if b := bundles["proj/skills/big.md"]; b == nil || b.Notes != 2 || b.Oversize != 1 || b.Bytes <= getBodySoftCap {
		t.Errorf("memory_skills big bundle = %+v, want 2 notes, 1 oversize, > %d bytes", b, getBodySoftCap)
	}
	if b := bundles["proj/skills/plain.md"]; b != nil {
		t.Errorf("a skill without a folder has no bundle, got %+v", b)
	}

	res, _ = s.handleBootstrap(ctx, call(map[string]any{"project": "proj"}))
	var boot struct {
		Skills []noteRef `json:"available_skills"`
	}
	if err := json.Unmarshal([]byte(resultText(t, res)), &boot); err != nil {
		t.Fatal(err)
	}
	got := map[string]*skillBundle{}
	for _, sk := range boot.Skills {
		got[sk.Path] = sk.Bundle
	}
	if b := got["proj/skills/big.md"]; b == nil || b.Notes != 2 || b.Oversize != 1 {
		t.Errorf("bootstrap big bundle = %+v", b)
	}
	if b := got["global/skills/shared.md"]; b == nil || b.Notes != 1 || b.Oversize != 0 {
		t.Errorf("bootstrap global shared bundle = %+v", b)
	}
	if b, ok := got["proj/skills/plain.md"]; !ok || b != nil {
		t.Errorf("bootstrap plain: listed=%v bundle=%+v, want listed without bundle", ok, b)
	}
}
