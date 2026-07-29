package initprompt

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestRender_AllProfilesAllModes(t *testing.T) {
	cases := []struct {
		name    string
		profile Profile
		mode    Mode
	}{
		{"claude augment", ProfileClaude, ModeAugment},
		{"claude fromscratch", ProfileClaude, ModeFromScratch},
		{"cursor augment", ProfileCursor, ModeAugment},
		{"cursor fromscratch", ProfileCursor, ModeFromScratch},
		{"codex augment", ProfileCodex, ModeAugment},
		{"codex fromscratch", ProfileCodex, ModeFromScratch},
		{"aider augment", ProfileAider, ModeAugment},
		{"aider fromscratch", ProfileAider, ModeFromScratch},
		{"generic augment", ProfileGeneric, ModeAugment},
		{"generic fromscratch", ProfileGeneric, ModeFromScratch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Render("myproj", tc.profile, tc.mode, Hints{}, false)
			if err != nil {
				t.Fatalf("Render failed: %v", err)
			}
			if res.Mode != tc.mode {
				t.Errorf("mode = %q, want %q", res.Mode, tc.mode)
			}
			if !res.NeedsScaffold {
				t.Error("NeedsScaffold should be true when projectExists=false")
			}
			if len(res.Prompt) < 500 {
				t.Errorf("Prompt too short: %d chars (want ≥ 500)", len(res.Prompt))
			}
			if len(res.GosidianBlock) < 1500 {
				t.Errorf("GosidianBlock too short: %d chars (want ≥ 1500)", len(res.GosidianBlock))
			}
			if len(res.SuggestedQuestions) == 0 {
				t.Error("SuggestedQuestions should not be empty")
			}
			// PROJECT and TODAY must always be resolved server-side.
			if strings.Contains(res.GosidianBlock, "{{PROJECT}}") {
				t.Error("GosidianBlock contains unresolved {{PROJECT}}")
			}
			if strings.Contains(res.GosidianBlock, "{{TODAY}}") {
				t.Error("GosidianBlock contains unresolved {{TODAY}}")
			}
			if !strings.Contains(res.GosidianBlock, "myproj") {
				t.Error("GosidianBlock should mention the project name")
			}
			today := time.Now().UTC().Format("2006-01-02")
			if !strings.Contains(res.GosidianBlock, today) {
				t.Errorf("GosidianBlock should mention today (%s)", today)
			}
		})
	}
}

func TestRender_AugmentPromptAnchors(t *testing.T) {
	res, err := Render("p", ProfileClaude, ModeAugment, Hints{}, true)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	wantAnchors := []string{
		"Determina filename",
		"Merge",
		"preservando tutto il contenuto esistente",
	}
	for _, a := range wantAnchors {
		if !strings.Contains(res.Prompt, a) {
			t.Errorf("augment prompt missing anchor %q", a)
		}
	}
	if strings.Contains(res.Prompt, "Scan cwd") {
		t.Error("augment prompt should not contain 'Scan cwd' anchor (that's from-scratch territory)")
	}
}

func TestRender_FromScratchPromptAnchors(t *testing.T) {
	res, err := Render("p", ProfileClaude, ModeFromScratch, Hints{}, true)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	wantAnchors := []string{
		"Scan cwd",
		"Compila",
	}
	for _, a := range wantAnchors {
		if !strings.Contains(res.Prompt, a) {
			t.Errorf("from-scratch prompt missing anchor %q", a)
		}
	}
}

func TestRender_ProjectExistsSkipsScaffold(t *testing.T) {
	res, err := Render("p", ProfileGeneric, ModeAugment, Hints{}, true)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	if res.NeedsScaffold {
		t.Error("NeedsScaffold should be false when projectExists=true")
	}
}

func TestRender_HintsResolveBlockPlaceholders(t *testing.T) {
	hints := Hints{
		Language:     "italiano",
		CodeLanguage: "inglese",
		ProjectType:  "applicazione web",
		Stack:        "Next.js + Prisma",
		HotFiles:     "src/app.tsx, prisma/schema.prisma",
	}
	res, err := Render("p", ProfileClaude, ModeAugment, hints, true)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	for _, ph := range []string{
		"{{LANGUAGE}}", "{{CODE_LANGUAGE}}", "{{PROJECT_TYPE}}",
		"{{STACK}}", "{{HOT_FILES}}",
	} {
		if strings.Contains(res.GosidianBlock, ph) {
			t.Errorf("block contains unresolved %s despite hint", ph)
		}
	}
	for _, want := range []string{"italiano", "inglese", "applicazione web", "Next.js + Prisma"} {
		if !strings.Contains(res.GosidianBlock, want) {
			t.Errorf("block missing hint value %q", want)
		}
	}
}

func TestRender_NoHintsKeepsPlaceholdersIntact(t *testing.T) {
	res, err := Render("p", ProfileClaude, ModeAugment, Hints{}, true)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	for _, ph := range []string{
		"{{LANGUAGE}}", "{{CODE_LANGUAGE}}", "{{PROJECT_TYPE}}",
		"{{STACK}}", "{{HOT_FILES}}",
	} {
		if !strings.Contains(res.GosidianBlock, ph) {
			t.Errorf("block missing placeholder %s (should remain unresolved without hint)", ph)
		}
	}
}

func TestRender_RejectsUnknownProfile(t *testing.T) {
	_, err := Render("p", Profile("bogus"), ModeAugment, Hints{}, true)
	if err == nil {
		t.Fatal("expected error for unknown profile")
	}
	if !strings.Contains(err.Error(), "unknown agent profile") {
		t.Errorf("error should mention unknown profile, got: %v", err)
	}
}

