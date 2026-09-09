package mcp

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/audit"
	"github.com/mark3labs/mcp-go/mcp"
)

// bridgeHintThreshold is the base64 length above which an upload is wasteful
// enough to warrant redirecting the agent to the bridge dir (IMP-059).
const bridgeHintThreshold = 128 << 10

// bridgeHint returns a redirect message when a base64 upload is large enough to
// be wasteful. It always points at the HTTP upload endpoint (the primary cheap
// path, always available) and adds the bridge dir when one is configured.
// Surfaced in the tool result so the agent learns the efficient path next time.
func (s *Server) bridgeHint(dataB64 string) string {
	if len(dataB64) <= bridgeHintThreshold {
		return ""
	}
	msg := fmt.Sprintf("this base64 upload pushed ~%d KiB through the context (≈ that many tokens). "+
		"Cheaper: POST the file (multipart, field 'file') with your bearer token to the upload endpoint — it is your MCP /sse URL with /sse replaced by /upload (e.g. .../mcp/sse -> .../mcp/upload, or legacy :8765/sse -> :8765/upload). The bytes go over HTTP, not the context; the response carries the path to reference.",
		len(dataB64)>>10)
	if s.bridgeDir != "" {
		msg += fmt.Sprintf(" Or stage it in the bridge dir %q and pass bridge_filename.", s.bridgeDir)
	}
	return msg
}

