// Package mcp — graph analytics tools (memory_hubs, memory_path).
//
// These expose the wikilink graph that the index already maintains for
// backlinks/outlinks as two agent-facing lenses:
//
//   - memory_hubs: the most-connected notes ("god nodes") — the inverse of the
//     orphan-note lint, useful for grooming and for orienting in an unfamiliar
//     project.
//   - memory_path: the shortest wikilink path between two notes — answers "how
//     is this ADR connected to that convention?" without N manual backlink hops.
//
// Both are read-only and honour the caller's token scope: a node outside the
// token's project scope (or in a hidden project) is never revealed.
package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
)

// registerGraphTools wires memory_hubs and memory_path into the MCP surface.
func (s *Server) registerGraphTools() {
	s.impl.AddTool(mcp.NewTool("memory_hubs",
		mcp.WithDescription("List the most-connected notes (\"hubs\"/\"god nodes\") ranked by undirected wikilink degree, descending. The inverse signal of orphan notes: hubs are where the vault graph concentrates. Pass `project` to scope to one top-level folder (degree then counts only intra-project links); empty = vault-wide. "+scopedProjectNote),
		mcp.WithString("project", mcp.Description("Optional project (top-level folder) to scope the ranking. Empty = vault-wide.")),
		mcp.WithNumber("limit", mcp.Description("Max hubs to return (default 20, max 100).")),
	), s.handleHubs)

	s.impl.AddTool(mcp.NewTool("memory_path",
		mcp.WithDescription("Find the shortest path between two notes over the undirected wikilink graph (resolved links only). Returns the ordered list of note paths from `from` to `to`, inclusive of both endpoints, or path:[] with found:false when they are not connected. Both endpoints must be inside the token's scope; if the shortest path traverses a note outside the caller's scope the call reports no reachable path rather than leaking foreign paths."),
		mcp.WithString("from", mcp.Required(), mcp.Description("Vault-relative path of the source note.")),
		mcp.WithString("to", mcp.Required(), mcp.Description("Vault-relative path of the target note.")),
		mcp.WithNumber("max_depth", mcp.Description("Maximum number of hops to search (default 6, max 20). Caps the BFS so a query on a large vault stays cheap.")),
	), s.handlePath)
}

type hubEntry struct {
	Path   string `json:"path"`
	Title  string `json:"title"`
	Degree int    `json:"degree"`
}

func (s *Server) handleHubs(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	// The project goes through scopedProject (parity with memory_list_tags).
	project, err := scopedProject(tok, req.GetString("project", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if project != "" {
		if res := s.rejectIfHidden(project); res != nil {
			return res, nil
		}
	}

	limit := req.GetInt("limit", 20)
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	hubs, err := s.index.Hubs(project, limit)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("hubs failed", err), nil
	}
	out := make([]hubEntry, 0, len(hubs))
	for _, h := range hubs {
		// Vault-wide ranking may surface notes the token cannot read; drop them.
		if !tok.AllowsPath(h.Path) || s.pathInHiddenProject(h.Path) {
			continue
		}
		out = append(out, hubEntry{Path: h.Path, Title: h.Title, Degree: h.Degree})
	}
	return mcp.NewToolResultJSON(map[string]any{"hubs": out})
}

func (s *Server) handlePath(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	from, err := req.RequireString("from")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	to, err := req.RequireString("to")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if !tok.AllowsPath(from) {
		return mcp.NewToolResultErrorf("path %q is outside the token's scope", from), nil
	}
	if !tok.AllowsPath(to) {
		return mcp.NewToolResultErrorf("path %q is outside the token's scope", to), nil
	}

	maxDepth := req.GetInt("max_depth", 6)
	if maxDepth <= 0 || maxDepth > 20 {
		maxDepth = 6
	}

	path, err := s.index.BFSPath(from, to, maxDepth)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("path search failed", err), nil
	}
	// A path that crosses a note outside the caller's scope (or a hidden
	// project) must not leak those paths: report it as unreachable instead.
	for _, p := range path {
		if !tok.AllowsPath(p) || s.pathInHiddenProject(p) {
			path = nil
			break
		}
	}
	return mcp.NewToolResultJSON(map[string]any{
		"from":  from,
		"to":    to,
		"found": path != nil,
		"path":  path,
		"hops":  pathHops(path),
	})
}

// pathHops is the number of edges in a path (len-1), or 0 for an empty/unfound
// path. Kept as a tiny helper so the JSON shape is explicit about hop count.
func pathHops(path []string) int {
	if len(path) < 2 {
		return 0
	}
	return len(path) - 1
}
