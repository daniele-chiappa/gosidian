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

// The prompts name the hint placeholders to say which ones the agent still
// has to fill: they are never replaced by values there, and the sentence on
// the open ones follows the hints actually given.
func TestRender_PromptNamesOpenPlaceholders(t *testing.T) {
	hints := Hints{Language: "italiano", Stack: "Go", HotFiles: "src/a.go\nsrc/b.go"}
	for _, p := range Profiles() {
		for _, m := range []Mode{ModeAugment, ModeFromScratch} {
			res, err := Render("p", p, m, hints, true)
			if err != nil {
				t.Fatalf("%s/%s: %v", p, m, err)
			}
			if strings.Contains(res.Prompt, "`italiano`") || strings.Contains(res.Prompt, "src/a.go") {
				t.Errorf("%s/%s: prompt holds a hint value where it names a placeholder", p, m)
			}
			if !strings.Contains(res.Prompt, "Placeholder ancora da risolvere nel blocco:") {
				t.Errorf("%s/%s: prompt does not say which placeholders are still open", p, m)
			}
			// Replacing an existing stub must reach the real end marker:
			// up to v2 the stub quoted it in its own text.
			if m == ModeAugment && !strings.Contains(res.Prompt, "riga che contiene **soltanto**") {
				t.Errorf("%s/%s: prompt lacks the stub placement rule", p, m)
			}
			if strings.Contains(res.Prompt, "## Memory & workflow (gosidian)`") {
				t.Errorf("%s/%s: prompt still says the block starts at its heading", p, m)
			}
		}
	}
	res, _ := Render("p", ProfileClaude, ModeAugment, hints, true)
	if want := "Placeholder ancora da risolvere nel blocco: `{{CODE_LANGUAGE}}`, `{{PROJECT_TYPE}}`."; !strings.Contains(res.Prompt, want) {
		t.Errorf("prompt lacks %q", want)
	}
	all := Hints{Language: "it", CodeLanguage: "en", ProjectType: "cli", Stack: "Go", HotFiles: "main.go"}
	if res, _ := Render("p", ProfileClaude, ModeAugment, all, true); !strings.Contains(res.Prompt, "Tutti i placeholder del blocco sono già risolti") {
		t.Error("prompt should say every placeholder is filled")
	}
	// The block's own comment lists no placeholders: filled in, a multi-line
	// value used to split inside it.
	if strings.Contains(res.GosidianBlock, "Placeholder:") {
		t.Error("stub comment still lists the placeholders")
	}
}

// The Claude prompts list only the placeholders still open, and the
// suggested questions leave out what user_hints answered: an agent following
// them to the letter asked again for values it was given (BUG-074).
func TestRender_AsksOnlyForOpenPlaceholders(t *testing.T) {
	bullet := func(name string) string { return "- `{{" + name + "}}` — " }
	some := Hints{Language: "italiano", Stack: "Go", HotFiles: "main.go"}
	all := Hints{Language: "it", CodeLanguage: "en", ProjectType: "cli", Stack: "Go", HotFiles: "main.go"}
	for _, m := range []Mode{ModeAugment, ModeFromScratch} {
		res, _ := Render("p", ProfileClaude, m, some, true)
		for _, name := range []string{"LANGUAGE", "STACK", "HOT_FILES"} {
			if strings.Contains(res.Prompt, bullet(name)) {
				t.Errorf("%s: prompt still asks for {{%s}}, given in user_hints", m, name)
			}
		}
		for _, name := range []string{"CODE_LANGUAGE", "PROJECT_TYPE"} {
			if !strings.Contains(res.Prompt, bullet(name)) {
				t.Errorf("%s: prompt does not ask for the open {{%s}}", m, name)
			}
		}
		if got := len(res.SuggestedQuestions); got != 2 {
			t.Errorf("%s: %d suggested questions with language and hot files given, want 2 (type, conventions): %q",
				m, got, res.SuggestedQuestions)
		}

		res, _ = Render("p", ProfileClaude, m, all, true)
		for _, name := range hintPlaceholders {
			if strings.Contains(res.Prompt, bullet(name)) {
				t.Errorf("%s: every hint given, prompt still asks for {{%s}}", m, name)
			}
		}
		if !strings.Contains(res.Prompt, "Salta questo step") {
			t.Errorf("%s: every hint given, the placeholder step is not skipped", m)
		}
		if len(res.SuggestedQuestions) != 1 || !strings.Contains(res.SuggestedQuestions[0], "convenzioni") {
			t.Errorf("%s: every hint given, suggested questions = %q", m, res.SuggestedQuestions)
		}
	}
	if res, _ := Render("p", ProfileClaude, ModeAugment, Hints{}, true); len(res.SuggestedQuestions) != 4 {
		t.Errorf("no hints: %d suggested questions, want 4", len(res.SuggestedQuestions))
	}
}

