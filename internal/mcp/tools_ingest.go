package mcp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/audit"
	"github.com/mark3labs/mcp-go/mcp"
)

// registerIngestTool adds memory_ingest, the single front door for saving
// files into the vault (ADR-018). It routes by extension to the existing
// table/media/attachment paths and adds the missing one: ingesting .md/.html
// note bodies from a server-side file instead of through the context.
// Called from registerTools().
func (s *Server) registerIngestTool() {
	s.impl.AddTool(mcp.NewTool("memory_ingest",
		mcp.WithDescription("Save a file into the vault with automatic routing — the ONE tool for \"store this file/report\". Provide the file ONE way, cheapest first: `bridge_filename` (staged in the server's bridge dir), `source_path` (server-side path inside an allowed upload root), `url` (the server fetches it — allowlisted prefixes only), `attachment` (vault path of a file already uploaded), or base64 `data` (small files only). Remote agents with a shell: pass `transfer:\"http\"` instead of a source to mint a single-use upload URL, then POST the bytes there (no bearer needed). Routing by extension: .csv → table note, image → media note, .md/.html → the note itself (body read server-side, no tokens through the context), anything else → plain attachment. Force a kind with `as`."),
		mcp.WithString("project", mcp.Required(), mcp.Description("Vault project to store into.")),
		mcp.WithString("bridge_filename", mcp.Description("Basename of a file you staged in the server's bridge dir (GOSIDIAN_MCP_BRIDGE_DIR). Read and consumed server-side — near-zero token cost (co-located deploys).")),
		mcp.WithString("source_path", mcp.Description("Absolute server-side filesystem path. Must be inside the vault, the bridge dir, or an allowed upload root (GOSIDIAN_MCP_ALLOWED_UPLOAD_ROOTS).")),
		mcp.WithString("url", mcp.Description("http(s) URL the SERVER fetches the file from (CI artifacts, internal screenshot services). Gated by the GOSIDIAN_INGEST_URL_ALLOWLIST prefix allowlist; disabled when unset.")),
		mcp.WithString("attachment", mcp.Description("Vault-relative path of an already-uploaded attachment to promote into a table/media note without re-uploading.")),
		mcp.WithString("data", mcp.Description("Base64-encoded content. Costly for large files (~1 token/char) — prefer bridge_filename/source_path. Requires filename.")),
		mcp.WithString("transfer", mcp.Description("Pass \"http\" (with NO source) to mint a single-use upload ticket: the response carries the endpoint to POST the file to (multipart, field 'file', no Authorization header — the ticket is the credential, TTL ~5 min). On receipt the server executes this call's intent (as/note_path/title/caption/overwrite).")),
		mcp.WithString("filename", mcp.Description("Original filename for extension detection/validation. Required with data, optional otherwise (defaults to the source basename or the uploaded file's name).")),
		mcp.WithString("as", mcp.Description("Force the result kind instead of routing by extension: auto (default) | table | media | note | attachment.")),
		mcp.WithString("note_path", mcp.Description("Explicit vault-relative note path for the table/media/note result (e.g. 'proj/report-2026-07.md'). Derived from the filename/title when omitted.")),
		mcp.WithString("title", mcp.Description("Table/media notes: note title. Defaults to the filename stem.")),
		mcp.WithString("caption", mcp.Description("Table/media notes: markdown body describing the content — the searchable text. Strongly recommended.")),
		mcp.WithBoolean("overwrite", mcp.Description("Note kind only: replace the note when it already exists. Default false (fail on existing).")),
		mcp.WithString("if_match", mcp.Description("Note kind only, with overwrite: etag from a previous memory_get — the replace fails if the note changed since you read it.")),
	), s.handleIngest)
}

// ingestKind is the routing outcome of resolveIngestKind.
type ingestKind string

const (
	ingestTable      ingestKind = "table"
	ingestMedia      ingestKind = "media"
	ingestNote       ingestKind = "note"
	ingestAttachment ingestKind = "attachment"
)

