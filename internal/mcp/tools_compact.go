// Package mcp — memory_compact tool (v1.5, IMP-017).
//
// Compacts an append-only markdown log (hot.md, log.md, decisions.md, …) by
// preserving the last N entries and replacing the older ones with a
// caller-supplied summary. Entry boundary heuristic: a line matching
// `^## YYYY-MM-DD` starts a new entry. If the file contains no such lines
// the tool refuses to run (it can't safely decide where to cut). A dry-run
// mode reports what would change without touching the file.
package mcp

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/mark3labs/mcp-go/mcp"
)

// entryStartRe identifies the first line of each "## YYYY-MM-DD …" block.
// Deliberately anchored to match the log.md / hot.md conventions.
var entryStartRe = regexp.MustCompile(`(?m)^## \d{4}-\d{2}-\d{2}`)

// registerCompactTool adds memory_compact.
func (s *Server) registerCompactTool() {
	s.impl.AddTool(mcp.NewTool("memory_compact",
		mcp.WithDescription("Compact an append-only log-shaped note by keeping the last N entries and replacing the older ones with a caller-provided summary. Entries are delimited by `## YYYY-MM-DD …` headings (log.md / hot.md convention). The tool is intentionally dumb about the summary content — pass whatever text best represents the archived section. Use dry_run=true first to preview the new size and entry counts."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault-relative path of the note to compact.")),
		mcp.WithNumber("keep_last_n", mcp.Required(), mcp.Description("Number of most-recent entries to preserve verbatim. Older entries are replaced by archive_summary.")),
		mcp.WithString("archive_summary", mcp.Required(), mcp.Description("Text to place in lieu of the archived entries. Typically a bullet-list digest or a reference to an external archive file.")),
		mcp.WithBoolean("dry_run", mcp.Description("When true, returns the compaction plan without writing. Default false.")),
	), s.handleCompact)
}

type compactResult struct {
	Path             string `json:"path"`
	OriginalEntries  int    `json:"original_entries"`
	KeptEntries      int    `json:"kept_entries"`
	ArchivedEntries  int    `json:"archived_entries"`
	OriginalBytes    int    `json:"original_bytes"`
	NewBytes         int    `json:"new_bytes"`
	DryRun           bool   `json:"dry_run"`
	ETag             string `json:"etag,omitempty"`
	Noop             bool   `json:"noop,omitempty"`
}

func (s *Server) handleCompact(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	rel, err := s.vault.Rel(path)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("invalid path", err), nil
	}
	tok, errRes := s.authorizeWrite(ctx, rel)
	if errRes != nil {
		return errRes, nil
	}
	keep := req.GetInt("keep_last_n", -1)
	if keep < 0 {
		return mcp.NewToolResultError("keep_last_n must be >= 0"), nil
	}
	summary, err := req.RequireString("archive_summary")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	dryRun := req.GetBool("dry_run", false)

	note, err := s.vault.Load(rel)
	if err != nil {
		return mcp.NewToolResultErrorf("cannot read %q: %v", rel, err), nil
	}
	body := note.Content
	starts := entryStartRe.FindAllIndex(body, -1)
	if len(starts) == 0 {
		return mcp.NewToolResultError("no `## YYYY-MM-DD` entries found; file shape not compatible with memory_compact"), nil
	}
	original := len(starts)
	if keep >= original {
		// Nothing to do: file already shorter than or equal to the target.
		return mcp.NewToolResultJSON(compactResult{
			Path:            rel,
			OriginalEntries: original,
			KeptEntries:     original,
			ArchivedEntries: 0,
			OriginalBytes:   len(body),
			NewBytes:        len(body),
			DryRun:          dryRun,
			ETag:            note.ETag(),
			Noop:            true,
		})
	}
	// The "header" of the file is everything before the first entry's heading
	// (frontmatter, intro, top title). The "kept" section is everything from
	// the first entry to archive plus `keep` onwards.
	headerEnd := starts[0][0]
	keepStart := starts[original-keep][0]
	header := body[:headerEnd]
	keepSuffix := body[keepStart:]

	newBody := renderCompactedHead(header, summary, original-keep, time.Now().UTC()) + string(keepSuffix)

	if errRes := s.checkWriteLimits(tok, len(newBody)); errRes != nil {
		return errRes, nil
	}

	if dryRun {
		return mcp.NewToolResultJSON(compactResult{
			Path:            rel,
			OriginalEntries: original,
			KeptEntries:     keep,
			ArchivedEntries: original - keep,
			OriginalBytes:   len(body),
			NewBytes:        len(newBody),
			DryRun:          true,
			ETag:            note.ETag(),
		})
	}

	if err := s.writeAndIndex(rel, []byte(newBody)); err != nil {
		return mcp.NewToolResultErrorFromErr("compact write failed", err), nil
	}
	s.auditWrite(ctx, audit.ActionUpdate, rel, "", int64(len(newBody)))

	// Re-load to fetch the new etag.
	newNote, _ := s.vault.Load(rel)
	etag := ""
	if newNote != nil {
		etag = newNote.ETag()
	}
	return mcp.NewToolResultJSON(compactResult{
		Path:            rel,
		OriginalEntries: original,
		KeptEntries:     keep,
		ArchivedEntries: original - keep,
		OriginalBytes:   len(body),
		NewBytes:        len(newBody),
		DryRun:          false,
		ETag:            etag,
	})
}

// renderCompactedHead rebuilds the head of the file: whatever came before the
// first archived entry (frontmatter + heading + introduction) followed by
// the archive marker + the caller-supplied summary, then a blank line.
func renderCompactedHead(prefix []byte, summary string, archivedCount int, now time.Time) string {
	var sb strings.Builder
	// Preserve the original header (frontmatter + any preamble) untouched —
	// the cut point is the first archived entry's heading.
	sb.Write(prefix)
	if len(prefix) > 0 && prefix[len(prefix)-1] != '\n' {
		sb.WriteString("\n")
	}
	fmt.Fprintf(&sb, "\n<!-- archived %d entr%s before %s -->\n\n",
		archivedCount, pluralY(archivedCount), now.Format("2006-01-02"))
	sb.WriteString(strings.TrimSpace(summary))
	sb.WriteString("\n\n")
	return sb.String()
}

func pluralY(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
