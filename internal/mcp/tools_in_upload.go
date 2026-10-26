package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/infolder"
	"github.com/gosidian/gosidian/internal/vault"
	"github.com/mark3labs/mcp-go/mcp"
)

// The upload into a project's IN folder for agents (IMP-172 phase 5b): the
// tool opens the session, as the web UI and the REST API do, and gives a
// ticket; the agent sends the pieces from its shell to the in-upload
// endpoint with the ticket, never through the model's context.

// inUploadChannel is the way in of the agents, for the caps of [in.mcp].
const inUploadChannel = "mcp"

func (s *Server) registerINUploadTool() {
	s.impl.AddTool(mcp.NewTool("memory_in_upload",
		mcp.WithDescription("Upload files into a project's IN folder (the files its pipelines take in: PDFs, XML, scans, a corpus), of any size, in pieces, resumable. The bytes never go through this call: "+
			"action open lists the files (path under dir, size) and returns the upload with a ticket and its endpoint; then send each file from your shell, in pieces of at most 64 MiB (32 MiB is a good size), each at the offset the server has: "+
			"`curl -sf -X PUT -H \"Authorization: Bearer <ticket>\" -H 'Content-Type: application/octet-stream' --data-binary @piece \"<mcp-host><endpoint>/files/<n>?offset=<received>\"` (a 409 answer says the right `received`; a piece cut short keeps what arrived, and the same piece sent again is written past what the server has). "+
			"Then action complete (or POST <endpoint>/complete with the ticket): the files take their places in the background as one batch, a zip with unzip:true unpacked into its own folder; action status follows it to each file's result. "+
			"The same files opened again into the same folder are the same upload, with what arrived and a new ticket (the old one stops): the way to resume. A file already there is replaced (the old one into the trash), kept as `name (2).ext` or skipped (policy); the same file is left. "+
			"For many small files send one zip with unzip:true. Needs write access to the project and its IN folder (use_in)."),
		mcp.WithString("action", mcp.Required(), mcp.Description("open, status, complete, cancel, or list (the uploads of your account in the project)."), mcp.Enum("open", "status", "complete", "cancel", "list")),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project of the IN folder. "+scopedProjectNote)),
		mcp.WithString("id", mcp.Description("status, complete, cancel: the upload.")),
		mcp.WithString("dir", mcp.Description("open: the folder of the IN folder the files go to, made if missing; empty for its root.")),
		mcp.WithArray("files", mcp.Description("open: the files, at most 100000."),
			mcp.Items(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":     map[string]any{"type": "string", "description": "Relative to dir, with / between folders: no hidden, empty, . or .. segment."},
					"size":     map[string]any{"type": "integer", "description": "Bytes."},
					"unzip":    map[string]any{"type": "boolean", "description": "A .zip to unpack on the server."},
					"modified": map[string]any{"type": "integer", "description": "The time the file keeps, Unix milliseconds."},
				},
				"required": []string{"path", "size"},
			})),
		mcp.WithString("policy", mcp.Description("open, complete: replace, keep (default) or skip, for a file whose place is taken.")),
		mcp.WithArray("drop", mcp.Description("complete: the numbers (n) of files left out."), mcp.WithNumberItems()),
		mcp.WithBoolean("files_out", mcp.Description("status: true to list the files too, with what arrived of each and, once done, its result (default false).")),
	), s.handleINUpload)
}

// inUploadOwner is who an upload of a token is: its account, so that the
// account sees it in the IN window too, or the token itself.
func inUploadOwner(tok *auth.Token) string {
	if tok.OwnerUserID != "" {
		return tok.OwnerUserID
	}
	return "token:" + tok.ID
}