// resolveIngestKind maps (as, ext) to the ingestion kind. In auto mode a
// table/media route whose vault flag is off degrades to a plain attachment
// (the extension is still attachable) and returns a warning explaining the
// degradation; an explicit `as` never degrades — the delegated handler
// rejects with its own teaching error.
func (s *Server) resolveIngestKind(as, ext string) (ingestKind, string, *mcp.CallToolResult) {
	isImage := false
	if info, ok := attach.AllowedExt[ext]; ok {
		isImage = info.IsImage
	}
	isNoteExt := ext == ".md" || ext == ".html"

	switch as {
	case "", "auto":
		switch {
		case ext == ".csv":
			if s.vault.TableNotesEnabled() {
				return ingestTable, "", nil
			}
			return ingestAttachment, "table_notes is disabled on this instance, so the CSV was stored as a plain attachment. An admin can enable [vault] table_notes (project toggle-UI or GOSIDIAN_VAULT_TABLE_NOTES) to get paginated table notes.", nil
		case isImage:
			if s.vault.MediaNotesEnabled() {
				return ingestMedia, "", nil
			}
			return ingestAttachment, "media_notes is disabled on this instance, so the image was stored as a plain attachment. An admin can enable [vault] media_notes (project toggle-UI or GOSIDIAN_VAULT_MEDIA_NOTES) to get first-class media notes.", nil
		case isNoteExt:
			return ingestNote, "", nil
		case ext == "":
			return "", "", mcp.NewToolResultError("cannot route: the source has no file extension (pass filename with an extension, or force a kind with as)")
		default:
			return ingestAttachment, "", nil
		}
	case "table":
		if ext != ".csv" {
			return "", "", mcp.NewToolResultErrorf("as:table requires a .csv source; got %q", ext)
		}
		return ingestTable, "", nil
	case "media":
		if !isImage {
			return "", "", mcp.NewToolResultErrorf("as:media requires an image source (png/jpg/jpeg/gif/webp/svg); got %q", ext)
		}
		return ingestMedia, "", nil
	case "note":
		if !isNoteExt {
			return "", "", mcp.NewToolResultErrorf("as:note requires a .md or .html source; got %q", ext)
		}
		return ingestNote, "", nil
	case "attachment":
		if isNoteExt {
			return "", "", mcp.NewToolResultErrorf("%q cannot be stored as a plain attachment; use as:note (or omit as)", ext)
		}
		return ingestAttachment, "", nil
	default:
		return "", "", mcp.NewToolResultError("as must be one of: auto, table, media, note, attachment")
	}
}