// registerAttachmentTools adds the four attachment-related tools.
// Called from registerTools().
func (s *Server) registerAttachmentTools() {
	s.impl.AddTool(mcp.NewTool("memory_upload_attachment",
		mcp.WithDescription("Upload a file attachment to the vault and get a ready-to-splice markdown embed. CHEAPEST for large files: POST multipart (field 'file', bearer token) to the /upload endpoint — your MCP /sse URL with /sse replaced by /upload; bytes travel over HTTP, not the model context. This tool takes ONE source: base64 `data`, server-side `source_path`, or `bridge_filename` (staged in the bridge dir). Size cap and allowed extensions are in bootstrap `capabilities`."),
		mcp.WithString("bridge_filename", mcp.Description("PREFERRED for images/binaries: the basename of a file you staged in the server's bridge dir (GOSIDIAN_MCP_BRIDGE_DIR). The server reads it from there and consumes it — near-zero token cost (no base64 through the context).")),
		mcp.WithString("data", mcp.Description("Base64-encoded file content. Costly for large files (~1 token/char) — prefer bridge_filename/source_path. Required only when neither of those is used.")),
		mcp.WithString("source_path", mcp.Description("Absolute filesystem path to the file — RESOLVED ON THE SERVER, not the client. Use this when gosidian and the agent share a filesystem (local install, Docker volume mount, co-located deploy). For remote setups use 'data' or bridge_filename. Must be inside the vault, the bridge dir, or an allowed upload root (GOSIDIAN_MCP_ALLOWED_UPLOAD_ROOTS).")),
		mcp.WithString("filename", mcp.Description("Original filename for extension validation and link text. Required with data, optional with source_path/bridge_filename (defaults to basename).")),
		mcp.WithString("project", mcp.Description("Optional project to store the attachment in. Empty stores at vault root.")),
	), s.handleUploadAttachment)

	s.impl.AddTool(mcp.NewTool("memory_list_attachments",
		mcp.WithDescription("List attachments in the vault, optionally filtered by project. Returns path, size, MIME type, and modification time for each attachment."),
		mcp.WithString("project", mcp.Description("Optional project name to scope the listing.")),
	), s.handleListAttachments)

	s.impl.AddTool(mcp.NewTool("memory_delete_attachment",
		mcp.WithDescription("Delete an attachment file from the vault. The file is permanently removed. Does not update notes that reference it."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault-relative attachment path (e.g. 'project/attachments/abc123.png').")),
	), s.handleDeleteAttachment)

	s.impl.AddTool(mcp.NewTool("memory_attachment_info",
		mcp.WithDescription("Get metadata about an attachment, including which notes reference it. Use this to check if an attachment is orphaned before deleting."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault-relative attachment path.")),
	), s.handleAttachmentInfo)

	s.impl.AddTool(mcp.NewTool("memory_upload_resource",
		mcp.WithDescription("Upload a file resource decoupled from any note (returns the handle path/url/hash/mime/kind/size, no embed markdown) — the pre-uploader for the stage-then-attach pattern. CHEAPEST for large files: POST multipart (field 'file', bearer token) to the /upload endpoint (your /sse URL with /sse → /upload). ONE source: base64 `data`, `source_path`, or `bridge_filename`. Storage is <project>/attachments/<hash>.<ext>, magic-bytes verified; caps/extensions in bootstrap `capabilities`."),
		mcp.WithString("project", mcp.Required(), mcp.Description("Vault project to store the resource in. Required (resources without a project context are rare and intentionally not auto-routed).")),
		mcp.WithString("bridge_filename", mcp.Description("PREFERRED for images/binaries: the basename of a file you staged in the server's bridge dir (GOSIDIAN_MCP_BRIDGE_DIR). Read and consumed server-side — near-zero token cost.")),
		mcp.WithString("data", mcp.Description("Base64-encoded file content. Costly for large files (~1 token/char) — prefer bridge_filename/source_path. Required only when neither of those is used.")),
		mcp.WithString("source_path", mcp.Description("Absolute filesystem path to the file — RESOLVED ON THE SERVER, not the client. Use when gosidian and the agent share a filesystem. For remote setups use 'data' or bridge_filename. Must be inside the vault, the bridge dir, or an allowed upload root (GOSIDIAN_MCP_ALLOWED_UPLOAD_ROOTS).")),
		mcp.WithString("filename", mcp.Description("Original filename for extension validation. Required with data, optional with source_path/bridge_filename (defaults to basename).")),
		mcp.WithString("kind", mcp.Description("Hint for caller branching: image | document | auto (default). Echoed back in the response — server resolves auto from the validated MIME family.")),
	), s.handleUploadResource)
}

// storeAttachmentFromRequest validates and stores an uploaded file from one of
// `bridge_filename` (a file staged in the bridge dir), a server-side
// `source_path`, or base64 `data`, applying the same auth and size checks as
// the upload tools. Returns the stored result plus its byte size, or a
// ready-to-return tool error result. Shared by the two upload tools and by
// memory_create_media_note so the validation logic lives in one place.
func (s *Server) storeAttachmentFromRequest(ctx context.Context, project, filename, dataB64, sourcePath, bridgeFilename string) (*attach.Result, int64, *mcp.CallToolResult) {
	// Bridge upload (IMP-059): the agent staged a file in the bridge dir and
	// passes only its basename. We resolve it under the bridge dir (an allowed
	// root), store it, then consume the staged copy. Near-zero token cost.
	if bridgeFilename != "" {
		if s.bridgeDir == "" {
			return nil, 0, mcp.NewToolResultError("bridge_filename given but no bridge dir is configured (set GOSIDIAN_MCP_BRIDGE_DIR)")
		}
		staged := filepath.Join(s.bridgeDir, filepath.Base(bridgeFilename))
		if filename == "" {
			filename = filepath.Base(bridgeFilename)
		}
		ext := strings.ToLower(filepath.Ext(filename))
		testRel := attach.RelPath(project, "test"+ext)
		if _, errRes := s.authorizeWrite(ctx, testRel); errRes != nil {
			return nil, 0, errRes
		}
		res, err := attach.StoreFromPath(s.vault, staged, filename, project, s.effectiveUploadRoots())
		if err != nil {
			return nil, 0, mcp.NewToolResultError(err.Error())
		}
		var size int64
		if abs, absErr := s.vault.Abs(res.Path); absErr == nil {
			if fi, stErr := os.Stat(abs); stErr == nil {
				size = fi.Size()
			}
		}
		_ = os.Remove(staged) // consume the staging copy (best-effort)
		return res, size, nil
	}

	if sourcePath == "" && dataB64 == "" {
		return nil, 0, mcp.NewToolResultError("provide one of: bridge_filename (staged file), source_path (server path), or data (base64). For large files avoid base64: POST the bytes to the HTTP /upload endpoint (your /sse URL with /sse→/upload, bearer token) or mint a single-use upload URL with memory_ingest transfer:\"http\"")
	}

	if sourcePath != "" {
		// Server-side filesystem read — no base64 through context.
		if filename == "" {
			filename = filepath.Base(sourcePath)
		}
		// Auth check: verify write scope on the target attachment path.
		ext := strings.ToLower(filepath.Ext(filename))
		testRel := attach.RelPath(project, "test"+ext)
		if _, errRes := s.authorizeWrite(ctx, testRel); errRes != nil {
			return nil, 0, errRes
		}
		res, err := attach.StoreFromPath(s.vault, sourcePath, filename, project, s.effectiveUploadRoots())
		if err != nil {
			return nil, 0, mcp.NewToolResultError(err.Error())
		}
		// For audit: stat the stored file to get size.
		var size int64
		if abs, absErr := s.vault.Abs(res.Path); absErr == nil {
			if fi, stErr := os.Stat(abs); stErr == nil {
				size = fi.Size()
			}
		}
		return res, size, nil
	}

	// Base64 path.
	if filename == "" {
		return nil, 0, mcp.NewToolResultError("filename is required when using data (base64)")
	}
	data, err := base64.StdEncoding.DecodeString(dataB64)
	if err != nil {
		return nil, 0, mcp.NewToolResultError("invalid base64: " + err.Error())
	}
	ext := strings.ToLower(filepath.Ext(filename))
	testRel := attach.RelPath(project, "test"+ext)
	tok, errRes := s.authorizeWrite(ctx, testRel)
	if errRes != nil {
		return nil, 0, errRes
	}
	if errRes := s.checkWriteLimits(tok, len(data)); errRes != nil {
		return nil, 0, errRes
	}
	res, err := attach.Store(s.vault, data, filename, project)
	if err != nil {
		return nil, 0, mcp.NewToolResultError(err.Error())
	}
	return res, int64(len(data)), nil
}

func (s *Server) handleUploadAttachment(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	dataB64 := req.GetString("data", "")
	res, dataSize, errRes := s.storeAttachmentFromRequest(
		ctx,
		req.GetString("project", ""),
		req.GetString("filename", ""),
		dataB64,
		req.GetString("source_path", ""),
		req.GetString("bridge_filename", ""),
	)
	if errRes != nil {
		return errRes, nil
	}
	s.auditWrite(ctx, audit.ActionUploadAttachment, res.Path, "", dataSize)
	out := map[string]any{
		"path":     res.Path,
		"markdown": res.Markdown,
	}
	if h := s.bridgeHint(dataB64); h != "" {
		out["hint"] = h
	}
	return mcp.NewToolResultJSON(out)
}

// handleUploadResource is the editor-decoupled twin of
// handleUploadAttachment. Same storage, same validation, same audit
// action — but the response intentionally drops the `markdown` field so
// the caller (typically an agent doing 2-step "upload all, verify,
// then attach") decides when and where to insert the embed.
func (s *Server) handleUploadResource(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	project, err := req.RequireString("project")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if strings.TrimSpace(project) == "" {
		return mcp.NewToolResultError("project must not be empty"), nil
	}

	sourcePath := req.GetString("source_path", "")
	dataB64 := req.GetString("data", "")
	filename := req.GetString("filename", "")
	kindHint := strings.ToLower(strings.TrimSpace(req.GetString("kind", "auto")))
	if kindHint == "" {
		kindHint = "auto"
	}
	if kindHint != "image" && kindHint != "document" && kindHint != "auto" {
		return mcp.NewToolResultError("kind must be one of: image, document, auto"), nil
	}

	res, dataSize, errRes := s.storeAttachmentFromRequest(ctx, project, filename, dataB64, sourcePath, req.GetString("bridge_filename", ""))
	if errRes != nil {
		return errRes, nil
	}

	s.auditWrite(ctx, audit.ActionUploadAttachment, res.Path, "", dataSize)

	// Resolve kind and MIME from the validated extension (always reliable
	// since attach.Store rejects unknown extensions before reaching here).
	ext := strings.ToLower(filepath.Ext(res.Filename))
	mime := "application/octet-stream"
	resolvedKind := "document"
	if info, ok := attach.AllowedExt[ext]; ok {
		mime = info.MIME
		if info.IsImage {
			resolvedKind = "image"
		}
	}

	out := map[string]any{
		"path":              res.Path,
		"url":               "/vault-files/" + res.Path,
		"mime":              mime,
		"kind":              resolvedKind,
		"size":              dataSize,
		"original_filename": filename,
		"hash":              strings.TrimSuffix(res.Filename, ext),
	}
	if h := s.bridgeHint(dataB64); h != "" {
		out["hint"] = h
	}
	return mcp.NewToolResultJSON(out)
}

// allowedExtSet builds a set of allowed extensions for vault.ListAttachments.
func allowedExtSet() map[string]bool {
	return attach.ExtSet()
}

func (s *Server) handleListAttachments(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	project, err := scopedProject(tok, req.GetString("project", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if project != "" {
		if res := s.rejectIfHidden(project); res != nil {
			return res, nil
		}
	}

	infos, err := s.vault.ListAttachments(project, allowedExtSet())
	if err != nil {
		return mcp.NewToolResultErrorFromErr("list attachments failed", err), nil
	}

	type attachEntry struct {
		Path  string `json:"path"`
		Size  int64  `json:"size"`
		MIME  string `json:"mime"`
		Mtime int64  `json:"mtime"`
	}
	out := make([]attachEntry, 0, len(infos))
	for _, info := range infos {
		if !tok.AllowsPath(info.Path) {
			continue
		}
		ext := strings.ToLower(filepath.Ext(info.Path))
		mime := "application/octet-stream"
		if ei, ok := attach.AllowedExt[ext]; ok {
			mime = ei.MIME
		}
		out = append(out, attachEntry{
			Path:  info.Path,
			Size:  info.Size,
			MIME:  mime,
			Mtime: info.ModTime.Unix(),
		})
	}
	return mcp.NewToolResultJSON(map[string]any{"attachments": out})
}

func (s *Server) handleDeleteAttachment(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	rel, err := s.vault.Rel(path)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("invalid path", err), nil
	}
	// Only allow deleting files inside attachments/ directories.
	if !strings.Contains("/"+rel, "/attachments/") {
		return mcp.NewToolResultError("path is not inside an attachments/ directory"), nil
	}
	if _, errRes := s.authorizeWrite(ctx, rel); errRes != nil {
		return errRes, nil
	}
	if !s.vault.Exists(rel) {
		return mcp.NewToolResultErrorf("attachment %q not found", rel), nil
	}
	if err := s.vault.DeleteAttachment(rel, allowedExtSet()); err != nil {
		return mcp.NewToolResultErrorFromErr("delete failed", err), nil
	}
	s.auditWrite(ctx, audit.ActionDeleteAttachment, rel, "", 0)
	return mcp.NewToolResultJSON(map[string]any{"deleted": true, "path": rel})
}

func (s *Server) handleAttachmentInfo(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	rel, err := s.vault.Rel(path)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("invalid path", err), nil
	}
	if !strings.Contains("/"+rel, "/attachments/") {
		return mcp.NewToolResultError("path is not inside an attachments/ directory"), nil
	}
	if !tok.AllowsPath(rel) {
		return mcp.NewToolResultErrorf("path %q is outside the token's scope", rel), nil
	}

	abs, err := s.vault.Abs(rel)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("resolve path", err), nil
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return mcp.NewToolResultErrorf("attachment %q not found", rel), nil
	}

	ext := strings.ToLower(filepath.Ext(rel))
	mime := "application/octet-stream"
	if ei, ok := attach.AllowedExt[ext]; ok {
		mime = ei.MIME
	}

	// Find notes that reference this attachment by searching the index for the
	// hashed filename (the unique part of the path).
	basename := filepath.Base(rel)
	var referencedBy []string
	if hits, err := s.index.Search(basename, 100); err == nil {
		for _, h := range hits {
			if !tok.AllowsPath(h.Path) {
				continue
			}
			// Verify the note actually contains a reference to this attachment
			// (not just a coincidental substring match on the hash).
			if note, loadErr := s.vault.Load(h.Path); loadErr == nil {
				if strings.Contains(string(note.Content), basename) {
					referencedBy = append(referencedBy, h.Path)
				}
			}
		}
	}

	return mcp.NewToolResultJSON(map[string]any{
		"path":          rel,
		"size":          fi.Size(),
		"mime":          mime,
		"mtime":         fi.ModTime().Unix(),
		"referenced_by": referencedBy,
	})
}
