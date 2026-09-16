// Package mcp — memory_self_improve tool (self-improvement feedback loop).
//
// Lets an opted-in agent record a structured insight about real-usage
// friction with gosidian itself. Each call writes one note to the configured
// insights project (private), tagged type:insight / status:pending for later
// human triage. Gated behind the [self_improve] master switch AND the calling
// token's per-token opt-in flag; with either off, every call is rejected.
// See plan 20260608-self-improve-feedback-loop.
package mcp

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/server/events"
	"github.com/mark3labs/mcp-go/mcp"
)

var selfImproveCategories = map[string]struct{}{
	"friction":           {},
	"bug":                {},
	"missing-capability": {},
	"docs-gap":           {},
	"performance":        {},
	"meta":               {},
}

var selfImproveConfidences = map[string]struct{}{
	"low":    {},
	"medium": {},
	"high":   {},
}

// registerSelfImproveTool wires memory_self_improve into the MCP surface.
func (s *Server) registerSelfImproveTool() {
	s.impl.AddTool(mcp.NewTool("memory_self_improve",
		mcp.WithDescription("Record a structured insight about friction you hit while USING gosidian itself (the MCP/UI/tools), not about the vault content. Writes one note to the private insights project for human triage. Only works when the operator has enabled the self-improvement loop and this token is opted in; otherwise it returns an error. IMPORTANT: describe the friction in the abstract — never include note content, project names, file paths, or user data."),
		mcp.WithString("category", mcp.Required(), mcp.Description("One of: friction (UX/ergonomics pain), bug (something misbehaved), missing-capability (wanted a tool/param that doesn't exist), docs-gap (couldn't find how to do X), performance (slow/token-heavy), meta (anything else).")),
		mcp.WithString("title", mcp.Required(), mcp.Description("Short one-line summary of the insight.")),
		mcp.WithString("friction", mcp.Required(), mcp.Description("What happened, described in the abstract. Never include note content, project names, paths, or user data.")),
		mcp.WithString("confidence", mcp.Required(), mcp.Description("How sure you are this is a real, actionable signal: low | medium | high.")),
		mcp.WithString("suggestion", mcp.Description("Optional: a concrete improvement you would propose.")),
		mcp.WithString("agent_label", mcp.Description("Optional: your own model label (e.g. \"opus-4.8\"), purely informational.")),
	), s.handleSelfImprove)
}

type selfImproveResult struct {
	Path     string `json:"path"`
	Category string `json:"category"`
	Status   string `json:"status"`
	ETag     string `json:"etag,omitempty"`
}

func (s *Server) handleSelfImprove(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok := s.tokenFromContext(ctx)
	if tok == nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	if !s.selfImproveEnabled {
		return mcp.NewToolResultError("self-improvement loop is disabled (operator must set [self_improve] enabled=true)"), nil
	}
	if !tok.SelfImproveOptIn {
		return mcp.NewToolResultError("this token is not opted in to the self-improvement loop"), nil
	}

	category, err := req.RequireString("category")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	category = strings.TrimSpace(category)
	if _, ok := selfImproveCategories[category]; !ok {
		return mcp.NewToolResultErrorf("unknown category %q (expected friction, bug, missing-capability, docs-gap, performance, meta)", category), nil
	}
	title, err := req.RequireString("title")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	title = strings.Join(strings.Fields(title), " ") // collapse to a single clean line
	if title == "" {
		return mcp.NewToolResultError("title must not be empty"), nil
	}
	friction, err := req.RequireString("friction")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	friction = strings.TrimSpace(friction)
	if friction == "" {
		return mcp.NewToolResultError("friction must not be empty"), nil
	}
	confidence, err := req.RequireString("confidence")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	confidence = strings.TrimSpace(confidence)
	if _, ok := selfImproveConfidences[confidence]; !ok {
		return mcp.NewToolResultErrorf("unknown confidence %q (expected low, medium, high)", confidence), nil
	}
	suggestion := strings.TrimSpace(req.GetString("suggestion", ""))
	agentLabel := strings.Join(strings.Fields(req.GetString("agent_label", "")), " ")

	project := s.selfImproveProject
	if project == "" {
		project = "insights"
	}

	now := time.Now().UTC()
	path := project + "/" + now.Format("2006-01-02") + "-" + siSlug(title) + "-" + generateCorrelationID() + ".md"
	rel, err := s.vault.Rel(path)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("invalid path", err), nil
	}

	body := renderInsight(project, now, category, title, friction, confidence, suggestion, agentLabel, tok.ID, correlationIDFromContext(ctx))
	if errRes := s.checkWriteLimits(tok, len(body)); errRes != nil {
		return errRes, nil
	}
	if err := s.writeAndIndex(rel, []byte(body)); err != nil {
		return mcp.NewToolResultErrorFromErr("write failed", err), nil
	}
	s.auditWrite(ctx, audit.ActionCreate, rel, "", int64(len(body)))

	if s.events != nil {
		s.events.Publish(events.TopicInsight, map[string]any{
			"action":     "create",
			"path":       rel,
			"category":   category,
			"confidence": confidence,
			"alert":      category == "bug" || confidence == "high",
			"source":     "mcp",
		})
	}

	result := selfImproveResult{Path: rel, Category: category, Status: "pending"}
	if fresh, err := s.vault.Load(rel); err == nil {
		result.ETag = fresh.ETag()
	}
	return mcp.NewToolResultJSON(result)
}

// renderInsight builds the markdown body of one insight note. category and
// confidence live as dedicated frontmatter fields (not tags) so they can be
// queried without widening the closed tag vocabulary; only type:insight and
// status:pending go in the tags array. source_token is the token id, which
// is already a non-reversible 8-hex prefix of the token hash — no
// user-identifying data is recorded.
func renderInsight(project string, now time.Time, category, title, friction, confidence, suggestion, agentLabel, tokenID, session string) string {
	date := now.Format("2006-01-02")
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("title: " + yamlQuote(title) + "\n")
	b.WriteString("description: " + yamlQuote(siFirstLine(friction, 120)) + "\n")
	b.WriteString("tags: [" + project + ", type:insight, status:pending]\n")
	b.WriteString("type: insight\n")
	b.WriteString("category: " + category + "\n")
	b.WriteString("confidence: " + confidence + "\n")
	b.WriteString("status: pending\n")
	b.WriteString("created: " + date + "\n")
	b.WriteString("source_token: " + tokenID + "\n")
	if agentLabel != "" {
		b.WriteString("agent_label: " + yamlQuote(agentLabel) + "\n")
	}
	if session != "" {
		b.WriteString("session: " + session + "\n")
	}
	b.WriteString("---\n\n")
	b.WriteString("# " + title + "\n\n")
	b.WriteString("**Category:** " + category + " · **Confidence:** " + confidence + "\n\n")
	b.WriteString("## Friction\n\n" + friction + "\n")
	if suggestion != "" {
		b.WriteString("\n## Suggestion\n\n" + suggestion + "\n")
	}
	return b.String()
}

var siSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

// siSlug turns a title into a short kebab-case filename fragment.
func siSlug(s string) string {
	s = strings.ToLower(s)
	s = siSlugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 48 {
		s = strings.Trim(s[:48], "-")
	}
	if s == "" {
		s = "insight"
	}
	return s
}

// siFirstLine returns the first line of s, trimmed to at most max runes on a
// word boundary, suitable for a one-line frontmatter description.
func siFirstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	cut := string(r[:max])
	if sp := strings.LastIndex(cut, " "); sp > max/2 {
		cut = cut[:sp]
	}
	return cut + "…"
}
