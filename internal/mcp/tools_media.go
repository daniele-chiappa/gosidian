package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/audit"
	"github.com/mark3labs/mcp-go/mcp"
)

// registerMediaTools adds the image media-note tools (ADR-013). Called from
// registerTools() alongside registerAttachmentTools().
func (s *Server) registerMediaTools() {
	s.impl.AddTool(mcp.NewTool("memory_create_media_note",
		mcp.WithDescription("Create an image media note: upload (or reference) an image AND create the markdown note that points to it, in one call. The note is a normal .md (frontmatter `type: image` + `media:`), indexed/linked like any note. The body is the caption — WRITE A DESCRIPTIVE ONE: it is the only searchable text (image bytes are not indexed). Images only (png/jpg/jpeg/gif/webp/svg). Requires media_notes (see bootstrap `capabilities`). Provide the image ONE way, cheapest first: `attachment` (already-uploaded path), `bridge_filename`, `source_path`, or base64 `data` (last resort)."),
		mcp.WithString("project", mcp.Required(), mcp.Description("Vault project for both the note and the image attachment.")),
		mcp.WithString("caption", mcp.Description("Markdown body describing the image — the searchable transcript that lands in FTS. Strongly recommended: an empty caption yields a media note with no retrievable text.")),
		mcp.WithString("title", mcp.Description("Note title. Defaults to the image filename (without extension) when omitted.")),
		mcp.WithString("path", mcp.Description("Explicit vault-relative note path (e.g. 'proj/diagram.md'). When omitted, derived as '<project>/<slug(title|filename)>.md'. A missing .md extension is appended.")),
		mcp.WithString("attachment", mcp.Description("PREFERRED: vault-relative path of an image you ALREADY uploaded — via the HTTP upload endpoint (your MCP base URL plus /upload, e.g. .../mcp -> .../mcp/upload) or memory_upload_resource. The note references it, no re-upload. Cheapest path: POST the bytes over HTTP, then pass the returned `path` here.")),
		mcp.WithString("bridge_filename", mcp.Description("The basename of an image you staged in the server's bridge dir (GOSIDIAN_MCP_BRIDGE_DIR). Read and consumed server-side — near-zero token cost (co-located deploys).")),
		mcp.WithString("data", mcp.Description("Base64-encoded image content. Costly for large images (~1 token/char) — prefer bridge_filename/source_path. Required only when neither of those is used.")),
		mcp.WithString("source_path", mcp.Description("Absolute server-side filesystem path to the image. Use when gosidian and the agent share a filesystem; otherwise use data or bridge_filename. Must be inside the vault, the bridge dir, or an allowed upload root.")),
		mcp.WithString("filename", mcp.Description("Original image filename for extension validation. Required with data, optional with source_path/bridge_filename (defaults to basename).")),
	), s.handleCreateMediaNote)
}

