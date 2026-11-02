// Package mcp — memory_lint tool (v1.9).
//
// Exposes the internal/lint package as an MCP tool: `go vet` for the vault.
// Agents run it as a self-check before committing a session's work to catch
// structural drift (broken wikilinks, orphan notes, malformed frontmatter,
// tag-vocabulary leaks, plan/hot.md status divergence) silently accumulated
// during editing.
package mcp

import (
	"context"
	"strings"

	"github.com/gosidian/gosidian/internal/lint"
	"github.com/mark3labs/mcp-go/mcp"
)

// registerLintTool wires memory_lint into the MCP surface.
func (s *Server) registerLintTool() {
	s.impl.AddTool(mcp.NewTool("memory_lint",
		mcp.WithDescription("Run structural health checks on a project's notes; issues grouped by severity (error/warning/info). The default rules: broken-wikilink, broken wikilinks; broken-anchor, links to a heading that is not there (an ID alone such as ADR-010 names \"ADR-010 — …\"); orphan-note, orphan notes; frontmatter-missing, missing frontmatter; frontmatter-invalid-yaml, frontmatter that is not valid YAML or a value cut at a ' #'; frontmatter-tag-unknown, a tag outside the closed vocabulary (a vocabulary violation; a project with the use_tag_vocabulary flag extends it via `tag_vocabulary:` in memory/conventions.md frontmatter, exact tags or ns:* wildcards); status-incoherent, a plan status against hot.md Active plans; hot-oversize, an oversized hot.md (it is inlined into every bootstrap payload); skill-oversize, a type:skill note larger than memory_get serves whole; database-field-invalid, a row of a `type: database` note whose frontmatter breaks its schema (undeclared fields, missing required ones, values of the wrong type); field-tag-mismatch, a namespaced tag that disagrees with its field (status:x against status, and on a database's rows its select fields); pipeline-invalid, a `type: pipeline` note the server would refuse to run (no ```pipeline block, YAML that does not parse, an unknown node type, a reference to a node that does not run before); diagram-invalid, graph-invalid, chat-invalid, team-invalid and board-invalid, a ```diagram, ```graph, ```chat, ```team or ```board block that does not read, on the line of the problem, or a note of its type (type: diagram, …) without exactly one block, and also a bot whose pipeline is no note of the project (chat-invalid), a role whose agent is no note of the vault (team-invalid), a card whose board is none or whose column is not its state's, and a database of the tasks without the cards' fields (board-invalid); diagram-twin-stale, a diagram's ```mermaid twin made from an earlier version of it, or with no diagram above. Zero issues of severity:error indicates a coherent vault. "+scopedProjectNote),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project (top-level folder) to lint. "+scopedProjectNote)),
		mcp.WithArray("rules", mcp.Description("The rule names to run; empty runs all the default ones. One more runs only when named: unlinked-mentions, prose that names another note's title/basename without a wikilink (advisory, higher-noise; it skips index notes as sources, a label two notes share, and database templates). A note turns a rule off for itself with lint_disable: [rule, …] in its frontmatter.")),
		mcp.WithString("min_severity", mcp.Description("The lowest severity returned (error|warning|info); empty returns all. 'warning' skips the informational issues, 'error' is for CI gating.")),
	), s.handleLint)
}

func (s *Server) handleLint(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	project, err := s.resolveProject(tok, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	rules := req.GetStringSlice("rules", nil)
	for i, r := range rules {
		rules[i] = strings.TrimSpace(r)
	}

	minSeverity := lint.Severity(strings.TrimSpace(req.GetString("min_severity", "")))
	if minSeverity != "" {
		switch minSeverity {
		case lint.SeverityError, lint.SeverityWarning, lint.SeverityInfo:
			// ok
		default:
			return mcp.NewToolResultErrorf("unknown min_severity %q (expected error, warning, info)", string(minSeverity)), nil
		}
	}

	linter := lint.New(s.vault, s.index).
		WithExtraAllowedTags(s.lintExtraAllowedTags).
		WithHotOversizeLimit(s.lintHotOversizeBytes).
		WithSkillOversizeLimit(getBodySoftCap).
		WithProjectTagVocabulary(s.projects != nil && s.projects.UsesTagVocabulary(project)).
		WithPipelineTypes(pipelineHost{s}.types())
	issues, err := linter.Run(ctx, project, rules, minSeverity)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("lint run failed", err), nil
	}
	// Token scope filter: drop issues for files outside the caller's scope.
	// (NotesByPrefix already filters on project, but scoped tokens may be
	// narrower than one project — belt and braces.)
	filtered := make([]lint.Issue, 0, len(issues))
	for _, i := range issues {
		if !tok.AllowsPath(i.File) {
			continue
		}
		filtered = append(filtered, i)
	}
	summary := lint.Summarise(filtered)

	// Echo the rules actually run, not a hardcoded default: an explicit
	// `rules` argument (e.g. the opt-in unlinked-mentions) overrides the
	// defaults, and a consumer reading this field must see what ran.
	ranRules := rules
	if len(ranRules) == 0 {
		ranRules = lint.DefaultRules()
	}

	return mcp.NewToolResultJSON(map[string]any{
		"issues":  filtered,
		"summary": summary,
		"project": project,
		"rules":   ranRules,
	})
}
