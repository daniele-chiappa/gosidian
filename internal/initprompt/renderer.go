package initprompt

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

//go:embed all:assets/*
var assetsFS embed.FS

// Mode selects between the "augment an existing init file" flow and the
// "create from scratch" flow. ModeAugment is preferred when the caller
// already has the output of the agent's native /init.
type Mode string

const (
	ModeAugment     Mode = "augment"
	ModeFromScratch Mode = "from-scratch"
)

// Hints bundles optional per-call context. Fields that are non-empty are
// substituted into gosidian_block placeholders server-side, so the agent
// does not have to resolve them afterwards. Fields left empty keep their
// {{PLACEHOLDER}} form and the agent fills them after its Q&A.
type Hints struct {
	Language     string // used for {{LANGUAGE}} (vault notes language)
	CodeLanguage string // used for {{CODE_LANGUAGE}} (code / commit language)
	ProjectType  string // used for {{PROJECT_TYPE}} (app / CLI / library / infra / docs)
	Stack        string // used for {{STACK}} (framework / runtime summary)
	HotFiles     string // used for {{HOT_FILES}}
	FilenameHint string // used for {{FILENAME_HINT}} (e.g. "CLAUDE.md")
	CwdHint      string // used for {{CWD_HINT}} (agent's cwd, informative only)
	AgentName    string // used for {{AGENT_NAME}}; falls back to profile DisplayName
}

// Result is the value returned by Render and exposed as JSON by the MCP
// handler. `Prompt` is the multi-section instruction set the agent
// executes; `GosidianBlock` is the parametric markdown to innest into the
// agent-native instruction file.
type Result struct {
	Mode          Mode   `json:"mode"`
	NeedsScaffold bool   `json:"needs_scaffold"`
	Prompt        string `json:"prompt"`
	// GosidianBlock is the thin instruction-file stub to innest into the
	// agent-native file. The full operational directives live in
	// memory_bootstrap's directives_block, not here.
	GosidianBlock      string   `json:"gosidian_block"`
	StubVersion        int      `json:"stub_version"`
	SuggestedQuestions []string `json:"suggested_questions"`
}

// ErrUnknownProfile is returned when a caller requests a profile that is
// not registered in profilesMap.
var ErrUnknownProfile = errors.New("unknown agent profile")

// ErrUnknownMode is returned for an invalid mode value.
var ErrUnknownMode = errors.New("unknown mode")

// Render produces the init payload for (project, profile, mode).
// projectExists controls the NeedsScaffold flag — true when the project
// already exists in the vault, false when the agent should call
// memory_project_scaffold first.
func Render(project string, profile Profile, mode Mode, hints Hints, projectExists bool) (Result, error) {
	if strings.TrimSpace(project) == "" {
		return Result{}, errors.New("project is required")
	}
	if mode != ModeAugment && mode != ModeFromScratch {
		return Result{}, fmt.Errorf("%w: %q", ErrUnknownMode, mode)
	}
	prof, ok := profilesMap[profile]
	if !ok {
		return Result{}, fmt.Errorf("%w: %q", ErrUnknownProfile, profile)
	}

	promptAsset := prof.PromptAugment
	if mode == ModeFromScratch {
		promptAsset = prof.PromptFromScratch
	}
	promptBytes, err := assetsFS.ReadFile(promptAsset)
	if err != nil {
		return Result{}, fmt.Errorf("read prompt asset %s: %w", promptAsset, err)
	}
	stubBytes, err := assetsFS.ReadFile(sharedStubTemplate)
	if err != nil {
		return Result{}, fmt.Errorf("read stub template: %w", err)
	}

	vars := map[string]string{
		"PROJECT":       project,
		"TODAY":         time.Now().UTC().Format("2006-01-02"),
		"AGENT_PROFILE": string(profile),
		"AGENT_NAME":    coalesce(hints.AgentName, prof.DisplayName),
		"STUB_VERSION":  strconv.Itoa(StubVersion),
	}
	if hints.Language != "" {
		vars["LANGUAGE"] = hints.Language
	}
	if hints.CodeLanguage != "" {
		vars["CODE_LANGUAGE"] = hints.CodeLanguage
	}
	if hints.ProjectType != "" {
		vars["PROJECT_TYPE"] = hints.ProjectType
	}
	if hints.Stack != "" {
		vars["STACK"] = hints.Stack
	}
	if hints.HotFiles != "" {
		vars["HOT_FILES"] = hints.HotFiles
	}
	if hints.FilenameHint != "" {
		vars["FILENAME_HINT"] = hints.FilenameHint
	}
	if hints.CwdHint != "" {
		vars["CWD_HINT"] = hints.CwdHint
	}

	return Result{
		Mode:               mode,
		NeedsScaffold:      !projectExists,
		Prompt:             applyVars(string(promptBytes), vars),
		GosidianBlock:      applyVars(string(stubBytes), vars),
		StubVersion:        StubVersion,
		SuggestedQuestions: defaultSuggestedQuestions(),
	}, nil
}