func (s *Server) handleIngest(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	project, err := req.RequireString("project")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	project = strings.TrimSpace(project)
	if project == "" {
		return mcp.NewToolResultError("project must not be empty"), nil
	}

	dataB64 := req.GetString("data", "")
	sourcePath := strings.TrimSpace(req.GetString("source_path", ""))
	bridgeFilename := strings.TrimSpace(req.GetString("bridge_filename", ""))
	attachment := strings.TrimSpace(req.GetString("attachment", ""))
	filename := strings.TrimSpace(req.GetString("filename", ""))
	as := strings.ToLower(strings.TrimSpace(req.GetString("as", "auto")))
	urlSrc := strings.TrimSpace(req.GetString("url", ""))

	switch transfer := strings.ToLower(strings.TrimSpace(req.GetString("transfer", ""))); transfer {
	case "":
	case "http":
		if dataB64 != "" || sourcePath != "" || bridgeFilename != "" || attachment != "" || urlSrc != "" {
			return mcp.NewToolResultError("transfer:http mints a ticket for bytes POSTed later — do not pass a source in the same call"), nil
		}
		return s.mintIngestTicket(ctx, project, as, req)
	default:
		return mcp.NewToolResultError("transfer must be \"http\" (mint a single-use upload ticket) or omitted"), nil
	}

	if urlSrc != "" {
		if dataB64 != "" || sourcePath != "" || bridgeFilename != "" || attachment != "" {
			return mcp.NewToolResultError("provide ONE source: url cannot be combined with data/source_path/bridge_filename/attachment"), nil
		}
		// Cheap authorization before the server spends a fetch on it.
		if _, errRes := s.authorizeWrite(ctx, project+"/ingest-probe.md"); errRes != nil {
			return errRes, nil
		}
		data, fetchedName, err := s.fetchIngestURL(urlSrc)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		name := filename
		if name == "" {
			name = fetchedName
		}
		if name == "" {
			return mcp.NewToolResultError("cannot determine a filename from the URL; pass filename with an extension"), nil
		}
		return s.ingestRaw(ctx, ingestIntent{
			Project:   project,
			As:        as,
			NotePath:  strings.TrimSpace(req.GetString("note_path", "")),
			Title:     strings.TrimSpace(req.GetString("title", "")),
			Caption:   req.GetString("caption", ""),
			Filename:  name,
			Overwrite: req.GetBool("overwrite", false),
			IfMatch:   req.GetString("if_match", ""),
		}, data)
	}

	// Extension resolution mirrors the media/table handlers: explicit filename
	// first, then the basename of whichever source is present.
	fnForExt := filename
	for _, cand := range []string{sourcePath, bridgeFilename, attachment} {
		if fnForExt == "" && cand != "" {
			fnForExt = filepath.Base(cand)
		}
	}
	if fnForExt == "" {
		return mcp.NewToolResultError("provide the file ONE way: bridge_filename (staged in the bridge dir), source_path (server path), attachment (already-uploaded vault path), or data (base64, with filename)"), nil
	}
	ext := strings.ToLower(filepath.Ext(fnForExt))

	kind, warning, errRes := s.resolveIngestKind(as, ext)
	if errRes != nil {
		return errRes, nil
	}

	switch kind {
	case ingestTable, ingestMedia:
		// Delegate to the existing one-call creators: same validation, same
		// flags, same audit — memory_ingest is a router, not a rewrite.
		args := map[string]any{"project": project}
		for k, v := range map[string]string{
			"data":            dataB64,
			"source_path":     sourcePath,
			"bridge_filename": bridgeFilename,
			"attachment":      attachment,
			"filename":        filename,
			"title":           strings.TrimSpace(req.GetString("title", "")),
			"caption":         req.GetString("caption", ""),
			"path":            strings.TrimSpace(req.GetString("note_path", "")),
		} {
			if v != "" {
				args[k] = v
			}
		}
		delegated := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}}
		if kind == ingestTable {
			return s.handleCreateTableNote(ctx, delegated)
		}
		return s.handleCreateMediaNote(ctx, delegated)

	case ingestNote:
		return s.ingestNoteFromSource(ctx, project, ext, fnForExt, req)

	default: // ingestAttachment
		if attachment != "" {
			return mcp.NewToolResultError("the source is already an attachment; nothing to ingest (pass attachment only to promote it into a table/media note)"), nil
		}
		res, size, errRes := s.storeAttachmentFromRequest(ctx, project, filename, dataB64, sourcePath, bridgeFilename)
		if errRes != nil {
			return errRes, nil
		}
		s.auditWrite(ctx, audit.ActionUploadAttachment, res.Path, "", size)
		out := map[string]any{
			"path":     res.Path,
			"kind":     string(ingestAttachment),
			"markdown": res.Markdown,
			"size":     size,
		}
		if warning != "" {
			out["warning"] = warning
		}
		if h := s.bridgeHint(dataB64); h != "" {
			out["hint"] = h
		}
		return mcp.NewToolResultJSON(out)
	}
}

