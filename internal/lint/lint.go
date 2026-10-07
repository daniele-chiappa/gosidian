// Package lint — structural health checks for the gosidian vault.
//
// The agent-first paradigm (ADR-007) depends on disciplined metadata: when
// wikilinks resolve, frontmatter is well-formed, and status marker tags stay
// coherent with the session cache, structured retrieval gives reliable
// context. When discipline drifts, retrieval decays silently.
//
// memory_lint is the self-check agents run to catch that drift before it
// propagates. Think `go vet` for a vault: zero error-severity issues on a
// healthy project, warnings as guidance, info as nudges.
//
// Baseline rules (v1.9):
//
//   - broken-wikilink (warning) — [[target]] that doesn't resolve
//   - broken-anchor (warning)   — [[note#Heading]] whose heading is not there
//   - orphan-note (info)        — note with no in/out links
//   - frontmatter-missing (err) — note without YAML frontmatter
//   - frontmatter-invalid-yaml (warning) — frontmatter that is not valid YAML
//   - frontmatter-tag-unknown (warning) — tag outside the closed vocabulary
//   - status-incoherent (warning) — plan in-progress but absent from hot.md
//
// Rules are pure functions over an index snapshot and the vault; adding a
// new rule is three lines in DefaultRules.
package lint

import (
	"context"
	"fmt"
	"strings"

	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/vault"
)

// Severity classifies an Issue. Callers can filter by minimum severity.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// severityRank maps severity → ordinal for filtering. Higher = more severe.
func severityRank(s Severity) int {
	switch s {
	case SeverityError:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	}
	return 0
}

// Issue is one finding emitted by a rule.
type Issue struct {
	Severity Severity `json:"severity"`
	File     string   `json:"file"`
	Line     int      `json:"line,omitempty"` // 1-based; 0 when not applicable
	Rule     string   `json:"rule"`
	Message  string   `json:"message"`
	FixHint  string   `json:"fix_hint,omitempty"`
}

// ruleFunc is the internal rule implementation signature. Receives a Linter
// pointer for access to vault + index and returns issues plus a hard error
// (failed I/O) that aborts the whole run.
type ruleFunc func(ctx context.Context, l *Linter, project string) ([]Issue, error)

// ruleSpec wraps a rule's identity and implementation.
type ruleSpec struct {
	name            string
	defaultSeverity Severity
	fn              ruleFunc
}

// Linter holds the vault + index bindings used by rules.
type Linter struct {
	vault *vault.Vault
	index *index.Index
	// extraAllowedTags expands the closed vocabulary checked by
	// frontmatter-tag-unknown. nil/empty means "use built-in only".
	// Populated via WithExtraAllowedTags from a vault's
	// .gosidian/config.toml [lint.frontmatter_tag_vocabulary].
	extraAllowedTags map[string]struct{}
	// extraAllowedNamespaces holds the "ns:*" wildcard entries from the same
	// sources: any well-formed value in one of these namespaces is accepted.
	extraAllowedNamespaces map[string]struct{}
	// projectVocabEnabled activates the per-project vocabulary declared in
	// <project>/memory/conventions.md frontmatter (`tag_vocabulary:`).
	// Wired from the project's use_tag_vocabulary flag (IMP-075) — when
	// false the declaration is inert and the rule behaves as before.
	projectVocabEnabled bool
	// hotOversizeBytes is the hot-oversize threshold; <= 0 means
	// DefaultHotOversizeBytes. Populated via WithHotOversizeLimit from
	// [lint] hot_oversize_bytes.
	hotOversizeBytes int64
	// skillOversizeBytes is the skill-oversize threshold; <= 0 means
	// DefaultSkillOversizeBytes. The MCP server passes memory_get's cap.
	skillOversizeBytes int
}

// New wires a Linter against a live vault + index.
func New(v *vault.Vault, idx *index.Index) *Linter {
	return &Linter{vault: v, index: idx}
}

// WithHotOversizeLimit overrides the hot-oversize threshold in bytes. Values
// <= 0 keep DefaultHotOversizeBytes. Returns the receiver for chaining.
func (l *Linter) WithHotOversizeLimit(bytes int64) *Linter {
	l.hotOversizeBytes = bytes
	return l
}

// WithSkillOversizeLimit sets the skill-oversize threshold in bytes, the size
// above which memory_get truncates a note. Values <= 0 keep
// DefaultSkillOversizeBytes. Returns the receiver for chaining.
func (l *Linter) WithSkillOversizeLimit(bytes int) *Linter {
	l.skillOversizeBytes = bytes
	return l
}

