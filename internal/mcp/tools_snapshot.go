package mcp

import (
	"context"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/views"
	"github.com/mark3labs/mcp-go/mcp"
)

// registerSnapshotTool adds memory_snapshot (IMP-127 iteration 3, phase 3).
func (s *Server) registerSnapshotTool() {
	s.impl.AddTool(mcp.NewTool("memory_snapshot",
		mcp.WithDescription("Freeze a note as it reads now into a dated note beside it, <folder>/<name>.snapshots/YYYY-MM-DD.md (the time is added for a second one the same day): its views replaced by their rows, its `=count(…)` values by their numbers and its embeds included, so the vault's history keeps what they showed and the snapshot never recomputes. The note itself does not change; the snapshot has type: snapshot, source: [[note]] and the project's tag. Returns its path and how many views, values and embeds it froze."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault-relative path of the markdown note to freeze, e.g. 'proj/hot.md'.")),
	), s.handleSnapshot)
}

func (s *Server) handleSnapshot(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	rel, err := s.vault.Rel(s.notePathArg(raw))
	if err != nil {
		return mcp.NewToolResultErrorFromErr("invalid path", err), nil
	}
	if !strings.HasSuffix(strings.ToLower(rel), ".md") {
		return mcp.NewToolResultErrorf("%q is not a markdown note: only a .md note has views to freeze", rel), nil
	}
	reader, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	if !reader.AllowsPath(rel) || s.pathInHiddenProject(rel) {
		return mcp.NewToolResultErrorf("note %q not found", rel), nil
	}
	note, err := s.vault.Load(rel)
	if err != nil {
		return readNoteError(rel, err), nil
	}
	now := time.Now()
	dest := views.SnapshotPath(rel, now, func(p string) bool {
		_, err := s.vault.Load(p)
		return err == nil
	})
	tok, errRes := s.authorizeWrite(ctx, dest)
	if errRes != nil {
		return errRes, nil
	}
	frozen, st := views.Freeze(note.Content, s.viewContext(tok, rel, note.Content), s.viewQuery(tok))
	project, _, _ := strings.Cut(rel, "/")
	content := views.SnapshotNote(rel, note.Title, project, frozen, now)
	if errRes := s.checkWriteLimits(ctx, tok, len(content)); errRes != nil {
		return errRes, nil
	}
	unlock := s.vault.LockPath(dest)
	defer unlock()
	if _, err := s.vault.Load(dest); err == nil {
		return mcp.NewToolResultErrorf("snapshot %q already exists: try again", dest), nil
	}
	if err := s.writeAndIndex(dest, content); err != nil {
		return mcp.NewToolResultErrorFromErr("write failed", err), nil
	}
	s.auditWrite(ctx, audit.ActionCreate, dest, "", int64(len(content)))
	etag := ""
	if fresh, err := s.vault.Load(dest); err == nil {
		etag = fresh.ETag()
	}
	s.publishNoteChange("create", dest, etag, true)
	return mcp.NewToolResultJSON(map[string]any{
		"path":   dest,
		"source": rel,
		"size":   len(content),
		"views":  st.Views,
		"values": st.Values,
		"embeds": st.Embeds,
	})
}
