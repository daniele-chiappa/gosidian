// Package initprompt renders the prompt-guide + gosidian_block payload
// returned by the memory_init_agent MCP tool.
//
// Design: the tool does NOT scan the caller's filesystem (the server has
// no view of the agent's cwd). Instead it produces a multi-section prompt
// that the agent itself executes, plus a parametric markdown block the
// agent innests into the agent-native instruction file (CLAUDE.md /
// AGENTS.md / .cursor/rules.mdc / …). Filename resolution is delegated to
// the agent; the tool only surfaces optional hints.
//
// Two modes:
//   - ModeAugment (preferred): the agent has already run its native /init
//     and passes the produced content via existing_content. The prompt
//     instructs a merge that preserves existing sections.
//   - ModeFromScratch (fallback): no existing content. The prompt includes
//     cwd-scan instructions so the agent can synthesize a new file.
package initprompt

// Profile identifies the target AI agent. It only influences prompt tone /
// tool references (Claude's AskUserQuestion vs. a generic "ask the user"
// fallback, for example); the gosidian_block is identical across profiles.
type Profile string

const (
	ProfileClaude  Profile = "claude"
	ProfileCursor  Profile = "cursor"
	ProfileCodex   Profile = "codex"
	ProfileAider   Profile = "aider"
	ProfileGeneric Profile = "generic"
)

type profileMeta struct {
	DisplayName       string
	PromptAugment     string
	PromptFromScratch string
	// AnchorTemplate is the embed.FS path of the agent-anchor template for
	// this profile, or "" if the profile has no native spawnable-subagent
	// concept (then agents stay vault-note-only, no local anchor).
	AnchorTemplate string
	// AnchorDir is the working-dir-relative directory where anchors are
	// materialised for this profile (e.g. ".claude/agents").
	AnchorDir string
}

// sharedStubTemplate is the thin instruction-file stub emitted by
// memory_init_agent: Regola Zero (call bootstrap, follow directives_block) +
// local-specifics placeholders. The full operational directives are NOT here —
// they are served by memory_bootstrap. Paths are relative to the embed.FS root
// (rooted at "assets").
const sharedStubTemplate = "assets/_common/stub.tmpl.md"

// sharedDirectivesTemplate is the generic operational directives block served
// by memory_bootstrap (directives_block field), parameterised only by
// {{PROJECT}} and {{DIRECTIVES_VERSION}}.
const sharedDirectivesTemplate = "assets/_common/directives.tmpl.md"

// StubVersion is the version of the instruction-file stub (stub.tmpl.md). It is
// substituted into the `<!-- gosidian:stub v=N -->` marker and surfaced by
// memory_bootstrap as `stub_version`, so an agent knows when its (rarely
// changing) stub must be regenerated via memory_init_agent. Bump when the stub
// contract changes. Guarded by TestStubVersion_PinnedToContent.
//
// v2 (2026-06-09, IMP-048): the in-stub "Specifiche locali" section is now an
// explicit signpost ("write local specifics BELOW the closing marker") instead
// of an editable placeholder — content inside the markers is regenerated on
// every bump, so inviting edits there was a footgun. Already-converted projects
// self-heal on their next bootstrap (stub_version advances → regeneration).
const StubVersion = 2

