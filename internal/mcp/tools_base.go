package mcp

import (
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/views"
	"github.com/mark3labs/mcp-go/mcp"
)

// getBase is memory_get on an Obsidian base (IMP-118): a read-only note
// whose content is the base's views translated into view blocks, computed
// with render_views, and whose source is the YAML as written. No tool
// writes it: it is no note.
func (s *Server) getBase(tok *auth.Token, path string, render bool) *mcp.CallToolResult {
	rel, err := s.vault.Rel(path)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("invalid path", err)
	}
	if s.pathInHiddenProject(rel) {
		return mcp.NewToolResultErrorf("note %q not found", rel)
	}
	b, err := s.vault.LoadBase(rel)
	if err != nil {
		return readNoteError(rel, err)
	}
	content := views.BaseMarkdown(b.Path, b.Content)
	nc := noteContent{Path: b.Path, Title: b.Title, Kind: "base", Source: string(b.Content), ETag: b.ETag()}
	if render {
		if out, hash := s.renderViews(tok, b.Path, content); hash != "" {
			content, nc.ViewsRendered = out, true
		}
	}
	nc.Content = string(content)
	nc.Hint = "an Obsidian base, read-only: no tool writes it; content is its views translated into ```view blocks"
	if !nc.ViewsRendered {
		nc.Hint += " (pass render_views:true to compute them)"
	}
	nc.Hint += ", source its YAML as written"
	res, err := mcp.NewToolResultJSON(nc)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("encode", err)
	}
	return res
}
