package mcp

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/audit"
	"github.com/mark3labs/mcp-go/mcp"
)

// registerTableTools adds the CSV table-note tool (ADR-016). Called from
// registerTools() alongside registerMediaTools().
func (s *Server) registerTableTools() {
	s.impl.AddTool(mcp.NewTool("memory_create_table_note",
		mcp.WithDescription("Create a CSV table note: upload (or reference) a CSV AND create the markdown note that points to it, in one call. The note is a normal .md (frontmatter `type: table` + `media:`), indexed/linked like any note; the web UI renders the CSV as a paginated table. Use it for long tabular data (audit reports, exports) instead of bloating a markdown body. Column headers + row count are inlined into the body for search; cell VALUES are not indexed — write a caption. Requires table_notes (see bootstrap `capabilities`). Provide the CSV ONE way, cheapest first: `attachment` (already-uploaded path), `bridge_filename`, `source_path`, or base64 `data` (last resort)."),
		mcp.WithString("project", mcp.Required(), mcp.Description("Vault project for both the note and the CSV attachment.")),
		mcp.WithString("caption", mcp.Description("Markdown body describing the table — what it contains, where it comes from. Strongly recommended: together with the auto-inlined column headers it is the only searchable text (cell values are not indexed).")),
		mcp.WithString("title", mcp.Description("Note title. Defaults to the CSV filename (without extension) when omitted.")),
		mcp.WithString("path", mcp.Description("Explicit vault-relative note path (e.g. 'proj/audit-2026-07.md'). When omitted, derived as '<project>/<slug(title|filename)>.md'. A missing .md extension is appended.")),
		mcp.WithString("attachment", mcp.Description("PREFERRED: vault-relative path of a CSV you ALREADY uploaded — via the HTTP upload endpoint (your MCP /sse URL with /sse->/upload) or memory_upload_resource. The note references it, no re-upload. Cheapest path: POST the bytes over HTTP, then pass the returned `path` here.")),
		mcp.WithString("bridge_filename", mcp.Description("The basename of a CSV you staged in the server's bridge dir (GOSIDIAN_MCP_BRIDGE_DIR). Read and consumed server-side — near-zero token cost (co-located deploys).")),
		mcp.WithString("data", mcp.Description("Base64-encoded CSV content. Costly for large files (~1 token/char) — prefer attachment/bridge_filename/source_path. Required only when none of those is used.")),
		mcp.WithString("source_path", mcp.Description("Absolute server-side filesystem path to the CSV. Use when gosidian and the agent share a filesystem; otherwise use data or bridge_filename. Must be inside the vault, the bridge dir, or an allowed upload root.")),
		mcp.WithString("filename", mcp.Description("Original CSV filename for extension validation. Required with data, optional with source_path/bridge_filename/attachment (defaults to basename).")),
	), s.handleCreateTableNote)
}

