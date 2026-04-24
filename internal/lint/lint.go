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
//   - orphan-note (info)        — note with no in/out links
//   - frontmatter-missing (err) — note without YAML frontmatter
//   - frontmatter-tag-unknown (warning) — tag outside the closed vocabulary
//   - status-incoherent (warning) — plan in-progress but absent from hot.md
//
// Rules are pure functions over an index snapshot and the vault; adding a
// new rule is three lines in DefaultRules.
package lint

import (
	"context"
	"fmt"

	"github.com/gosidian/gosidian/internal/index"
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
}

// New wires a Linter against a live vault + index.
func New(v *vault.Vault, idx *index.Index) *Linter {
	return &Linter{vault: v, index: idx}
}

// DefaultRules returns the baseline v1.9 rules in a stable order.
func DefaultRules() []string {
	out := make([]string, 0, len(allRules))
	for _, r := range allRules {
		out = append(out, r.name)
	}
	return out
}

// AllRules returns the known rule registry keyed by name. Exposed for the
// MCP tool to validate `rules` input.
func AllRules() map[string]Severity {
	out := make(map[string]Severity, len(allRules))
	for _, r := range allRules {
		out[r.name] = r.defaultSeverity
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
	if minSeverity != "" {
		all = filterBySeverity(all, minSeverity)
	}
	return all, nil
}

// selectRules resolves the enabled list into ruleSpec instances in the
// order declared by allRules. An empty list means "all default rules".
// Unknown rule names return an error.
func selectRules(enabled []string) ([]ruleSpec, error) {
	if len(enabled) == 0 {
		return allRules, nil
	}
	want := make(map[string]struct{}, len(enabled))
	for _, n := range enabled {
		want[n] = struct{}{}
	}
	out := make([]ruleSpec, 0, len(enabled))
	for _, r := range allRules {
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
