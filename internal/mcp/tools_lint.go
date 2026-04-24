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
		mcp.WithDescription("Run structural health checks on a project's notes. Returns issues grouped by severity (error/warning/info). Rules check: broken wikilinks, orphan notes, missing frontmatter, unknown tags (closed vocabulary violation), plan status incoherent with hot.md Active plans. Zero issues of severity:error indicates a coherent vault. Scoped tokens are forced to their project."),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project (top-level folder) to lint. Scoped tokens are forced to their project.")),
		mcp.WithArray("rules", mcp.Description("Optional explicit list of rule names to run. Empty = run all default rules. Known rules: broken-wikilink, orphan-note, frontmatter-missing, frontmatter-tag-unknown, status-incoherent.")),
		mcp.WithString("min_severity", mcp.Description("Filter issues returned by minimum severity (error|warning|info). Empty returns all. Use 'warning' to skip informational issues, 'error' for CI gating.")),
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

	linter := lint.New(s.vault, s.index)
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

	return mcp.NewToolResultJSON(map[string]any{
		"issues":  filtered,
		"summary": summary,
		"project": project,
		"rules":   lint.DefaultRules(),
	})
}