// One pass: a value is never scanned for placeholders again.
func TestApplyVars_SinglePass(t *testing.T) {
	got := applyVars("{{A}} {{B}} {{C}} {{...}}", map[string]string{"A": "{{B}}", "B": "b"})
	if want := "{{B}} b {{C}} {{...}}"; got != want {
		t.Errorf("applyVars = %q, want %q", got, want)
	}
}

func TestProfileForFilename(t *testing.T) {
	for in, want := range map[string]Profile{
		"CLAUDE.md":                  ProfileClaude,
		"/home/u/repo/claude.md":     ProfileClaude,
		".cursorrules":               ProfileCursor,
		".cursor/rules/gosidian.mdc": ProfileCursor,
		`repo\.cursor\rules.mdc`:     ProfileCursor,
		"CONVENTIONS.md":             ProfileAider,
		".aider.conf.yml":            ProfileAider,
		"AGENTS.md":                  "",
		"":                           "",
	} {
		got, ok := ProfileForFilename(in)
		if got != want || ok != (want != "") {
			t.Errorf("ProfileForFilename(%q) = %q, %v; want %q", in, got, ok, want)
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
	// The markers are the only place they appear, and no comment holds
	// another: a quoted marker inside the leading comment closed it early
	// (BUG-067), and a search for the end marker found a quote first.
	for _, m := range []string{start, "<!-- /gosidian:stub -->"} {
		if n := strings.Count(res.GosidianBlock, m); n != 1 {
			t.Errorf("stub has %d occurrences of %q, want 1", n, m)
		}
	}
	for rest := res.GosidianBlock; ; {
		open := strings.Index(rest, "<!--")
		if open < 0 {
			break
		}
		end := strings.Index(rest[open+4:], "-->")
		if end < 0 {
			t.Fatal("stub has an unterminated HTML comment")
		}
		if inner := rest[open+4 : open+4+end]; strings.Contains(inner, "<!--") {
			t.Errorf("HTML comment opens another inside it: %q", inner)
		}
		rest = rest[open+4+end+3:]
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

// The template's own comment describes it without naming its placeholders:
// they were filled in there too, into "parametrico solo su myproj e 13"
// (BUG-073).
func TestRenderDirectives_CommentNamesNoPlaceholder(t *testing.T) {
	raw, err := assetsFS.ReadFile(sharedDirectivesTemplate)
	if err != nil {
		t.Fatal(err)
	}
	_, afterMarker, _ := strings.Cut(string(raw), "-->")
	comment, _, ok := strings.Cut(afterMarker, "-->")
	if !ok || !strings.HasPrefix(strings.TrimSpace(comment), "<!--") {
		t.Fatal("directives template lost its leading comment")
	}
	if ph := placeholderRE.FindString(comment); ph != "" {
		t.Errorf("leading comment names %s, which is filled in when the block renders", ph)
	}
}

// TestStubVersion_PinnedToContent and TestDirectivesVersion_PinnedToContent are
// discipline guards: each fails whenever the embedded template changes without
// the maintainer revisiting the matching version. When one fires: bump the
// version in profiles.go (if agents should pick the change up) AND update the
// pin, or just update the pin for a cosmetic edit.
func TestStubVersion_PinnedToContent(t *testing.T) {
	assertPinned(t, sharedStubTemplate, StubVersion, 3,
		"044e7ab69778b17760ef3f40d959cc3c876955279601124d529114fcdb732fb9")
}

func TestDirectivesVersion_PinnedToContent(t *testing.T) {
	assertPinned(t, sharedDirectivesTemplate, DirectivesVersion, 24,
		"06886183f4f1b1aa61a780318b699a7339ce2a9b8954f3da17a6b02022e197bd")
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
			"If an agent would act differently on the new text (names, steps, rules): bump the version in profiles.go, then set wantVersion+wantHash.\n"+
			"Only if it would act the same (whitespace, a typo in prose): just set wantHash.", asset, got, wantHash)
	}
}

func TestRenderReadDirectives_KeepsReadingSections(t *testing.T) {
	full, fv, err := RenderDirectives("proj")
	if err != nil {
		t.Fatal(err)
	}
	read, rv, err := RenderReadDirectives("proj")
	if err != nil {
		t.Fatal(err)
	}
	if rv != fv {
		t.Errorf("read version %d != full version %d", rv, fv)
	}
	if len(read) >= len(full)/2 {
		t.Errorf("read block is %d bytes of %d: expected well under half", len(read), len(full))
	}
	for _, h := range readDirectiveSections {
		if !strings.Contains(read, h) {
			t.Errorf("read block misses section %q", h)
		}
	}
	if !strings.HasPrefix(read, "<!-- gosidian:directives v=") || !strings.Contains(read, "<!-- /gosidian:directives -->") {
		t.Error("read block lost its markers")
	}
	if strings.Contains(read, "### Quando scrivere in memoria") {
		t.Error("read block still carries the ingest rules")
	}
}
