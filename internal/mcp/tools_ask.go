// Package mcp — memory_ask tool (v1.9).
//
// Structured "ask the human a question" primitive. Appends to
// <project>/docs/open-questions.md with auto-incremented OQ-NNN id,
// creating the file from a minimal template on first use.
//
// Rationale: CLAUDE.md mandates capturing open questions in the docs tree
// ("regola della cattura immediata"), but the ergonomics of doing so by
// hand (read the file → scan for next OQ-NNN → format the block →
// memory_append) are friction enough that the step gets skipped. This
// tool makes it a single call.
package mcp

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/mark3labs/mcp-go/mcp"
)

// registerAskTool wires memory_ask into the MCP surface.
func (s *Server) registerAskTool() {
	s.impl.AddTool(mcp.NewTool("memory_ask",
		mcp.WithDescription("Record a structured open question for human review. Appends a new `### OQ-NNN — <summary>` block to <project>/docs/open-questions.md in the 'Aperte' section, creating the file from a minimal template on first use. The id is auto-incremented based on the highest existing OQ-NNN in the file. Returns the final path, the assigned OQ id, and the new note etag."),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project (top-level folder). Scoped tokens are forced to their project.")),
		mcp.WithString("question", mcp.Required(), mcp.Description("The question to record, as a full sentence. The first ~80 chars are used as the block heading summary.")),
		mcp.WithString("urgency", mcp.Description("Urgency hint (low|medium|high). Default medium.")),
		mcp.WithString("context", mcp.Description("Optional context/background: why you're asking, what you've already ruled out, impact. Free text.")),
	), s.handleAsk)
}

type askResult struct {
	Path  string `json:"path"`
	OQID  string `json:"oq_id"`
	ETag  string `json:"etag,omitempty"`
	Index int    `json:"index"`
}

var oqHeadingRe = regexp.MustCompile(`(?m)^###\s+OQ-(\d{3,})\b`)

func (s *Server) handleAsk(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok := s.tokenFromContext(ctx)
	if tok == nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	project, err := s.resolveProject(tok, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	question, err := req.RequireString("question")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	question = strings.TrimSpace(question)
	if question == "" {
		return mcp.NewToolResultError("question must not be empty"), nil
	}
	urgency := strings.TrimSpace(req.GetString("urgency", "medium"))
	switch urgency {
	case "low", "medium", "high":
		// ok
	default:
		return mcp.NewToolResultErrorf("unknown urgency %q (expected low, medium, high)", urgency), nil
	}
	qContext := strings.TrimSpace(req.GetString("context", ""))

	path := project + "/docs/open-questions.md"
	rel, err := s.vault.Rel(path)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("invalid path", err), nil
	}
	// Write authorisation on the target file path (same as a memory_append).
	if _, errRes := s.authorizeWrite(ctx, rel); errRes != nil {
		return errRes, nil
	}

	// Load existing file when present; otherwise seed with the canonical
	// template. Either way we end up with a byte slice and the next OQ id.
	var existing []byte
	if note, loadErr := s.vault.Load(rel); loadErr == nil {
		existing = note.Content
	}
	nextID := nextOQIndex(existing)
	body := appendOQBlock(existing, project, nextID, question, urgency, qContext)

	if errRes := s.checkWriteLimits(tok, len(body)); errRes != nil {
		return errRes, nil
	}
	if err := s.writeAndIndex(rel, body); err != nil {
		return mcp.NewToolResultErrorFromErr("write failed", err), nil
	}
	s.auditWrite(ctx, audit.ActionAppend, rel, "", int64(len(body)))

	result := askResult{
		Path:  rel,
		OQID:  fmt.Sprintf("OQ-%03d", nextID),
		Index: nextID,
	}
	if fresh, err := s.vault.Load(rel); err == nil {
		result.ETag = fresh.ETag()
	}
	return mcp.NewToolResultJSON(result)
}

// nextOQIndex scans existing content for `### OQ-NNN` headings and returns
// the next integer above the maximum found. Returns 1 for empty content.
func nextOQIndex(body []byte) int {
	max := 0
	for _, m := range oqHeadingRe.FindAllStringSubmatch(string(body), -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n > max {
			max = n
		}
	}
	return max + 1
}

// appendOQBlock returns a new body with an OQ block appended to the
// "Aperte" section. When body is empty, the canonical open-questions.md
// template is synthesized and the first OQ is placed under the Aperte
// heading. When the file exists, we append the block after the Aperte
// heading but before the "Risolte" heading when possible; otherwise we
// append at the end of the file.
func appendOQBlock(body []byte, project string, id int, question, urgency, qContext string) []byte {
	today := time.Now().UTC().Format("2006-01-02")
	summary := oqHeadingSummary(question)
	block := fmt.Sprintf(
		"### OQ-%03d — %s\n\n- **Date**: %s\n- **Urgency**: %s\n- **Question**: %s\n",
		id, summary, today, urgency, question,
	)
	if qContext != "" {
		block += "- **Context**: " + qContext + "\n"
	}
	if len(body) == 0 {
		return []byte(renderOpenQuestionsTemplate(project) + "\n" + block)
	}
	src := string(body)
	// Try to insert the block right before the "## Risolte" heading so the
	// Aperte section stays ordered by discovery time. If no such heading,
	// append at the end of the file.
	if idx := strings.Index(src, "\n## Risolte"); idx >= 0 {
		before := src[:idx]
		after := src[idx:]
		// Ensure a blank line separator.
		if !strings.HasSuffix(before, "\n\n") {
			if strings.HasSuffix(before, "\n") {
				before += "\n"
			} else {
				before += "\n\n"
			}
		}
		return []byte(before + block + "\n" + after)
	}
	// Fallback: append with a blank-line separator.
	sep := "\n"
	if !strings.HasSuffix(src, "\n") {
		sep = "\n\n"
	} else if !strings.HasSuffix(src, "\n\n") {
		sep = "\n"
	} else {
		sep = ""
	}
	return []byte(src + sep + block)
}

// oqHeadingSummary returns a short (≤80 char) heading-friendly summary of
// the question. Drops trailing punctuation, single-line, word-boundary safe.
func oqHeadingSummary(question string) string {
	q := strings.TrimSpace(question)
	if i := strings.IndexByte(q, '\n'); i >= 0 {
		q = q[:i]
	}
	q = strings.TrimRight(q, ".?!")
	if len(q) <= 80 {
		return q
	}
	cut := q[:80]
	if sp := strings.LastIndex(cut, " "); sp > 40 {
		cut = cut[:sp]
	}
	return cut + "…"
}

// renderOpenQuestionsTemplate returns the minimal canonical template for a
// fresh open-questions.md file. Matches the shape used across gosidian
// projects.
func renderOpenQuestionsTemplate(project string) string {
	return "---\n" +
		"title: open-questions\n" +
		"description: Domande aperte senza risposta ancora decisa. Quando una question riceve risposta, spostala in " + project + "/docs/qa.md e rimuovila da qui.\n" +
		"tags: [" + project + ", type:doc, topic:meta]\n" +
		"type: doc\n" +
		"updated: " + time.Now().UTC().Format("2006-01-02") + "\n" +
		"---\n\n" +
		"# Open questions\n\n" +
		"Sezioni: **Aperte** (in attesa di decisione), **Risolte** (con link alla risposta / plan / ADR).\n\n" +
		"## Aperte\n"
}
