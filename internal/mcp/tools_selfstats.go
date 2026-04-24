// Package mcp — memory_self_stats tool (v1.5, IMP-018).
//
// Read-only introspection of the calling agent's own rate-limit budget and
// token identity. Lets an agent auto-throttle before it gets rejected by the
// write limiter, and echo its scope back so handoff/debug flows can confirm
// which credentials they're running under.
package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
)

// registerSelfStatsTool adds memory_self_stats.
func (s *Server) registerSelfStatsTool() {
	s.impl.AddTool(mcp.NewTool("memory_self_stats",
		mcp.WithDescription("Introspect the calling token's current state: rate-limit budget (max_per_minute, used, remaining), token identity (id, name, project, scopes), and correlation id of the current MCP session when available. Use this before a burst of writes to avoid hitting the rate limit, or to confirm which credentials you're running under."),
	), s.handleSelfStats)
}

type selfStatsTokenInfo struct {
	ID      string   `json:"id"`
	Name    string   `json:"name,omitempty"`
	Project string   `json:"project,omitempty"`
	Scopes  []string `json:"scopes,omitempty"`
}

func (s *Server) handleSelfStats(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	info := selfStatsTokenInfo{
		ID:      tok.ID,
		Name:    tok.Name,
		Project: tok.Project,
		Scopes:  append([]string(nil), tok.Scopes...),
	}
	stats := s.limiter.Stats(tok.ID)
	payload := map[string]any{
		"token":      info,
		"rate_limit": stats,
	}
	if cid := correlationIDFromContext(ctx); cid != "" {
		payload["correlation_id"] = cid
	}
	return mcp.NewToolResultJSON(payload)
}
