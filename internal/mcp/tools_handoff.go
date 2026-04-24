// Package mcp — agent handoff tools (v1.5, IMP-015).
//
// Two tools that formalize the "agent A passes state to agent B" pattern
// documented in the project CLAUDE.md: create a handoff note (summary +
// pending items) and query pending ones for a given destination agent.
//
// Handoff notes live at <project>/handoffs/YYYYMMDD-HHMMSS-<slug>.md with a
// stable frontmatter shape so memory_pending_handoffs can filter by
// `to_agent` and `status` with a single NotesByTag("type:handoff") call
// followed by a frontmatter scan.
package mcp

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/mark3labs/mcp-go/mcp"
)

// _ = toNoteDoc is a no-op placeholder kept to document that handoff handlers
// reuse writeAndIndex from tools.go, which performs the vault save + index
// upsert atomically. No local helper needed.

// registerHandoffTools adds the two handoff tools.
func (s *Server) registerHandoffTools() {
	s.impl.AddTool(mcp.NewTool("memory_create_handoff",
		mcp.WithDescription("Create a handoff note that passes context from one agent to another. Stored at <project>/handoffs/YYYYMMDD-HHMMSS-<slug>.md with frontmatter {type: handoff, from_agent, to_agent, status: pending}. The destination agent can then call memory_pending_handoffs to pick it up. Use this instead of an ad-hoc note when control switches between specialized agents during a task."),
		mcp.WithString("project", mcp.Required(), mcp.Description("Target project (top-level folder). Scoped tokens are forced to their project.")),
		mcp.WithString("from_agent", mcp.Required(), mcp.Description("Slug of the source agent (e.g. 'go-backend').")),
		mcp.WithString("to_agent", mcp.Required(), mcp.Description("Slug of the destination agent (e.g. 'web-ui-htmx').")),
		mcp.WithString("summary", mcp.Required(), mcp.Description("One-paragraph summary of what was done and why the handoff is happening.")),
		mcp.WithArray("pending_items", mcp.Description("Optional array of open items the receiving agent should pick up. Rendered as a bullet list in the note body.")),
	), s.handleCreateHandoff)

	s.impl.AddTool(mcp.NewTool("memory_pending_handoffs",
		mcp.WithDescription("List pending handoff notes addressed to a specific agent under a project. Filters by tag type:handoff, frontmatter status:pending, and frontmatter to_agent. Use at session start when taking over from another agent."),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project to search in. Scoped tokens are forced to their project.")),
		mcp.WithString("for_agent", mcp.Required(), mcp.Description("Destination agent slug; matches frontmatter to_agent exactly.")),
	), s.handlePendingHandoffs)
}

type handoffEntry struct {
	Path      string `json:"path"`
	Title     string `json:"title"`
	FromAgent string `json:"from_agent,omitempty"`
	ToAgent   string `json:"to_agent,omitempty"`
	Created   string `json:"created,omitempty"`
	Summary   string `json:"summary,omitempty"`
}

func (s *Server) handleCreateHandoff(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeWrite(ctx, "")
	if errRes != nil {
		return errRes, nil
	}
	project, err := s.resolveProject(tok, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	fromAgent := strings.TrimSpace(req.GetString("from_agent", ""))
	toAgent := strings.TrimSpace(req.GetString("to_agent", ""))
	summary := strings.TrimSpace(req.GetString("summary", ""))
	if fromAgent == "" || toAgent == "" {
		return mcp.NewToolResultError("from_agent and to_agent are required"), nil
	}
	if summary == "" {
		return mcp.NewToolResultError("summary is required"), nil
	}
	pending := req.GetStringSlice("pending_items", nil)

	now := time.Now().UTC()
	slug := handoffSlug(fromAgent, toAgent)
	relPath := fmt.Sprintf("%s/handoffs/%s-%s.md", project, now.Format("20060102-150405"), slug)

	// Scope/path check + size limit enforcement. We reuse authorizeWrite with
	// the concrete path now that we know it.
	if _, errRes := s.authorizeWrite(ctx, relPath); errRes != nil {
		return errRes, nil
	}
	content := renderHandoffBody(fromAgent, toAgent, now, summary, pending)
	if errRes := s.checkWriteLimits(tok, len(content)); errRes != nil {
		return errRes, nil
	}

	if err := s.writeAndIndex(relPath, []byte(content)); err != nil {
		return mcp.NewToolResultErrorFromErr("handoff create failed", err), nil
	}
	s.auditWrite(ctx, audit.ActionCreate, relPath, "", int64(len(content)))

	return mcp.NewToolResultJSON(map[string]any{
		"path":       relPath,
		"from_agent": fromAgent,
		"to_agent":   toAgent,
		"created":    now.Format(time.RFC3339),
	})
}

func (s *Server) handlePendingHandoffs(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	project, err := s.resolveProject(tok, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	forAgent := strings.TrimSpace(req.GetString("for_agent", ""))
	if forAgent == "" {
		return mcp.NewToolResultError("for_agent is required"), nil
	}

	notes, err := s.index.NotesByTag("type:handoff")
	if err != nil {
		return mcp.NewToolResultErrorFromErr("handoff lookup failed", err), nil
	}
	out := make([]handoffEntry, 0)
	prefix := project + "/handoffs/"
	for _, n := range notes {
		if !strings.HasPrefix(n.Path, prefix) {
			continue
		}
		if !tok.AllowsPath(n.Path) {
			continue
		}
		note, loadErr := s.vault.Load(n.Path)
		if loadErr != nil {
			continue
		}
		raw := parser.ExtractFrontmatterRaw(note.Content)
		fm := parser.ParseFrontmatterFields(raw)
		if fmString(fm, "to_agent") != forAgent {
			continue
		}
		if fmString(fm, "status") != "pending" {
			continue
		}
		out = append(out, handoffEntry{
			Path:      n.Path,
			Title:     n.Title,
			FromAgent: fmString(fm, "from_agent"),
			ToAgent:   fmString(fm, "to_agent"),
			Created:   fmString(fm, "created"),
			Summary:   truncateExcerpt(parser.ExtractSection(note.Content, "Summary"), 300),
		})
	}
	return mcp.NewToolResultJSON(map[string]any{"handoffs": out})
}

// ---- helpers ----

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func handoffSlug(from, to string) string {
	base := strings.ToLower(from + "-to-" + to)
	base = slugRe.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		return "handoff"
	}
	return base
}

func renderHandoffBody(from, to string, created time.Time, summary string, pending []string) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "title: Handoff %s → %s\n", from, to)
	fmt.Fprintf(&sb, "type: handoff\n")
	fmt.Fprintf(&sb, "status: pending\n")
	fmt.Fprintf(&sb, "from_agent: %s\n", from)
	fmt.Fprintf(&sb, "to_agent: %s\n", to)
	fmt.Fprintf(&sb, "created: %s\n", created.Format(time.RFC3339))
	fmt.Fprintf(&sb, "tags: [type:handoff, status:pending]\n")
	sb.WriteString("---\n\n")
	fmt.Fprintf(&sb, "# Handoff %s → %s\n\n", from, to)
	sb.WriteString("## Summary\n\n")
	sb.WriteString(summary)
	sb.WriteString("\n\n")
	if len(pending) > 0 {
		sb.WriteString("## Pending items\n\n")
		for _, p := range pending {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			fmt.Fprintf(&sb, "- %s\n", p)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func fmString(fm map[string]any, key string) string {
	if v, ok := fm[key].(string); ok {
		return v
	}
	return ""
}
