package mcp

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/canvas"
	"github.com/mark3labs/mcp-go/mcp"
)

// getCanvas is memory_get on an Obsidian canvas (IMP-144): a read-only note
// whose content is the canvas as text — its cards in reading order, within
// their groups, and its connections — with the notes its file cards name
// as wikilinks, those the token may read. source, the JSON as written, comes
// with raw:true only: it is mostly coordinates. No tool writes a canvas.
func (s *Server) getCanvas(tok *auth.Token, path string, raw bool) *mcp.CallToolResult {
	rel, err := s.vault.Rel(path)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("invalid path", err)
	}
	if s.pathInHiddenProject(rel) {
		return mcp.NewToolResultErrorf("note %q not found", rel)
	}
	c, err := s.vault.LoadCanvas(rel)
	if err != nil {
		return readNoteError(rel, err)
	}
	nc := noteContent{Path: c.Path, Title: c.Title, Kind: "canvas", ETag: c.ETag()}
	cv, err := canvas.Parse(c.Content)
	if err != nil {
		return mcp.NewToolResultErrorf("%q cannot be read as a canvas: %v", rel, err)
	}
	content := canvas.Markdown(c.Path, cv, func(file string) string {
		if r, err := s.vault.Rel(file); err == nil && tok.AllowsPath(r) && !s.pathInHiddenProject(r) && s.vault.Exists(r) {
			return "[[" + strings.TrimSuffix(r, ".md") + "]]"
		}
		return "`" + file + "`"
	})
	nc.Hint = "an Obsidian canvas, read-only: no tool writes it; content is its cards and connections as text, the notes of its file cards as wikilinks"
	if raw {
		nc.Source = string(c.Content)
		nc.Hint += ", source its JSON as written"
	} else {
		if len(content) > getBodySoftCap {
			cut := content[:getTruncChunk]
			if i := bytes.LastIndexByte(cut, '\n'); i > 0 {
				cut = cut[:i+1]
			}
			nc.Truncated, nc.Size = true, int64(len(content))
			nc.Hint += fmt.Sprintf("; truncated (%d of %d bytes): pass raw:true for all of it", len(cut), len(content))
			content = cut
		}
		nc.Hint += " (pass raw:true for its JSON in source)"
	}
	nc.Content = string(content)
	res, err := mcp.NewToolResultJSON(nc)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("encode", err)
	}
	return res
}