// ingestNoteFromSource writes a .md/.html note whose body comes from a
// server-side file (bridge dir or allowed source_path) or base64 data — the
// write-side twin of the memory_get read guard: large bodies no longer need
// to travel through the model context (IMP-072, absorbed by ADR-018).
func (s *Server) ingestNoteFromSource(ctx context.Context, project, ext, fnForExt string, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if strings.TrimSpace(req.GetString("attachment", "")) != "" {
		return mcp.NewToolResultError("a note cannot be created from an attachment path (.md/.html are not attachable); provide bridge_filename, source_path, or data"), nil
	}

	// Acquire the body server-side.
	var content []byte
	var consumeStaged string
	var err error
	dataB64 := req.GetString("data", "")
	sourcePath := strings.TrimSpace(req.GetString("source_path", ""))
	bridgeFilename := strings.TrimSpace(req.GetString("bridge_filename", ""))
	switch {
	case bridgeFilename != "":
		if s.bridgeDir == "" {
			return mcp.NewToolResultError("bridge_filename given but no bridge dir is configured (set GOSIDIAN_MCP_BRIDGE_DIR)"), nil
		}
		staged := filepath.Join(s.bridgeDir, filepath.Base(bridgeFilename))
		content, err = os.ReadFile(staged)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return mcp.NewToolResultErrorf("bridge file %q not found in the bridge dir", filepath.Base(bridgeFilename)), nil
			}
			return mcp.NewToolResultErrorFromErr("read bridge file", err), nil
		}
		consumeStaged = staged
	case sourcePath != "":
		if err := attach.ValidateSourcePath(sourcePath, s.effectiveUploadRoots()); err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		content, err = os.ReadFile(filepath.Clean(sourcePath))
		if err != nil {
			return mcp.NewToolResultErrorFromErr("read source file", err), nil
		}
	case dataB64 != "":
		content, err = base64.StdEncoding.DecodeString(dataB64)
		if err != nil {
			return mcp.NewToolResultError("invalid base64: " + err.Error()), nil
		}
	default:
		return mcp.NewToolResultError("provide the note body via bridge_filename, source_path, or data (base64)"), nil
	}

	hint := ""
	if dataB64 != "" && len(content) > getBodySoftCap {
		hint = fmt.Sprintf("this note body traveled base64 through the context (~%d KiB ≈ that many tokens). Cheaper next time: write the file to disk and stage it in the bridge dir (bridge_filename), or use source_path inside an allowed upload root.", len(content)>>10)
	}

	res, errOut := s.writeIngestedNote(ctx, project, ext, fnForExt,
		strings.TrimSpace(req.GetString("note_path", "")), content,
		req.GetBool("overwrite", false), req.GetString("if_match", ""), hint)
	if errOut == nil && res != nil && !res.IsError && consumeStaged != "" {
		_ = os.Remove(consumeStaged) // consume the staging copy (best-effort)
	}
	return res, errOut
}

// writeIngestedNote is the note-kind write core shared by the MCP sources
// (ingestNoteFromSource) and the raw-bytes entrypoints (url fetch, ticket
// redemption): path resolution, flag/authz/limit checks, create-vs-overwrite
// with CAS, synchronous index, audit and event publish.
func (s *Server) writeIngestedNote(ctx context.Context, project, ext, fnForExt, notePath string, content []byte, overwrite bool, ifMatch, hint string) (*mcp.CallToolResult, error) {
	if ext == ".html" && !s.vault.HTMLNotesEnabled() {
		return mcp.NewToolResultError("html notes are disabled on this instance — a per-project flag an admin can flip live from the web UI project toggles (or [vault] html_notes / GOSIDIAN_VAULT_HTML_NOTES)"), nil
	}
	if notePath == "" {
		notePath = project + "/" + filepath.Base(fnForExt)
	}
	switch destExt := strings.ToLower(filepath.Ext(notePath)); destExt {
	case ext:
		// already consistent
	case ".md", ".html":
		return mcp.NewToolResultErrorf("note_path extension %q does not match the source extension %q", destExt, ext), nil
	default:
		notePath += ext
	}
	rel, err := s.vault.Rel(notePath)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("invalid note path", err), nil
	}
	tok, errRes := s.authorizeWrite(ctx, rel)
	if errRes != nil {
		return errRes, nil
	}
	if errRes := s.checkWriteLimits(tok, len(content)); errRes != nil {
		return errRes, nil
	}

	unlock := s.vault.LockPath(rel)
	defer unlock()
	created := true
	if existing, loadErr := s.vault.Load(rel); loadErr == nil {
		if !overwrite {
			return mcp.NewToolResultErrorf("note %q already exists; pass overwrite:true to replace it (optionally with if_match for a safe concurrent replace)", rel), nil
		}
		if errRes := checkIfMatch(existing, ifMatch); errRes != nil {
			return errRes, nil
		}
		created = false
	} else if ifMatch != "" {
		return mcp.NewToolResultErrorf("etag mismatch: note %q does not exist", rel), nil
	}

	if err := s.writeAndIndex(rel, content); err != nil {
		return mcp.NewToolResultErrorFromErr("write failed", err), nil
	}
	action := audit.ActionCreate
	event := "create"
	if !created {
		action = audit.ActionUpdate
		event = "update"
	}
	s.auditWrite(ctx, action, rel, "", int64(len(content)))
	out := map[string]any{
		"path":    rel,
		"kind":    string(ingestNote),
		"size":    len(content),
		"created": created,
	}
	freshETag := ""
	if fresh, err := s.vault.Load(rel); err == nil {
		freshETag = fresh.ETag()
		out["etag"] = freshETag
	}
	s.publishNoteChange(event, rel, freshETag, created)
	if hint != "" {
		out["hint"] = hint
	}
	return mcp.NewToolResultJSON(out)
}