// RenderDirectives renders the generic operational directives block for a
// project, parameterised only by {{PROJECT}} and {{DIRECTIVES_VERSION}}. It is
// served by memory_bootstrap (directives_block) so each project reads the rules
// fresh every session instead of embedding them in its instruction file.
// Returns the rendered markdown and the current DirectivesVersion.
func RenderDirectives(project string) (string, int, error) {
	if strings.TrimSpace(project) == "" {
		return "", 0, errors.New("project is required")
	}
	body, err := assetsFS.ReadFile(sharedDirectivesTemplate)
	if err != nil {
		return "", 0, fmt.Errorf("read directives template: %w", err)
	}
	vars := map[string]string{
		"PROJECT":            project,
		"DIRECTIVES_VERSION": strconv.Itoa(DirectivesVersion),
	}
	return applyVars(string(body), vars), DirectivesVersion, nil
}

// readDirectiveSections are the directive sections a token that cannot
// write acts on: where things are, which tags exist, how to read cheaply.
// The rest (stub conversion, ingest rules, note formats, plan placement,
// end-of-task workflow, handoff) is about writing.
var readDirectiveSections = []string{
	"### Mappa delle cartelle del vault",
	"### Vocabolario tag",
	"### Economia dei token",
}

// readDirectivesNote replaces the omitted sections in the read-only block.
const readDirectivesNote = "_Token di sola lettura: qui sotto solo le regole per orientarsi e leggere. Le regole di scrittura (ingest, formati, workflow di fine task, handoff) arrivano con un token che può scrivere nel progetto._"