func (s *Server) handleINUpload(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	if s.in == nil {
		return mcp.NewToolResultError("the IN folders are not available on this server"), nil
	}
	project, err := s.resolveProject(tok, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if s.projects == nil || !s.projects.UsesIN(project) {
		return mcp.NewToolResultErrorf("the project %s has no IN folder: an admin of the project turns use_in on (the IN chip in the Projects window)", project), nil
	}
	if tok, errRes = s.authorizeWrite(ctx, vault.INRoot(project)); errRes != nil {
		return errRes, nil
	}
	owner := inUploadOwner(tok)
	id := strings.TrimSpace(req.GetString("id", ""))
	action := strings.TrimSpace(req.GetString("action", ""))
	switch action {
	case "list":
		list, err := s.in.Uploads(project, owner)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("uploads", err), nil
		}
		return mcp.NewToolResultJSON(map[string]any{"uploads": list})
	case "open":
		return s.openINUpload(ctx, tok, project, owner, req)
	case "status", "complete", "cancel":
		if id == "" {
			return mcp.NewToolResultErrorf("%s needs the upload's id", action), nil
		}
	default:
		return mcp.NewToolResultError("action is open, status, complete, cancel or list"), nil
	}
	switch action {
	case "status":
		u, err := s.in.GetUpload(project, id, owner, req.GetBool("files_out", false))
		if err != nil {
			return inUploadError(err), nil
		}
		return mcp.NewToolResultJSON(u)
	case "complete":
		if errRes := s.checkWriteLimits(ctx, tok, 0); errRes != nil {
			return errRes, nil
		}
		u, err := s.in.CompleteUpload(project, id, owner, strings.TrimSpace(req.GetString("policy", "")), req.GetIntSlice("drop", nil))
		if err != nil {
			return inUploadError(err), nil
		}
		return mcp.NewToolResultJSON(map[string]any{"upload": u, "hint": "completing in the background: action status until state is done, then each file's result is in files (files_out:true)"})
	default: // cancel
		if errRes := s.checkWriteLimits(ctx, tok, 0); errRes != nil {
			return errRes, nil
		}
		if err := s.in.CancelUpload(project, id, owner); err != nil {
			return inUploadError(err), nil
		}
		return mcp.NewToolResultJSON(map[string]any{"id": id, "dropped": true})
	}
}

func (s *Server) openINUpload(ctx context.Context, tok *auth.Token, project, owner string, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	raw, err := json.Marshal(req.GetArguments()["files"])
	if err != nil {
		return mcp.NewToolResultError("files is a list of {path, size, unzip, modified}"), nil
	}
	var files []infolder.UploadFile
	if err := json.Unmarshal(raw, &files); err != nil || len(files) == 0 {
		return mcp.NewToolResultError("files is a list of {path, size, unzip, modified}, at least one"), nil
	}
	for i := range files {
		// What the server says of a file is not the caller's to say.
		files[i] = infolder.UploadFile{Path: files[i].Path, Size: files[i].Size, Unzip: files[i].Unzip, Modified: files[i].Modified}
	}
	if errRes := s.checkWriteLimits(ctx, tok, 0); errRes != nil {
		return errRes, nil
	}
	dir := strings.Trim(strings.TrimPrefix(strings.Trim(strings.TrimSpace(req.GetString("dir", "")), "/"), vault.INFolder), "/")
	u, err := s.in.OpenUpload(infolder.UploadRequest{Project: project, Dir: dir, Policy: strings.TrimSpace(req.GetString("policy", "")),
		Owner: tok.Name, OwnerID: owner, Channel: inUploadChannel, Files: files})
	if err != nil {
		return inUploadError(err), nil
	}
	ticket, err := s.in.MintUploadTicket(project, u.ID, owner, tok.ID)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("ticket", err), nil
	}
	endpoint := basePathFromContext(ctx) + "/in-upload/" + u.ID
	s.auditWrite(ctx, audit.ActionINUploadOpen, vault.INRoot(project)+strings.TrimSuffix("/"+u.Dir, "/"), u.ID, u.Bytes)
	return mcp.NewToolResultJSON(map[string]any{
		"upload":    u,
		"ticket":    ticket,
		"endpoint":  endpoint,
		"max_piece": infolder.MaxChunk,
		"hint": fmt.Sprintf("Send each file whose received is below its size, in pieces of at most %d bytes, to the SAME host as your MCP URL with the ticket as the bearer (not your MCP token): "+
			"curl -sf -X PUT -H \"Authorization: Bearer $TICKET\" -H 'Content-Type: application/octet-stream' --data-binary @piece \"<mcp-host>%s/files/<n>?offset=<received>\" — the answer says the new received. "+
			"Cut a piece with: tail -c +$((received + 1)) FILE | head -c 33554432. Then action complete. The ticket lasts as long as the upload; open again for a new one.", infolder.MaxChunk, endpoint),
	})
}

// inUploadError is a tool error for an error of the uploads.
func inUploadError(err error) *mcp.CallToolResult {
	var inc *infolder.IncompleteError
	var off *infolder.OffsetError
	switch {
	case errors.As(err, &inc):
		return mcp.NewToolResultErrorf("%v: files %v", err, inc.Files)
	case errors.As(err, &off):
		return mcp.NewToolResultErrorf("%v", err)
	case errors.Is(err, infolder.ErrNoSpace):
		return mcp.NewToolResultErrorf("%v: the server keeps that disk free ([in] reserve_bytes); free some, or ask its admin", err)
	case errors.Is(err, infolder.ErrTooLarge):
		return mcp.NewToolResultErrorf("%v ([in] and [in.mcp] of the server)", err)
	}
	return mcp.NewToolResultError(err.Error())
}
