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

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/vault"
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
	// The tasks of the teams of agents (IMP-195): the run tokens are core.
	"memory_tasks":          {},
	"memory_task_create":    {},
	"memory_task_claim":     {},
	"memory_task_heartbeat": {},
	"memory_task_update":    {},
	"memory_task_complete":  {},
}

// filterToolsByProfile is the ToolFilterFunc wired into the MCPServer. The
// token travels in ctx for every client→server message (WithSSEContextFunc),
// so the same decision applies to tools/list and tools/call. Beyond the
// static core set, memory_self_improve is admitted dynamically for tokens
// opted into that loop. The media/table creators no longer have a dynamic
// admission: memory_ingest routes to them internally, so a core worker gets
// table/media notes without ever seeing their schemas (ADR-018).
//
// The plugins' tools (IMP-182) pass for every profile, core included, but
// only to a token of a project the owner exposed their plugin in; the layout
// tools, which work with the engine of a plugin, the same.
func (s *Server) filterToolsByProfile(ctx context.Context, tools []mcp.Tool) []mcp.Tool {
	tok := s.tokenFromContext(ctx)
	core := tok != nil && tok.IsCoreProfile()
	out := make([]mcp.Tool, 0, len(tools))
	var layoutShown *bool
	for _, t := range tools {
		if pt, ok := s.pluginToolOf(t.Name); ok {
			if s.pluginToolVisible(tok, pt) {
				out = append(out, t)
			}
			continue
		}
		if layoutTools[t.Name] {
			if layoutShown == nil {
				shown := s.layoutVisible(tok)
				layoutShown = &shown
			}
			if *layoutShown {
				out = append(out, t)
			}
			continue
		}
		if s.branchToolOff(tok, t.Name) {
			continue
		}
		if !core {
			out = append(out, t)
			continue
		}
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

// branchToolOff reports whether name is the tool of a branch that is off
// for tok (ADR-035, roadmap M2.2): a branch off costs no schema. The
// pipelines' tools and memory_create_team, whose teams run as pipelines,
// need [pipelines] on; memory_in_upload needs a project with use_in that
// tok may write in. The creators of table and media notes need their
// format on (roadmap M1.3): off, they could only refuse, and memory_ingest
// stores those files as attachments. Hidden, a tool is also "not found" on
// tools/call.
func (s *Server) branchToolOff(tok *auth.Token, name string) bool {
	switch name {
	case "memory_pipelines", "memory_pipeline_run", "memory_create_team":
		return !s.pipelinesOn
	case "memory_in_upload":
		return !s.inWritable(tok)
	case "memory_create_table_note":
		return !s.vault.TableNotesEnabled()
	case "memory_create_media_note":
		return !s.vault.MediaNotesEnabled()
	}
	return false
}

// inWritable reports whether tok may upload into some project's IN folder,
// with the checks of authorizeWrite.
func (s *Server) inWritable(tok *auth.Token) bool {
	if s.in == nil || s.projects == nil || tok == nil || !tok.HasScope(auth.ScopeWrite) {
		return false
	}
	for _, p := range s.projects.INNames() {
		root := vault.INRoot(p)
		if !s.projectHidden(p) && tok.AllowsPath(root) && tok.InWritePaths(root) && tok.AllowsWrite(root) {
			return true
		}
	}
	return false
}