// WithExtraAllowedTags adds tags to the closed vocabulary the
// frontmatter-tag-unknown rule accepts. Format of each entry is the same
// as a frontmatter tag: "<namespace>:<value>" (e.g. "status:reference"),
// a bare tag name, or a namespace wildcard "<namespace>:*" (e.g. "cm:*")
// accepting any well-formed value in that namespace. Malformed entries
// (empty namespace, empty value) are skipped silently — a typo in the
// config should not crash the lint.
//
// Returns the receiver for chaining (`lint.New(v, idx).WithExtraAllowedTags(extras)`).
func (l *Linter) WithExtraAllowedTags(extra []string) *Linter {
	if len(extra) == 0 {
		return l
	}
	if l.extraAllowedTags == nil {
		l.extraAllowedTags = make(map[string]struct{}, len(extra))
	}
	if l.extraAllowedNamespaces == nil {
		l.extraAllowedNamespaces = make(map[string]struct{})
	}
	for _, t := range extra {
		t = trimTag(t)
		if !validExtraTag(t) {
			continue
		}
		if ns, ok := wildcardNamespace(t); ok {
			l.extraAllowedNamespaces[ns] = struct{}{}
			continue
		}
		l.extraAllowedTags[t] = struct{}{}
	}
	return l
}

// WithProjectTagVocabulary toggles the per-project extra vocabulary
// declared in <project>/memory/conventions.md frontmatter
// (`tag_vocabulary:`, exact tags or "ns:*" wildcards, capped at
// MaxProjectVocabEntries). Callers wire this from the project's
// use_tag_vocabulary flag (IMP-075); default off keeps the declaration
// inert. Returns the receiver for chaining.
func (l *Linter) WithProjectTagVocabulary(enabled bool) *Linter {
	l.projectVocabEnabled = enabled
	return l
}

// DefaultRules returns the baseline v1.9 rules in a stable order.
func DefaultRules() []string {
	out := make([]string, 0, len(allRules))
	for _, r := range allRules {
		out = append(out, r.name)
	}
	return out
}

// Run executes the selected rules against project. When enabled is empty,
// all default rules run. minSeverity filters the returned issues (empty =
// no filtering).
func (l *Linter) Run(ctx context.Context, project string, enabled []string, minSeverity Severity) ([]Issue, error) {
	if project == "" {
		return nil, fmt.Errorf("project is required")
	}
	rules, err := selectRules(enabled)
	if err != nil {
		return nil, err
	}
	var all []Issue
	for _, r := range rules {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		issues, err := r.fn(ctx, l, project)
		if err != nil {
			return nil, fmt.Errorf("rule %s: %w", r.name, err)
		}
		all = append(all, issues...)
	}
	all = l.dropDisabled(all)
	if minSeverity != "" {
		all = filterBySeverity(all, minSeverity)
	}
	return all, nil
}

// dropDisabled removes the issues of a note that turns their rule off with
// `lint_disable` in its frontmatter (a list, or names separated by commas):
// a note may opt out of a rule by hand (IMP-121, IMP-024). Each note with
// issues is read once; one that cannot be read keeps its issues.
func (l *Linter) dropDisabled(issues []Issue) []Issue {
	disabled := map[string]map[string]bool{}
	out := issues[:0]
	for _, i := range issues {
		off, ok := disabled[i.File]
		if !ok {
			off = map[string]bool{}
			if note, err := l.vault.Load(i.File); err == nil {
				for _, r := range parser.FrontmatterList(parser.FrontmatterRawForPath(i.File, note.Content), "lint_disable") {
					off[strings.TrimSpace(r)] = true
				}
			}
			disabled[i.File] = off
		}
		if !off[i.Rule] {
			out = append(out, i)
		}
	}
	return out
}

// selectRules resolves the enabled list into ruleSpec instances in stable
// registry order. An empty list means "all default rules" (allRules only —
// optional rules must be requested by name). Named rules resolve against the
// full registry (default + optional). Unknown rule names return an error.
func selectRules(enabled []string) ([]ruleSpec, error) {
	if len(enabled) == 0 {
		return allRules, nil
	}
	want := make(map[string]struct{}, len(enabled))
	for _, n := range enabled {
		want[n] = struct{}{}
	}
	out := make([]ruleSpec, 0, len(enabled))
	for _, r := range knownRules() {
		if _, ok := want[r.name]; ok {
			out = append(out, r)
			delete(want, r.name)
		}
	}
	if len(want) > 0 {
		unknown := make([]string, 0, len(want))
		for n := range want {
			unknown = append(unknown, n)
		}
		return nil, fmt.Errorf("unknown rule(s): %v", unknown)
	}
	return out, nil
}

// filterBySeverity keeps only issues at or above minSeverity.
func filterBySeverity(issues []Issue, min Severity) []Issue {
	minRank := severityRank(min)
	if minRank == 0 {
		return issues
	}
	out := make([]Issue, 0, len(issues))
	for _, i := range issues {
		if severityRank(i.Severity) >= minRank {
			out = append(out, i)
		}
	}
	return out
}

// Summary aggregates counts by severity and by rule for quick telemetry.
type Summary struct {
	BySeverity map[string]int `json:"by_severity"`
	ByRule     map[string]int `json:"by_rule"`
	Total      int            `json:"total"`
}

// Summarise computes a Summary for a slice of issues.
func Summarise(issues []Issue) Summary {
	s := Summary{
		BySeverity: map[string]int{},
		ByRule:     map[string]int{},
		Total:      len(issues),
	}
	for _, i := range issues {
		s.BySeverity[string(i.Severity)]++
		s.ByRule[i.Rule]++
	}
	return s
}