func TestRender_RejectsUnknownMode(t *testing.T) {
	_, err := Render("p", ProfileClaude, Mode("weird"), Hints{}, true)
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
}

func TestRender_RejectsEmptyProject(t *testing.T) {
	_, err := Render("  ", ProfileClaude, ModeAugment, Hints{}, true)
	if err == nil {
		t.Fatal("expected error for empty project")
	}
}

func TestRender_AgentNameFallsBackToProfileDisplayName(t *testing.T) {
	res, err := Render("p", ProfileClaude, ModeAugment, Hints{}, true)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	if !strings.Contains(res.GosidianBlock, "Claude Code") {
		t.Error("block should contain default AGENT_NAME = 'Claude Code' for claude profile")
	}
}

func TestRender_AgentNameHintOverridesDefault(t *testing.T) {
	hints := Hints{AgentName: "Custom IDE"}
	res, err := Render("p", ProfileGeneric, ModeAugment, hints, true)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	if !strings.Contains(res.GosidianBlock, "Custom IDE") {
		t.Error("block should honour AgentName hint")
	}
}

func TestProfiles_ReturnsAllRegistered(t *testing.T) {
	got := Profiles()
	if len(got) != 5 {
		t.Errorf("Profiles() returned %d entries, want 5", len(got))
	}
	for _, p := range got {
		if !IsKnownProfile(p) {
			t.Errorf("Profiles() returned unknown profile %q", p)
		}
	}
}

// TestRender_StubHasVersionMarker verifies the rendered stub carries the
// machine-readable version markers (start with the resolved StubVersion + end),
// that Result.StubVersion is set, and that the stub POINTS at the
// bootstrap-served directives rather than embedding them (ADR-010).
func TestRender_StubHasVersionMarker(t *testing.T) {
	res, err := Render("p", ProfileClaude, ModeAugment, Hints{}, true)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	if res.StubVersion != StubVersion {
		t.Errorf("Result.StubVersion = %d, want %d", res.StubVersion, StubVersion)
	}
	start := fmt.Sprintf("<!-- gosidian:stub v=%d -->", StubVersion)
	if !strings.Contains(res.GosidianBlock, start) {
		t.Errorf("stub missing start marker %q", start)
	}
	if !strings.Contains(res.GosidianBlock, "<!-- /gosidian:stub -->") {
		t.Error("stub missing end marker <!-- /gosidian:stub -->")
	}
	if strings.Contains(res.GosidianBlock, "{{STUB_VERSION}}") {
		t.Error("stub contains unresolved {{STUB_VERSION}}")
	}
	if !strings.Contains(res.GosidianBlock, "directives_block") {
		t.Error("stub should reference directives_block (directives are served by bootstrap, not embedded)")
	}
}

// TestRenderDirectives verifies the bootstrap-served directives render with the
// project name + version marker resolved and no dangling placeholders.
func TestRenderDirectives(t *testing.T) {
	body, ver, err := RenderDirectives("myproj")
	if err != nil {
		t.Fatalf("RenderDirectives failed: %v", err)
	}
	if ver != DirectivesVersion {
		t.Errorf("version = %d, want %d", ver, DirectivesVersion)
	}
	start := fmt.Sprintf("<!-- gosidian:directives v=%d -->", DirectivesVersion)
	if !strings.Contains(body, start) {
		t.Errorf("directives missing start marker %q", start)
	}
	if !strings.Contains(body, "<!-- /gosidian:directives -->") {
		t.Error("directives missing end marker")
	}
	if !strings.Contains(body, "myproj") {
		t.Error("directives should mention the project name")
	}
	for _, ph := range []string{"{{PROJECT}}", "{{DIRECTIVES_VERSION}}"} {
		if strings.Contains(body, ph) {
			t.Errorf("directives contains unresolved %s", ph)
		}
	}
	if _, _, err := RenderDirectives("  "); err == nil {
		t.Error("expected error for empty project")
	}
}

// TestStubVersion_PinnedToContent and TestDirectivesVersion_PinnedToContent are
// discipline guards: each fails whenever the embedded template changes without
// the maintainer revisiting the matching version. When one fires: bump the
// version in profiles.go (if agents should pick the change up) AND update the
// pin, or just update the pin for a cosmetic edit.
func TestStubVersion_PinnedToContent(t *testing.T) {
	assertPinned(t, sharedStubTemplate, StubVersion, 2,
		"78a4e8c3cd21d1eba604b675492b2462e11a9ae54785be2be61927d4160bbdb0")
}

func TestDirectivesVersion_PinnedToContent(t *testing.T) {
	assertPinned(t, sharedDirectivesTemplate, DirectivesVersion, 10,
		"93cef9879ee8ab3abd6067926d7fc4711a0288a2208489d629ada3878ec5b74a")
}

func assertPinned(t *testing.T, asset string, gotVersion, wantVersion int, wantHash string) {
	t.Helper()
	raw, err := assetsFS.ReadFile(asset)
	if err != nil {
		t.Fatalf("read %s: %v", asset, err)
	}
	sum := sha256.Sum256(raw)
	got := hex.EncodeToString(sum[:])
	if gotVersion != wantVersion {
		t.Fatalf("%s: version = %d, pin expects %d — update both together", asset, gotVersion, wantVersion)
	}
	if got != wantHash {
		t.Fatalf("%s content changed (sha256 %s, pin %s).\n"+
			"If agents should pick this up: bump the version in profiles.go, then set wantVersion+wantHash.\n"+
			"If cosmetic: just set wantHash.", asset, got, wantHash)
	}
}