// ingestIntent is a routed ingestion request whose bytes arrive out of band
// (url fetch, ticket redemption) instead of through an MCP source parameter.
type ingestIntent struct {
	Project   string
	As        string
	NotePath  string
	Title     string
	Caption   string
	Filename  string
	Overwrite bool
	IfMatch   string
}

// ingestRaw executes an ingestion whose bytes are already in hand server-side:
// the same extension routing as handleIngest, minus the transport. Table/media
// results store the bytes as an attachment first and then delegate note
// creation with `attachment`, so validation, flags, and audit stay identical
// to the MCP path.
func (s *Server) ingestRaw(ctx context.Context, in ingestIntent, data []byte) (*mcp.CallToolResult, error) {
	if strings.TrimSpace(in.Filename) == "" {
		return mcp.NewToolResultError("cannot route: missing filename (pass filename with an extension)"), nil
	}
	ext := strings.ToLower(filepath.Ext(in.Filename))
	kind, warning, errRes := s.resolveIngestKind(in.As, ext)
	if errRes != nil {
		return errRes, nil
	}

	if kind == ingestNote {
		return s.writeIngestedNote(ctx, in.Project, ext, in.Filename, in.NotePath, data, in.Overwrite, in.IfMatch, "")
	}

	// Every other kind starts with the bytes stored as an attachment
	// (extension allowlist, magic-bytes MIME check, 10 MiB cap).
	tok, errRes := s.authorizeWrite(ctx, attach.RelPath(in.Project, "probe"+ext))
	if errRes != nil {
		return errRes, nil
	}
	if errRes := s.checkWriteLimits(tok, len(data)); errRes != nil {
		return errRes, nil
	}
	res, err := attach.Store(s.vault, data, in.Filename, in.Project)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	s.auditWrite(ctx, audit.ActionUploadAttachment, res.Path, "", int64(len(data)))

	switch kind {
	case ingestTable, ingestMedia:
		args := map[string]any{"project": in.Project, "attachment": res.Path, "filename": in.Filename}
		for k, v := range map[string]string{
			"title":   in.Title,
			"caption": in.Caption,
			"path":    in.NotePath,
		} {
			if v != "" {
				args[k] = v
			}
		}
		delegated := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}}
		if kind == ingestTable {
			return s.handleCreateTableNote(ctx, delegated)
		}
		return s.handleCreateMediaNote(ctx, delegated)
	default: // ingestAttachment
		out := map[string]any{
			"path":     res.Path,
			"kind":     string(ingestAttachment),
			"markdown": res.Markdown,
			"size":     len(data),
		}
		if warning != "" {
			out["warning"] = warning
		}
		return mcp.NewToolResultJSON(out)
	}
}

// ingestFetchTimeout bounds a server-side url fetch end to end.
const ingestFetchTimeout = 20 * time.Second