func (s *Server) handleCreateMediaNote(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !s.vault.MediaNotesEnabled() {
		return mcp.NewToolResultError("media notes are disabled on this instance — a per-project flag an admin can flip live from the web UI project toggles (or [vault] media_notes / GOSIDIAN_VAULT_MEDIA_NOTES). Meanwhile the image can still be stored as a plain attachment (memory_ingest or memory_upload_attachment)"), nil
	}
	project, err := req.RequireString("project")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	project = strings.TrimSpace(project)
	if project == "" {
		return mcp.NewToolResultError("project must not be empty"), nil
	}

	dataB64 := req.GetString("data", "")
	sourcePath := req.GetString("source_path", "")
	filename := req.GetString("filename", "")
	caption := req.GetString("caption", "")
	title := strings.TrimSpace(req.GetString("title", ""))
	explicitPath := strings.TrimSpace(req.GetString("path", ""))
	bridgeFilename := strings.TrimSpace(req.GetString("bridge_filename", ""))
	attachment := strings.TrimSpace(req.GetString("attachment", ""))

	// Resolve the image extension up front so a non-image is rejected before any
	// storage or note write happens. With attachment/source_path/bridge_filename
	// and no filename, the basename supplies the extension.
	fnForExt := filename
	if fnForExt == "" && sourcePath != "" {
		fnForExt = filepath.Base(sourcePath)
	}
	if fnForExt == "" && bridgeFilename != "" {
		fnForExt = filepath.Base(bridgeFilename)
	}
	if fnForExt == "" && attachment != "" {
		fnForExt = filepath.Base(attachment)
	}
	if fnForExt == "" {
		return mcp.NewToolResultError("provide an image via attachment (already-uploaded path), bridge_filename, source_path, or data (with filename)"), nil
	}
	ext := strings.ToLower(filepath.Ext(fnForExt))
	mime, isImage, extErr := attach.ValidateExt(ext)
	if extErr != nil {
		return mcp.NewToolResultError(extErr.Error()), nil
	}
	if !isImage {
		return mcp.NewToolResultErrorf("media notes support images only; %q is not an image type", ext), nil
	}

	// Default the title from the filename stem; used both for the note title and
	// (when no explicit path) the derived slug.
	stem := strings.TrimSuffix(filepath.Base(fnForExt), filepath.Ext(fnForExt))
	if title == "" {
		title = stem
	}

	// Resolve and validate the note path (explicit or derived).
	notePath := explicitPath
	if notePath == "" {
		notePath = project + "/" + slugifyFilename(title) + ".md"
	}
	if !strings.HasSuffix(strings.ToLower(notePath), ".md") {
		notePath += ".md"
	}
	rel, err := s.vault.Rel(notePath)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("invalid note path", err), nil
	}
	tok, errRes := s.authorizeWrite(ctx, rel)
	if errRes != nil {
		return errRes, nil
	}
	// The exists-probe and the write below share the per-path lock, like
	// every other create path: without it two concurrent creates both pass
	// the probe and the second silently overwrites the first.
	unlock := s.vault.LockPath(rel)
	defer unlock()
	if _, err := s.vault.Load(rel); err == nil {
		return mcp.NewToolResultErrorf("note %q already exists", rel), nil
	}

	// Obtain the image: reference an already-uploaded attachment (e.g. from
	// POST /mcp/upload) when `attachment` is set, otherwise upload it now.
	var res *attach.Result
	var size int64
	if attachment != "" {
		aRel, aErr := s.vault.Rel(attachment)
		if aErr != nil {
			return mcp.NewToolResultErrorFromErr("invalid attachment path", aErr), nil
		}
		if !tok.AllowsPath(aRel) {
			return mcp.NewToolResultErrorf("attachment %q is outside the token's scope", aRel), nil
		}
		abs, aErr := s.vault.Abs(aRel)
		if aErr != nil {
			return mcp.NewToolResultErrorFromErr("resolve attachment", aErr), nil
		}
		fi, statErr := os.Stat(abs)
		if statErr != nil || fi.IsDir() {
			return mcp.NewToolResultErrorf("attachment %q not found", aRel), nil
		}
		res = &attach.Result{Path: aRel}
		size = fi.Size()
	} else {
		var errRes *mcp.CallToolResult
		res, size, errRes = s.storeAttachmentFromRequest(ctx, project, filename, dataB64, sourcePath, bridgeFilename)
		if errRes != nil {
			return errRes, nil
		}
		s.auditWrite(ctx, audit.ActionUploadAttachment, res.Path, "", size)
	}

	// Assemble and write the note.
	content := buildMediaNote(title, res.Path, project, caption)
	if errRes := s.checkWriteLimits(tok, len(content)); errRes != nil {
		return errRes, nil
	}
	if err := s.writeAndIndex(rel, []byte(content)); err != nil {
		// The attachment is content-addressed (dedup by hash) and orphan GC is
		// tracked separately (IMP-033), so we leave it and surface its path so a
		// retry can reference it instead of re-uploading.
		return mcp.NewToolResultErrorf("note write failed (image stored at %q): %v", res.Path, err), nil
	}
	s.auditWrite(ctx, audit.ActionCreate, rel, "", int64(len(content)))
	if fresh, err := s.vault.Load(rel); err == nil {
		s.publishNoteChange("create", rel, fresh.ETag(), true)
	} else {
		s.publishNoteChange("create", rel, "", true)
	}

	out := map[string]any{
		"path": rel,
		"kind": "image",
		"media": map[string]any{
			"path": res.Path,
			"url":  "/vault-files/" + res.Path,
			"mime": mime,
			"size": size,
		},
	}
	if strings.TrimSpace(caption) == "" {
		out["warning"] = "empty caption: this media note has no searchable text (FTS). Add a caption describing the image so an agent can retrieve it."
	}
	if h := s.bridgeHint(dataB64); h != "" {
		out["hint"] = h
	}
	return mcp.NewToolResultJSON(out)
}

// slugifyFilename turns a title into a safe, lowercase note-file stem: runs of
// non-alphanumeric characters collapse to a single dash, leading/trailing
// dashes are trimmed. Falls back to "image" when nothing survives.
func slugifyFilename(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "image"
	}
	return out
}

// buildMediaNote assembles the markdown source of an image media note: the
// frontmatter (title, type:image, media pointer, project + type:image tags)
// followed by the caption as the body.
func buildMediaNote(title, mediaPath, project, caption string) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %s\n", yamlQuote(title))
	b.WriteString("type: image\n")
	fmt.Fprintf(&b, "media: %s\n", mediaPath)
	fmt.Fprintf(&b, "tags: [%s, type:image]\n", project)
	b.WriteString("---\n\n")
	if c := strings.TrimSpace(caption); c != "" {
		b.WriteString(c)
		b.WriteString("\n")
	}
	return b.String()
}