// DirectivesVersion is the version of the operational directives
// (directives.tmpl.md). It is substituted into the
// `<!-- gosidian:directives v=N -->` marker and surfaced by memory_bootstrap as
// `directives_version`. Since the directives are served fresh each session,
// projects never go stale on content — this version is informational + caching.
// Bump on every meaningful change. Guarded by TestDirectivesVersion_PinnedToContent.
//
// v2 (2026-06-09, IMP-049): directives now instruct pre-stub projects to
// self-convert their instruction file at bootstrap — breaks the first-conversion
// chicken-and-egg so existing projects heal without a manual rollout.
//
// v3 (2026-07-06, orchestrator-bus M2+M3+M5): handoff lifecycle — documents
// the pending→claimed→done|rejected states, the claim-before-work rule
// (memory_claim_handoff / memory_complete_handoff) and the server-stamped
// created_by/claimed_by/completed_by identity fields; adds type:handoff and
// the handoff status vocabulary to the closed tag list. Also documents the
// bootstrap token-economy knobs (known_directives_version / known_etags /
// mode=lite, batch_get outline/frontmatter modes, hot-oversize lint rule).
//
// v4 (2026-07-06, capability discovery): new «Formati di nota e allegati»
// section — markdown stays the declared default; .html native notes and the
// attachment upload tools (with the HTTP /upload endpoint, never base64 for
// large files) are now discoverable from the directives, cross-referencing
// the `capabilities` block that memory_bootstrap serves alongside; two new
// ingest-table rows route HTML artifacts and binary files accordingly.
//
// v5 (2026-07-06, ADR-016 table notes): «Formati di nota e allegati» gains
// the CSV table-note bullet (memory_create_table_note, capabilities.table_notes,
// cell values not indexed → caption required) and the ingest table routes
// long tabular data to a linked table note instead of the markdown body.
//
// v6 (2026-07-06, token-economy round 2): full prose tightening (-24%, same
// rules); the token-economy section documents the new memory_get oversize
// guard (bodies over 24 KiB come back truncated with outline + hint;
// raw:true bypasses) and the bootstrap auto-lite default for oversize hot.md.
//
// v7 (2026-07-07, anchors round 2): the token-economy section documents
// known_anchor_metas — the bootstrap anchors delta for anchor-enabled
// projects (canonical → meta_version; unchanged items ship without content).
//
// v8 (2026-07-11, ADR-018 one-door ingestion): «Formati di nota e allegati»
// now leads with memory_ingest as THE way to save a file (extension routing,
// sources cheapest-first incl. the bridge dir surfaced in
// capabilities.attachments.bridge_dir, the url source and the transfer:http
// single-use ticket); the ingest-rules table routes CSV/binary files through
// memory_ingest; the legacy upload tools are footnoted for explicit
// stage-then-attach flows.
//
// v9 (2026-07-12, ADR-019 maintenance digest): the end-of-task workflow gains
// step 0 — when the bootstrap serves maintenance.attention (hot.md oversize
// and/or broken wikilinks), propose the relevant grooming before closing;
// stale_count is context, not an obligation.
//
// v10 (2026-07-29, IMP-075 per-project tag vocabulary): the tag vocabulary
// section documents the open topic:<area> namespace and the per-project
// extension — domain taxonomies declared in memory/conventions.md
// frontmatter (tag_vocabulary: exact tags or ns:* wildcards, capped),
// active only under the project's use_tag_vocabulary flag, surfaced by the
// bootstrap tag_vocabulary block and honoured by memory_lint.
const DirectivesVersion = 10

// AnchorVersion is the version of the agent-anchor template/format. It is
// substituted into the `<!-- gosidian:anchor v=N ... -->` marker so the
// bootstrap reconciler knows when an anchor's shell (not its canonical role,
// which lives in the vault) must be re-rendered. Bump when the anchor template
// or marker contract changes.
const AnchorVersion = 1

// DefaultAnchorTools is the least-privilege tool preset granted to a spawned
// anchor subagent when the canonical note does not override it via a
// `harness.tools` block. memory_get is mandatory: it is how the anchor pulls
// its real role from the vault at step zero.
var DefaultAnchorTools = []string{"Read", "Edit", "Bash", "Grep", "Glob", "mcp__gosidian__memory_get"}

// SupportsAnchors reports whether the profile materialises agent anchors
// (i.e. the CLI has a native spawnable-subagent concept).
func SupportsAnchors(p Profile) bool {
	m, ok := profilesMap[p]
	return ok && m.AnchorTemplate != ""
}

// AnchorDir returns the working-dir-relative directory where the profile's
// agent anchors are materialised, or "" when the profile has no anchor support.
func AnchorDir(p Profile) string {
	return profilesMap[p].AnchorDir
}

var profilesMap = map[Profile]profileMeta{
	ProfileClaude: {
		DisplayName:       "Claude Code",
		PromptAugment:     "assets/claude/prompt_augment.md",
		PromptFromScratch: "assets/claude/prompt_fromscratch.md",
		AnchorTemplate:    "assets/claude/agent_anchor.tmpl.md",
		AnchorDir:         ".claude/agents",
	},
	ProfileCursor: {
		DisplayName:       "Cursor",
		PromptAugment:     "assets/cursor/prompt_augment.md",
		PromptFromScratch: "assets/cursor/prompt_fromscratch.md",
	},
	ProfileCodex: {
		DisplayName:       "OpenAI Codex",
		PromptAugment:     "assets/codex/prompt_augment.md",
		PromptFromScratch: "assets/codex/prompt_fromscratch.md",
	},
	ProfileAider: {
		DisplayName:       "Aider",
		PromptAugment:     "assets/aider/prompt_augment.md",
		PromptFromScratch: "assets/aider/prompt_fromscratch.md",
	},
	ProfileGeneric: {
		DisplayName:       "AI coding agent",
		PromptAugment:     "assets/generic/prompt_augment.md",
		PromptFromScratch: "assets/generic/prompt_fromscratch.md",
	},
}

// Profiles returns the list of registered profiles in a stable order.
// Useful for discovery / documentation.
func Profiles() []Profile {
	return []Profile{
		ProfileClaude, ProfileCursor, ProfileCodex, ProfileAider, ProfileGeneric,
	}
}

// IsKnownProfile reports whether p is registered.
func IsKnownProfile(p Profile) bool {
	_, ok := profilesMap[p]
	return ok
}
