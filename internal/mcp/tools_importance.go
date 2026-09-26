// Package mcp — memory_notes_by_importance tool (v1.3, IMP-010).
//
// Convention: notes may carry `importance: N` in their frontmatter, where N
// is an integer 1..5 (5 = critical, 3 = default, 1 = archival). The index
// keeps it in notes.importance (default 3 when absent or unparseable, since
// schema v1), so the tool filters by min_level and sorts by importance DESC
// without loading a single note.
package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
)

// registerImportanceTool adds the memory_notes_by_importance tool.
func (s *Server) registerImportanceTool() {
	s.impl.AddTool(mcp.NewTool("memory_notes_by_importance",
		mcp.WithDescription("List notes in a project ranked by their frontmatter `importance` field (integer 1..5, default 3 when missing). Filtered to importance >= min_level and sorted DESC. Use this instead of memory_list_notes when you need the most important notes of a project for triage or pinned views. Convention: 5=critical, 3=default, 1=archival."),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project (top-level folder). Scoped tokens are forced to their project.")),
		mcp.WithNumber("min_level", mcp.Description("Minimum importance level (1..5). Default 3.")),
		mcp.WithNumber("limit", mcp.Description("Max notes to return (default 50, max 500).")),
	), s.handleNotesByImportance)
}

type importanceEntry struct {
	Path       string `json:"path"`
	Title      string `json:"title"`
	Importance int    `json:"importance"`
}

func (s *Server) handleNotesByImportance(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	project, err := s.resolveProject(tok, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	minLevel := req.GetInt("min_level", 3)
	if minLevel < 1 {
		minLevel = 1
	}
	if minLevel > 5 {
		minLevel = 5
	}
	limit := req.GetInt("limit", 50)
	if limit <= 0 || limit > 500 {
		limit = 50
	}

	rows, err := s.index.NotesByImportance(project, minLevel)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("list failed", err), nil
	}
	collected := make([]importanceEntry, 0)
	for _, n := range rows {
		if !tok.AllowsPath(n.Path) {
			continue
		}
		if len(collected) == limit {
			break
		}
		collected = append(collected, importanceEntry{Path: n.Path, Title: n.Title, Importance: n.Importance})
	}
	return mcp.NewToolResultJSON(map[string]any{"notes": collected})
}
