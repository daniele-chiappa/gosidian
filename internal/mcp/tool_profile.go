// Package mcp — per-token tool profiles (plan 20260706-tool-profile-per-token).
//
// A token created with --tool-profile core sees only the worker subset of the
// tool catalogue: mcp-go's WithToolFilter applies the cut to tools/list AND
// enforces it on tools/call (a filtered-out tool is "not found" even when
// called by name), so the profile is an access-control boundary, not a
// visibility hint. Empty/"full" profiles — including admin tokens and
// auth-disabled deployments — see everything, keeping existing setups intact.
package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
)

// coreToolSet is the worker subset: session start, note CRUD, targeted reads,
// search/discovery basics, file ingestion and the handoff lifecycle.
// Everything else (admin, audit, lint, compact, scaffold, init/promote,
// advanced discovery) stays full-profile — Prometheus shows ~30 tools with
// 0-3 calls/30d, all outside this set.
//
// ADR-018: memory_ingest is the ONLY file door workers see. The legacy upload
// matrix (upload_attachment, upload_resource, create_media_note,
// create_table_note) stays full-profile — collapsing the 5-tool × 4-channel
// choice that made agents stall on "save this CSV/screenshot".
var coreToolSet = map[string]struct{}{
	"memory_bootstrap":        {},
	"memory_get":              {},
	"memory_get_section":      {},
	"memory_get_outline":      {},
	"memory_get_frontmatter":  {},
	"memory_batch_get":        {},
	"memory_create":           {},
	"memory_update":           {},
	"memory_edit":             {},
	"memory_append":           {},
	"memory_search":           {},
	"memory_list_notes":       {},
	"memory_list_projects":    {},
	"memory_notes_by_tag":     {},
	"memory_query":            {},
	"memory_ingest":           {},
	"memory_create_handoff":   {},
	"memory_pending_handoffs": {},
	"memory_claim_handoff":    {},
	"memory_complete_handoff": {},
	"memory_wait_changes":     {},
}

// filterToolsByProfile is the ToolFilterFunc wired into the MCPServer. The
// token travels in ctx for every client→server message (WithSSEContextFunc),
// so the same decision applies to tools/list and tools/call. Beyond the
// static core set, memory_self_improve is admitted dynamically for tokens
// opted into that loop. The media/table creators no longer have a dynamic
// admission: memory_ingest routes to them internally, so a core worker gets
// table/media notes without ever seeing their schemas (ADR-018).
func (s *Server) filterToolsByProfile(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
	tok := s.tokenFromContext(ctx)
	if tok == nil || !tok.IsCoreProfile() {
		return tools
	}
	out := make([]mcp.Tool, 0, len(coreToolSet)+1)
	for _, t := range tools {
		if _, ok := coreToolSet[t.Name]; ok {
			out = append(out, t)
			continue
		}
		if t.Name == "memory_self_improve" && tok.SelfImproveOptIn {
			out = append(out, t)
		}
	}
	return out
}
