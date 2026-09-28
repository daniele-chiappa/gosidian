// Package mcp — memory_self_stats tool (v1.5, IMP-018).
//
// Read-only introspection of the calling agent's own rate-limit budget and
// token identity. Lets an agent auto-throttle before it gets rejected by the
// write limiter, and echo its scope back so handoff/debug flows can confirm
// which credentials they're running under.
package mcp

import (
	"context"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/mark3labs/mcp-go/mcp"
)

// registerSelfStatsTool adds memory_self_stats.
func (s *Server) registerSelfStatsTool() {
	s.impl.AddTool(mcp.NewTool("memory_self_stats",
		mcp.WithDescription("Introspect the calling token's current state: rate-limit budget (max_per_minute, used, remaining), token identity (id, name, projects, scopes), access (every project the token can reach with its live level, read or write: an account can hold write on some projects only, whatever the token's scopes say), and correlation id of the current MCP session when available. Use this before a burst of writes to avoid hitting the rate limit, or to confirm which credentials you're running under and where you may write."),
	), s.handleSelfStats)
}

type selfStatsTokenInfo struct {
	ID          string   `json:"id"`
	Name        string   `json:"name,omitempty"`
	Project     string   `json:"project,omitempty"`  // legacy single-project display (first project)
	Projects    []string `json:"projects,omitempty"` // full multi-project scope; empty = admin
	Scopes      []string `json:"scopes,omitempty"`
	ToolProfile string   `json:"tool_profile,omitempty"` // "core" when restricted; empty = full
}

// selfStatsAccess is one project the token reaches and what it may do there,
// derived from the live effective token (the owning account's grants).
type selfStatsAccess struct {
	Project string `json:"project"`
	Level   string `json:"level"` // read | write
}

// tokenAccess lists the projects tok can reach, hidden-from-MCP ones
// excluded, with the level the write path would apply (Token.AllowsWrite).
// An unscoped token reaches every project of the vault.
func (s *Server) tokenAccess(tok *auth.Token) []selfStatsAccess {
	names := tok.ProjectList()
	if len(names) == 0 && s.vault != nil {
		if projs, err := s.vault.Projects(); err == nil {
			for _, p := range projs {
				names = append(names, p.Name)
			}
		}
	}
	out := make([]selfStatsAccess, 0, len(names))
	for _, p := range names {
		if s.projectHidden(p) {
			continue
		}
		out = append(out, selfStatsAccess{Project: p, Level: accessLevel(tok, p)})
	}
	return out
}

// accessLevel is "write" when tok may write in project, "read" otherwise.
func accessLevel(tok *auth.Token, project string) string {
	if tok != nil && tok.AllowsWrite(project) {
		return "write"
	}
	return "read"
}

func (s *Server) handleSelfStats(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	info := selfStatsTokenInfo{
		ID:          tok.ID,
		Name:        tok.Name,
		Project:     tok.Project,
		Projects:    tok.ProjectList(),
		Scopes:      append([]string(nil), tok.Scopes...),
		ToolProfile: tok.ToolProfile,
	}
	stats := s.limiter.Stats(tok.ID)
	payload := map[string]any{
		"token":      info,
		"access":     s.tokenAccess(tok),
		"rate_limit": stats,
	}
	if cid := correlationIDFromContext(ctx); cid != "" {
		payload["correlation_id"] = cid
	}
	return mcp.NewToolResultJSON(payload)
}
