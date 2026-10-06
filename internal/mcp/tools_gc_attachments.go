package mcp

import (
	"context"
	"path"
	"sort"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/mark3labs/mcp-go/mcp"
)

// gcListCap bounds the orphans one answer lists; the totals count them all.
const gcListCap = 200

type gcOrphan struct {
	Path     string  `json:"path"`
	Size     int64   `json:"size"`
	AgeHours float64 `json:"age_hours"`
	TrashID  string  `json:"trash_id,omitempty"`
}

// handleGCAttachments implements memory_gc_attachments (IMP-033): the
// attachments of a project that no file of the vault names any more, older
// than min_age_hours. A dry run (the default) lists them; with dry_run
// false they go to the trash when it is on, else they are deleted.
func (s *Server) handleGCAttachments(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	project, err := s.resolveProject(tok, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if project == "" {
		return mcp.NewToolResultError("project is required"), nil
	}
	if !tok.AllowsPath(project + "/attachments/x") {
		return mcp.NewToolResultErrorf("project %q is outside the token's scope", project), nil
	}
	dryRun := req.GetBool("dry_run", true)
	minAge := req.GetFloat("min_age_hours", 24)
	if minAge < 0 {
		return mcp.NewToolResultError("min_age_hours must be >= 0"), nil
	}
	if !dryRun {
		if _, errRes := s.authorizeWrite(ctx, project+"/attachments/x"); errRes != nil {
			return errRes, nil
		}
		if errRes := s.checkWriteLimits(ctx, tok, 0); errRes != nil {
			return errRes, nil
		}
	}

	atts, err := s.vault.ProjectAttachments(project, allowedExtSet())
	if err != nil {
		return mcp.NewToolResultErrorFromErr("list attachments", err), nil
	}
	names := make([]string, 0, len(atts))
	for _, a := range atts {
		names = append(names, path.Base(a.Path))
	}
	// References come from the whole vault, whatever the token's scope: a
	// note of another project naming the file keeps it. Only this project's
	// files are listed, so nothing outside the scope is named.
	unref, err := s.vault.Unreferenced(names)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("scan references", err), nil
	}
	now := time.Now()
	var orphans []gcOrphan
	var bytes int64
	young := 0
	for _, a := range atts {
		if !unref[path.Base(a.Path)] {
			continue
		}
		age := now.Sub(a.ModTime).Hours()
		if age < minAge {
			young++
			continue
		}
		orphans = append(orphans, gcOrphan{Path: a.Path, Size: a.Size, AgeHours: float64(int(age*10)) / 10})
		bytes += a.Size
	}
	sort.Slice(orphans, func(i, j int) bool { return orphans[i].Path < orphans[j].Path })

	out := map[string]any{
		"project":       project,
		"dry_run":       dryRun,
		"attachments":   len(atts),
		"orphans_count": len(orphans),
		"orphans_bytes": bytes,
		"too_young":     young,
		"min_age_hours": minAge,
		"trash":         s.trash != nil,
	}
	if !dryRun {
		removed := 0
		var failed []string
		for i := range orphans {
			o := &orphans[i]
			if s.trash != nil {
				id, err := s.trash.DiscardNote(o.Path)
				if err != nil {
					failed = append(failed, o.Path+": "+err.Error())
					continue
				}
				o.TrashID = id
			} else if err := s.vault.DeleteAttachment(o.Path, allowedExtSet()); err != nil {
				failed = append(failed, o.Path+": "+err.Error())
				continue
			}
			s.auditWrite(ctx, audit.ActionDeleteAttachment, o.Path, o.TrashID, o.Size)
			removed++
		}
		out["removed"] = removed
		if len(failed) > 0 {
			out["failed"] = failed
		}
	}
	if len(orphans) > gcListCap {
		out["truncated"] = true
		orphans = orphans[:gcListCap]
	}
	if orphans == nil {
		orphans = []gcOrphan{}
	}
	out["orphans"] = orphans
	return mcp.NewToolResultJSON(out)
}