// ingestPrefix is one parsed allowlist entry. Scheme, host and effective
// port are compared exactly; the path is compared on a segment boundary.
type ingestPrefix struct {
	scheme string
	host   string // lowercase hostname
	port   string // "" when it is the scheme default
	path   string // cleaned; "/" covers the whole host
}

// parseIngestPrefix validates one allowlist entry. Entries must be absolute
// http(s) URLs with a host and without userinfo, query or fragment.
func parseIngestPrefix(raw string) (ingestPrefix, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ingestPrefix{}, err
	}
	scheme := strings.ToLower(u.Scheme)
	switch {
	case scheme != "http" && scheme != "https":
		return ingestPrefix{}, fmt.Errorf("%q: scheme must be http or https", raw)
	case u.Hostname() == "":
		return ingestPrefix{}, fmt.Errorf("%q: missing host", raw)
	case u.User != nil:
		return ingestPrefix{}, fmt.Errorf("%q: userinfo is not allowed", raw)
	case u.RawQuery != "" || u.Fragment != "":
		return ingestPrefix{}, fmt.Errorf("%q: query and fragment are not allowed", raw)
	}
	return ingestPrefix{scheme: scheme, host: strings.ToLower(u.Hostname()), port: effectivePort(scheme, u.Port()), path: cleanURLPath(u.Path)}, nil
}

func effectivePort(scheme, port string) string {
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		return ""
	}
	return port
}

func cleanURLPath(p string) string {
	if p == "" {
		return "/"
	}
	return path.Clean("/" + p)
}

// ingestURLAllowed reports whether u is covered by the allowlist. The match
// is structural — parsed scheme, host, port and path — never a string
// prefix: "https://api.example.com@evil/x" and "https://api.example.com.evil/x"
// both start with the literal text "https://api.example.com" and both must
// be rejected. The request path is cleaned first so "/pub/../secret" cannot
// ride on a "/pub" entry.
func (s *Server) ingestURLAllowed(u string) bool {
	pu, err := url.Parse(u)
	if err != nil || pu.User != nil {
		return false
	}
	scheme := strings.ToLower(pu.Scheme)
	host := strings.ToLower(pu.Hostname())
	port := effectivePort(scheme, pu.Port())
	reqPath := cleanURLPath(pu.Path)
	for _, p := range s.ingestURLAllow {
		if p.scheme != scheme || p.host != host || p.port != port {
			continue
		}
		if p.path == "/" || reqPath == p.path || strings.HasPrefix(reqPath, p.path+"/") {
			return true
		}
	}
	return false
}

// fetchIngestURL downloads the memory_ingest `url` source. The prefix
// allowlist is the SSRF boundary: it gates the initial URL and every redirect
// hop, and there is no implicit default. Returns the bytes plus a filename
// suggestion taken from the final URL path.
func (s *Server) fetchIngestURL(rawURL string) ([]byte, string, error) {
	if len(s.ingestURLAllow) == 0 {
		return nil, "", errors.New("url ingestion is disabled; set GOSIDIAN_INGEST_URL_ALLOWLIST (comma-separated URL prefixes) to enable it")
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, "", fmt.Errorf("url must be absolute http(s): %q", rawURL)
	}
	if !s.ingestURLAllowed(rawURL) {
		return nil, "", fmt.Errorf("url %q is not inside the ingest URL allowlist", rawURL)
	}
	client := &http.Client{
		Timeout: ingestFetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if !s.ingestURLAllowed(req.URL.String()) {
				return fmt.Errorf("redirect to %q is outside the ingest URL allowlist", req.URL)
			}
			return nil
		},
	}
	resp, err := client.Get(rawURL)
	if err != nil {
		return nil, "", fmt.Errorf("fetch %q: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("fetch %q: HTTP %d", rawURL, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, attach.MaxBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("fetch %q: read body: %w", rawURL, err)
	}
	if len(data) > attach.MaxBytes {
		return nil, "", errors.New("file too large (max 10 MiB)")
	}
	name := path.Base(resp.Request.URL.Path)
	if name == "." || name == "/" {
		name = ""
	}
	return data, name, nil
}