// RenderReadDirectives renders the directives block for a token that cannot
// write to the project: the preamble plus readDirectiveSections, in template
// order, with the same version as the full block. It saves about two thirds
// of the block on sessions that could never apply the rest.
func RenderReadDirectives(project string) (string, int, error) {
	full, version, err := RenderDirectives(project)
	if err != nil {
		return "", 0, err
	}
	var out []string
	keep, inSection, noted := true, false, false
	for _, line := range strings.Split(full, "\n") {
		if strings.HasPrefix(line, "<!-- /gosidian:directives") {
			out = append(out, line)
			keep = true
			continue
		}
		if strings.HasPrefix(line, "### ") {
			if !noted {
				out = append(out, readDirectivesNote, "")
				noted = true
			}
			inSection = true
			keep = false
			for _, h := range readDirectiveSections {
				if strings.HasPrefix(line, h) {
					keep = true
					break
				}
			}
		}
		if keep || !inSection {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n"), version, nil
}

func applyVars(body string, vars map[string]string) string {
	for k, v := range vars {
		body = strings.ReplaceAll(body, "{{"+k+"}}", v)
	}
	return body
}

func coalesce(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func defaultSuggestedQuestions() []string {
	return []string{
		"In quale lingua preferisci le note del vault (italiano / inglese / …)?",
		"Che tipo di progetto è (applicazione web / CLI / libreria / infra / docs-only)?",
		"Quali sono i 2-3 file o cartelle più hot (che cambiano spesso)?",
		"Ci sono convenzioni di build / test / code style già stabilite da rispettare?",
	}
}

// AnchorInput carries the canonical metadata needed to render a local agent
// anchor for a vault `type:agent` note. The note body is NOT included: the
// anchor never duplicates the role, it pulls it from the vault at runtime.
type AnchorInput struct {
	CanonicalPath string   // vault path of the type:agent note, e.g. "plancia/agents/frontend-engineer.md"
	Slug          string   // anchor file basename (without .md); also the default Name
	Name          string   // optional override (harness.name); defaults to Slug
	Description   string   // routing hint (harness.description or the note description)
	Tools         []string // optional override (harness.tools); defaults to DefaultAnchorTools
	ToolsAll      bool     // harness.tools "all": omit the tools line so the CLI grants its full default toolset
	Model         string   // optional (harness.model)
	Materialize   bool     // harness.materialize; false keeps the role vault-only (no local anchor)
}

// AnchorResult is a rendered agent anchor: a thin local file plus the
// meta_version fingerprint of its anchor-relevant metadata (used by the
// bootstrap reconciler to detect when only the shell — not the canonical role,
// which lives in the vault — must be refreshed).
type AnchorResult struct {
	Path          string `json:"path"`
	Content       string `json:"content"`
	MetaVersion   string `json:"meta_version"`
	AnchorVersion int    `json:"anchor_version"`
}

// RenderAgentAnchor renders the local anchor file for one vault agent note,
// for a profile that supports anchors. It applies the locked defaults
// (name=slug, tools=DefaultAnchorTools, model optional) and computes the
// meta_version over the resolved metadata only — the body template is stable,
// so the version changes iff something that affects the anchor shell changes.
func RenderAgentAnchor(profile Profile, in AnchorInput) (AnchorResult, error) {
	prof, ok := profilesMap[profile]
	if !ok {
		return AnchorResult{}, fmt.Errorf("%w: %q", ErrUnknownProfile, profile)
	}
	if prof.AnchorTemplate == "" {
		return AnchorResult{}, fmt.Errorf("profile %q does not support agent anchors", profile)
	}
	if strings.TrimSpace(in.CanonicalPath) == "" || strings.TrimSpace(in.Slug) == "" {
		return AnchorResult{}, errors.New("anchor requires canonical path and slug")
	}
	name := coalesce(in.Name, in.Slug)
	tools := in.Tools
	if in.ToolsAll {
		tools = nil
	} else if len(tools) == 0 {
		tools = DefaultAnchorTools
	}
	desc := strings.TrimSpace(in.Description)
	model := strings.TrimSpace(in.Model)
	metaVersion := anchorMetaVersion(in.CanonicalPath, name, desc, tools, in.ToolsAll, model)

	body, err := assetsFS.ReadFile(prof.AnchorTemplate)
	if err != nil {
		return AnchorResult{}, fmt.Errorf("read anchor template: %w", err)
	}
	modelLine := ""
	if model != "" {
		modelLine = "model: " + model + "\n"
	}
	// ToolsAll omits the tools line entirely: the reference CLI treats a
	// missing tools key as "inherit the full default toolset".
	toolsLine := ""
	if !in.ToolsAll {
		toolsLine = "tools: " + strings.Join(tools, ", ") + "\n"
	}
	vars := map[string]string{
		"NAME":           name,
		"DESCRIPTION":    yamlInline(desc),
		"TOOLS_LINE":     toolsLine,
		"MODEL_LINE":     modelLine,
		"CANONICAL":      in.CanonicalPath,
		"META_VERSION":   metaVersion,
		"ANCHOR_VERSION": strconv.Itoa(AnchorVersion),
		"PROFILE":        string(profile),
	}
	path := strings.TrimRight(prof.AnchorDir, "/") + "/" + in.Slug + ".md"
	return AnchorResult{
		Path:          path,
		Content:       applyVars(string(body), vars),
		MetaVersion:   metaVersion,
		AnchorVersion: AnchorVersion,
	}, nil
}

// anchorMetaVersion is a short sha256 over the anchor-relevant metadata, in a
// fixed order with NUL separators so distinct field layouts cannot collide.
// The tools-all case hashes a NUL-prefixed sentinel no real tool list can
// produce; every other input keeps the pre-tools-all hash (mass-rewrite guard:
// TestRenderAgentAnchor_MetaVersionGolden).
func anchorMetaVersion(canonical, name, desc string, tools []string, toolsAll bool, model string) string {
	joined := strings.Join(tools, ",")
	if toolsAll {
		joined = "\x00all"
	}
	h := sha256.New()
	fmt.Fprintf(h, "canonical\x00%s\x00name\x00%s\x00description\x00%s\x00tools\x00%s\x00model\x00%s",
		canonical, name, desc, joined, model)
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// yamlInline renders s as a safe single-line double-quoted YAML scalar:
// newlines collapse to spaces and inner double-quotes become single-quotes.
func yamlInline(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, `"`, `'`)
	return `"` + strings.TrimSpace(s) + `"`
}
