// Package mcp — memory_notes_by_importance tool (v1.3, IMP-010).
//
// Convention: notes may carry `importance: N` in their frontmatter, where N
// is an integer 1..5 (5 = critical, 3 = default, 1 = archival). This tool
// enumerates notes in a project, parses the importance from each note's
// frontmatter (defaulting to 3 when absent / unparseable), filters by
// min_level, and returns them sorted by importance DESC.
//
// Trade-off: reading frontmatter per-note is O(N) vault loads. Adequate for
// projects under ~1000 notes thanks to the LRU cache. If this becomes a
// bottleneck, promote `importance` to a column on the notes table (see
// follow-up in gosidian/plans/20260422-v1.3-importance-search.md).
package mcp

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/gosidian/gosidian/internal/parser"
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

	notes, err := s.index.NotesByPrefix(project)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("list failed", err), nil
	}

	collected := make([]importanceEntry, 0)
	for _, n := range notes {
		if !tok.AllowsPath(n.Path) {
			continue
		}
		imp := s.readImportance(n.Path)
		if imp < minLevel {
			continue
		}
		collected = append(collected, importanceEntry{
			Path:       n.Path,
			Title:      n.Title,
			Importance: imp,
		})
	}
	sort.SliceStable(collected, func(i, j int) bool {
		if collected[i].Importance != collected[j].Importance {
			return collected[i].Importance > collected[j].Importance
		}
		return collected[i].Path < collected[j].Path
	})
	if len(collected) > limit {
		collected = collected[:limit]
	}
	return mcp.NewToolResultJSON(map[string]any{"notes": collected})
}

// readImportance loads the note at `path`, parses its frontmatter, and
// returns the `importance` scalar as an int clamped to [1,5]. Missing or
// unparseable values return 3 (the convention's default) so unannotated
// notes remain visible at min_level<=3 and hidden at min_level>=4.
func (s *Server) readImportance(path string) int {
	note, err := s.vault.Load(path)
	if err != nil {
		return 3
	}
	raw := parser.ExtractFrontmatterRaw(note.Content)
	fm := parser.ParseFrontmatterFields(raw)
	v, ok := fm["importance"].(string)
	if !ok || v == "" {
		return 3
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 3
	}
	if n < 1 {
		return 1
	}
	if n > 5 {
		return 5
	}
	return n
}