func (s *Server) handleCreateTableNote(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if !s.vault.TableNotesEnabled() {
		return mcp.NewToolResultError("table notes are disabled on this instance — a per-project flag an admin can flip live from the web UI project toggles (or [vault] table_notes / GOSIDIAN_VAULT_TABLE_NOTES). Meanwhile the CSV can still be stored as a plain attachment (memory_ingest or memory_upload_attachment)"), nil
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

	// Resolve the extension up front so a non-CSV is rejected before any
	// storage or note write happens.
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
		return mcp.NewToolResultError("provide a CSV via attachment (already-uploaded path), bridge_filename, source_path, or data (with filename)"), nil
	}
	ext := strings.ToLower(filepath.Ext(fnForExt))
	if ext != ".csv" {
		return mcp.NewToolResultErrorf("table notes support .csv only; got %q", ext), nil
	}
	mime, _, extErr := attach.ValidateExt(ext)
	if extErr != nil {
		return mcp.NewToolResultError(extErr.Error()), nil
	}

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

	// Obtain the CSV: reference an already-uploaded attachment when
	// `attachment` is set, otherwise upload it now.
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

	// Summarise the stored CSV (headers + row count) for the FTS body. An
	// unparsable file is rejected here, before the note write, so a table note
	// never points at garbage.
	abs, aErr := s.vault.Abs(res.Path)
	if aErr != nil {
		return mcp.NewToolResultErrorFromErr("resolve stored csv", aErr), nil
	}
	raw, rErr := os.ReadFile(abs)
	if rErr != nil {
		return mcp.NewToolResultErrorf("read stored csv %q: %v", res.Path, rErr), nil
	}
	cols, rows, csvErr := csvSummary(raw)
	if csvErr != nil {
		return mcp.NewToolResultErrorf("invalid CSV (attachment stored at %q): %v", res.Path, csvErr), nil
	}

	// Assemble and write the note.
	content := buildTableNote(title, res.Path, project, caption, cols, rows)
	if errRes := s.checkWriteLimits(tok, len(content)); errRes != nil {
		return errRes, nil
	}
	if err := s.writeAndIndex(rel, []byte(content)); err != nil {
		// The attachment is content-addressed (dedup by hash) and orphan GC is
		// tracked separately (IMP-033), so we leave it and surface its path so a
		// retry can reference it instead of re-uploading.
		return mcp.NewToolResultErrorf("note write failed (csv stored at %q): %v", res.Path, err), nil
	}
	s.auditWrite(ctx, audit.ActionCreate, rel, "", int64(len(content)))
	if fresh, err := s.vault.Load(rel); err == nil {
		s.publishNoteChange("create", rel, fresh.ETag(), true)
	} else {
		s.publishNoteChange("create", rel, "", true)
	}

	out := map[string]any{
		"path": rel,
		"kind": "table",
		"media": map[string]any{
			"path": res.Path,
			"url":  "/vault-files/" + res.Path,
			"mime": mime,
			"size": size,
		},
		"columns": cols,
		"rows":    rows,
	}
	if strings.TrimSpace(caption) == "" {
		out["warning"] = "empty caption: only the column headers are searchable (cell values are not indexed). Add a caption describing the table so an agent can retrieve it."
	}
	if h := s.bridgeHint(dataB64); h != "" {
		out["hint"] = h
	}
	return mcp.NewToolResultJSON(out)
}

// csvSummary parses raw CSV bytes and returns the header record plus the
// number of data rows (excluding the header). The delimiter is sniffed from
// the first line among comma / semicolon / tab, so European-locale exports
// work out of the box. Ragged rows are tolerated (FieldsPerRecord=-1); a
// structurally unparsable file is an error.
func csvSummary(raw []byte) (cols []string, rows int, err error) {
	firstLine := raw
	if i := bytes.IndexByte(raw, '\n'); i >= 0 {
		firstLine = raw[:i]
	}
	delim := ','
	best := bytes.Count(firstLine, []byte{','})
	if n := bytes.Count(firstLine, []byte{';'}); n > best {
		best, delim = n, ';'
	}
	if n := bytes.Count(firstLine, []byte{'\t'}); n > best {
		delim = '\t'
	}

	r := csv.NewReader(bytes.NewReader(raw))
	r.Comma = delim
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	header, err := r.Read()
	if err != nil {
		return nil, 0, fmt.Errorf("no header row: %w", err)
	}
	for {
		if _, err := r.Read(); err != nil {
			if err == io.EOF {
				return header, rows, nil
			}
			return nil, 0, err
		}
		rows++
	}
}

// buildTableNote assembles the markdown source of a CSV table note: the
// frontmatter (title, type:table, media pointer, project + type:table tags)
// followed by the caption and the auto-generated summary line — column
// headers + row count — which is what lands in FTS for this note.
func buildTableNote(title, mediaPath, project, caption string, cols []string, rows int) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %s\n", yamlQuote(title))
	b.WriteString("type: table\n")
	fmt.Fprintf(&b, "media: %s\n", mediaPath)
	fmt.Fprintf(&b, "tags: [%s, type:table]\n", project)
	b.WriteString("---\n\n")
	if c := strings.TrimSpace(caption); c != "" {
		b.WriteString(c)
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "Columns: %s\n", strings.Join(cols, ", "))
	fmt.Fprintf(&b, "Rows: %d\n", rows)
	return b.String()
}
