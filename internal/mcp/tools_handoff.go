// Package mcp — agent handoff tools (v1.5, IMP-015; lifecycle added by the
// orchestrator-bus plan, M2).
//
// Four tools that formalize the "agent A passes state to agent B" pattern:
// create a handoff note (summary + pending items), query handoffs for a given
// destination agent, atomically claim one, and complete it. Handoff notes live
// at <project>/handoffs/YYYYMMDD-HHMMSS-<slug>.md with a stable frontmatter
// shape so memory_pending_handoffs can filter by `to_agent` and `status` with
// a single NotesByTag("type:handoff") call followed by a frontmatter scan.
//
// Lifecycle: pending → claimed → done | rejected. Claim and complete run
// under the per-note path lock (vault.LockPath), so when several agents race
// for the same handoff exactly one claim wins. created_by, claimed_by and
// completed_by are stamped server-side from the caller's token identity —
// they are not caller-supplied and cannot be forged over MCP. from_agent and
// to_agent stay declarative role slugs by design (one token may play several
// roles): declaration and identity live side by side in the frontmatter.
package mcp

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/vault"
	"github.com/mark3labs/mcp-go/mcp"
)

// handoffStatuses is the closed lifecycle vocabulary. "pending" is the only
// state memory_create_handoff ever writes; the transitions are owned by
// memory_claim_handoff (pending→claimed) and memory_complete_handoff
// (claimed→done|rejected).
var handoffStatuses = map[string]bool{
	"pending":  true,
	"claimed":  true,
	"done":     true,
	"rejected": true,
}

// registerHandoffTools adds the four handoff tools.
func (s *Server) registerHandoffTools() {
	s.impl.AddTool(mcp.NewTool("memory_create_handoff",
		mcp.WithDescription("Create a handoff note that passes context from one agent to another. Stored at <project>/handoffs/YYYYMMDD-HHMMSS-<slug>.md with frontmatter {type: handoff, from_agent, to_agent, status: pending, created_by}. created_by is stamped server-side from the caller's token identity; from_agent/to_agent are declarative role slugs. The destination agent picks it up via memory_pending_handoffs, takes it in charge with memory_claim_handoff and closes it with memory_complete_handoff. Use this instead of an ad-hoc note when control switches between specialized agents during a task."),
		mcp.WithString("project", mcp.Required(), mcp.Description("Target project (top-level folder). "+scopedProjectNote)),
		mcp.WithString("from_agent", mcp.Required(), mcp.Description("Slug of the source agent (e.g. 'go-backend').")),
		mcp.WithString("to_agent", mcp.Required(), mcp.Description("Slug of the destination agent (e.g. 'web-ui-htmx').")),
		mcp.WithString("summary", mcp.Required(), mcp.Description("One-paragraph summary of what was done and why the handoff is happening.")),
		mcp.WithArray("pending_items", mcp.Description("Optional array of open items the receiving agent should pick up. Rendered as a bullet list in the note body.")),
	), s.handleCreateHandoff)

	s.impl.AddTool(mcp.NewTool("memory_pending_handoffs",
		mcp.WithDescription("List handoff notes under a project, filtered by lifecycle status (default: pending) and optionally by destination agent. Use at session start when taking over from another agent, or with status=claimed/done to monitor handoffs in flight."),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project to search in. "+scopedProjectNote)),
		mcp.WithString("for_agent", mcp.Description("Destination agent slug; matches frontmatter to_agent exactly. Omit to list handoffs addressed to any agent.")),
		mcp.WithString("status", mcp.Description("Lifecycle filter: pending (default), claimed, done, rejected, or all.")),
	), s.handlePendingHandoffs)

	s.impl.AddTool(mcp.NewTool("memory_claim_handoff",
		mcp.WithDescription("Atomically claim a pending handoff: flips frontmatter status pending→claimed and stamps claimed_by/claimed_at from the caller's token identity, under a per-note lock — when several agents race for the same handoff exactly one wins and the others get an 'already claimed' error. Claim before starting the work so other agents skip it."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault-relative path of the handoff note (as returned by memory_pending_handoffs).")),
	), s.handleClaimHandoff)

	s.impl.AddTool(mcp.NewTool("memory_complete_handoff",
		mcp.WithDescription("Close a claimed handoff: sets status to done or rejected, stamps completed_by/completed_at, and optionally appends an '## Outcome' section to the note body. Only the token that claimed the handoff (or an admin token) can complete it."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault-relative path of the handoff note.")),
		mcp.WithString("outcome", mcp.Required(), mcp.Description("Final status: 'done' (work finished) or 'rejected' (handoff refused or not applicable).")),
		mcp.WithString("note", mcp.Description("Optional outcome text appended to the note as an '## Outcome' section.")),
	), s.handleCompleteHandoff)
}

type handoffEntry struct {
	Path        string `json:"path"`
	Title       string `json:"title"`
	Status      string `json:"status,omitempty"`
	FromAgent   string `json:"from_agent,omitempty"`
	ToAgent     string `json:"to_agent,omitempty"`
	Created     string `json:"created,omitempty"`
	CreatedBy   string `json:"created_by,omitempty"`
	ClaimedBy   string `json:"claimed_by,omitempty"`
	ClaimedAt   string `json:"claimed_at,omitempty"`
	CompletedBy string `json:"completed_by,omitempty"`
	CompletedAt string `json:"completed_at,omitempty"`
	Summary     string `json:"summary,omitempty"`
}

func (s *Server) handleCreateHandoff(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Scope check only at this point — the concrete path doesn't exist yet.
	// authorizeWrite(ctx, "") would always reject project-scoped tokens
	// (AllowsPath("") is false), which silently barred them from creating
	// handoffs; the per-candidate authorizeWrite below covers path scope.
	tok := s.tokenFromContext(ctx)
	if tok == nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	if !tok.HasScope(auth.ScopeWrite) {
		return mcp.NewToolResultError("token lacks write scope"), nil
	}
	project, err := s.resolveProject(tok, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	fromAgent := strings.TrimSpace(req.GetString("from_agent", ""))
	toAgent := strings.TrimSpace(req.GetString("to_agent", ""))
	summary := strings.TrimSpace(req.GetString("summary", ""))
	if fromAgent == "" || toAgent == "" {
		return mcp.NewToolResultError("from_agent and to_agent are required"), nil
	}
	if summary == "" {
		return mcp.NewToolResultError("summary is required"), nil
	}
	pending := req.GetStringSlice("pending_items", nil)

	now := time.Now().UTC()
	slug := handoffSlug(fromAgent, toAgent)
	base := fmt.Sprintf("%s/handoffs/%s-%s", project, now.Format("20060102-150405"), slug)

	creator := tokenIdentity(tok)
	content := renderHandoffBody(fromAgent, toAgent, creator, now, summary, pending)

	// The timestamped filename makes collisions unlikely but not impossible
	// (two same-second handoffs between the same agents), so probe candidate
	// paths under the per-path lock and take the first free one instead of
	// clobbering. One lock is held at a time — no ordering concerns.
	relPath := ""
	for i := 0; i < 10 && relPath == ""; i++ {
		candidate := base
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", base, i+1)
		}
		candidate += ".md"
		// Scope/path check on the concrete path now that we know it.
		if _, errRes := s.authorizeWrite(ctx, candidate); errRes != nil {
			return errRes, nil
		}
		_, werr := s.writeNote(ctx, tok, noteWrite{
			rel: candidate, mode: writeCreate, content: fixedContent([]byte(content)), action: audit.ActionCreate,
		})
		switch {
		case werr != nil && werr.kind == writeExists:
			continue
		case werr != nil && werr.kind == writeFailed:
			return mcp.NewToolResultError("handoff create failed: " + werr.msg), nil
		case werr != nil:
			return werr.result(), nil
		}
		relPath = candidate
	}
	if relPath == "" {
		return mcp.NewToolResultError("could not allocate a unique handoff path (too many same-second handoffs); retry"), nil
	}

	return mcp.NewToolResultJSON(map[string]any{
		"path":       relPath,
		"from_agent": fromAgent,
		"to_agent":   toAgent,
		"status":     "pending",
		"created":    now.Format(time.RFC3339),
		"created_by": creator,
	})
}

func (s *Server) handlePendingHandoffs(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	project, err := s.resolveProject(tok, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	forAgent := strings.TrimSpace(req.GetString("for_agent", ""))
	statusFilter := strings.TrimSpace(req.GetString("status", "pending"))
	if statusFilter != "all" && !handoffStatuses[statusFilter] {
		return mcp.NewToolResultErrorf("unknown status %q (expected pending, claimed, done, rejected, or all)", statusFilter), nil
	}

	out, err := s.listHandoffs(tok, project, forAgent, statusFilter)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("handoff lookup failed", err), nil
	}
	return mcp.NewToolResultJSON(map[string]any{"handoffs": out})
}

// listHandoffs returns the handoffs of project the token may read, with a
// status (or "all") and, when forAgent is set, addressed to that agent.
func (s *Server) listHandoffs(tok *auth.Token, project, forAgent, statusFilter string) ([]handoffEntry, error) {
	notes, err := s.index.NotesByTag("type:handoff")
	if err != nil {
		return nil, err
	}
	out := make([]handoffEntry, 0)
	prefix := project + "/handoffs/"
	for _, n := range notes {
		if !strings.HasPrefix(n.Path, prefix) {
			continue
		}
		if !tok.AllowsPath(n.Path) {
			continue
		}
		note, loadErr := s.vault.Load(n.Path)
		if loadErr != nil {
			continue
		}
		raw := parser.FrontmatterRawForPath(n.Path, note.Content)
		fm := parser.ParseFrontmatterFields(raw)
		if forAgent != "" && fmString(fm, "to_agent") != forAgent {
			continue
		}
		status := fmString(fm, "status")
		if statusFilter != "all" && status != statusFilter {
			continue
		}
		out = append(out, handoffEntry{
			Path:        n.Path,
			Title:       n.Title,
			Status:      status,
			FromAgent:   fmString(fm, "from_agent"),
			ToAgent:     fmString(fm, "to_agent"),
			Created:     fmString(fm, "created"),
			CreatedBy:   fmString(fm, "created_by"),
			ClaimedBy:   fmString(fm, "claimed_by"),
			ClaimedAt:   fmString(fm, "claimed_at"),
			CompletedBy: fmString(fm, "completed_by"),
			CompletedAt: fmString(fm, "completed_at"),
			Summary:     truncateExcerpt(parser.ExtractSection(note.Content, "Summary"), 300),
		})
	}
	return out, nil
}

func (s *Server) handleClaimHandoff(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

	// The whole read→check→flip sequence runs under the path lock (in
	// writeNote): among N concurrent claimants exactly one sees
	// status=pending.
	now := time.Now().UTC().Format(time.RFC3339)
	claimer := tokenIdentity(tok)
	res, werr := s.writeNote(ctx, tok, noteWrite{
		rel: rel, mode: writeReplace, action: audit.ActionUpdate,
		content: func(note *vault.Note) ([]byte, error) {
			raw := parser.FrontmatterRawForPath(rel, note.Content)
			fm := parser.ParseFrontmatterFields(raw)
			if !isHandoffNote(fm, raw) {
				return nil, contentError("%q is not a handoff note (missing type: handoff)", rel)
			}
			switch status := fmString(fm, "status"); status {
			case "pending":
				// claimable
			case "claimed":
				return nil, contentError("handoff %q already claimed by %s at %s", rel, fmString(fm, "claimed_by"), fmString(fm, "claimed_at"))
			default:
				return nil, contentError("handoff %q is %q, not pending", rel, status)
			}
			updated, err := handoffSetStatus(note.Content, "claimed", [][2]string{
				{"claimed_by", claimer},
				{"claimed_at", now},
			})
			if err != nil {
				return nil, contentError("claim failed: %v", err)
			}
			return updated, nil
		},
	})
	if werr != nil {
		return handoffWriteError(rel, werr), nil
	}

	out := map[string]any{
		"path":       rel,
		"status":     "claimed",
		"claimed_by": claimer,
		"claimed_at": now,
	}
	if res.ETag != "" {
		out["etag"] = res.ETag
	}
	return mcp.NewToolResultJSON(out)
}

func (s *Server) handleCompleteHandoff(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	outcome, err := req.RequireString("outcome")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if outcome != "done" && outcome != "rejected" {
		return mcp.NewToolResultErrorf("unknown outcome %q (expected done or rejected)", outcome), nil
	}
	outcomeNote := strings.TrimSpace(req.GetString("note", ""))
	rel, err := s.vault.Rel(path)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("invalid path", err), nil
	}
	tok, errRes := s.authorizeWrite(ctx, rel)
	if errRes != nil {
		return errRes, nil
	}

	now := time.Now().UTC().Format(time.RFC3339)
	completer := tokenIdentity(tok)
	res, werr := s.writeNote(ctx, tok, noteWrite{
		rel: rel, mode: writeReplace, action: audit.ActionUpdate,
		content: func(note *vault.Note) ([]byte, error) {
			raw := parser.FrontmatterRawForPath(rel, note.Content)
			fm := parser.ParseFrontmatterFields(raw)
			if !isHandoffNote(fm, raw) {
				return nil, contentError("%q is not a handoff note (missing type: handoff)", rel)
			}
			if status := fmString(fm, "status"); status != "claimed" {
				return nil, contentError("handoff %q is %q, not claimed (claim it first)", rel, status)
			}
			// Admin tokens (unscoped) may complete on behalf of anyone — the
			// escape hatch for stuck claims from dead agents.
			if claimedBy := fmString(fm, "claimed_by"); !tok.IsAdmin() && claimedBy != completer {
				return nil, contentError("handoff %q was claimed by %s; only the claimer or an admin token can complete it", rel, claimedBy)
			}
			updated, err := handoffSetStatus(note.Content, outcome, [][2]string{
				{"completed_by", completer},
				{"completed_at", now},
			})
			if err != nil {
				return nil, contentError("complete failed: %v", err)
			}
			if outcomeNote != "" {
				body := strings.TrimRight(string(updated), "\n")
				updated = []byte(body + "\n\n## Outcome\n\n" + outcomeNote + "\n")
			}
			return updated, nil
		},
	})
	if werr != nil {
		return handoffWriteError(rel, werr), nil
	}

	out := map[string]any{
		"path":         rel,
		"status":       outcome,
		"completed_by": completer,
		"completed_at": now,
	}
	if res.ETag != "" {
		out["etag"] = res.ETag
	}
	return mcp.NewToolResultJSON(out)
}

// handoffWriteError words a refused handoff write: a missing note is a
// missing handoff.
func handoffWriteError(rel string, werr *writeError) *mcp.CallToolResult {
	if werr.kind == writeMissing {
		return mcp.NewToolResultErrorf("handoff %q does not exist", rel)
	}
	return werr.result()
}

// ---- helpers ----

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func handoffSlug(from, to string) string {
	base := strings.ToLower(from + "-to-" + to)
	base = slugRe.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		return "handoff"
	}
	return base
}

func renderHandoffBody(from, to, createdBy string, created time.Time, summary string, pending []string) string {
	var sb strings.Builder
	sb.WriteString("---\n")
	fmt.Fprintf(&sb, "title: Handoff %s → %s\n", from, to)
	fmt.Fprintf(&sb, "type: handoff\n")
	fmt.Fprintf(&sb, "status: pending\n")
	fmt.Fprintf(&sb, "from_agent: %s\n", from)
	fmt.Fprintf(&sb, "to_agent: %s\n", to)
	fmt.Fprintf(&sb, "created: %s\n", created.Format(time.RFC3339))
	fmt.Fprintf(&sb, "created_by: %s\n", createdBy)
	fmt.Fprintf(&sb, "tags: [type:handoff, status:pending]\n")
	sb.WriteString("---\n\n")
	fmt.Fprintf(&sb, "# Handoff %s → %s\n\n", from, to)
	sb.WriteString("## Summary\n\n")
	sb.WriteString(summary)
	sb.WriteString("\n\n")
	if len(pending) > 0 {
		sb.WriteString("## Pending items\n\n")
		for _, p := range pending {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			fmt.Fprintf(&sb, "- %s\n", p)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

func fmString(fm map[string]any, key string) string {
	if v, ok := fm[key].(string); ok {
		return v
	}
	return ""
}

// isHandoffNote accepts a note as a handoff when the frontmatter declares
// `type: handoff` or the raw frontmatter carries the type:handoff tag (the
// latter tolerates hand-authored notes with tags but no type field).
func isHandoffNote(fm map[string]any, raw string) bool {
	return fmString(fm, "type") == "handoff" || strings.Contains(raw, "type:handoff")
}

// tokenIdentity is the server-stamped identity written into claimed_by /
// completed_by: the token name plus its short id. Not caller-supplied, so a
// project token cannot impersonate another agent's token in the lifecycle
// fields (from_agent/to_agent stay declarative role slugs by design).
func tokenIdentity(tok *auth.Token) string {
	return tok.Name + "@" + tok.ID
}

var statusTagRe = regexp.MustCompile(`status:[a-z-]+`)

// handoffSetStatus rewrites the frontmatter block of a handoff note: sets the
// `status:` field, mirrors it into the status:* entry of the tags line when
// present, and upserts the extra key/value pairs before the closing
// delimiter. The body is left untouched. Only canonical top-level keys (no
// leading indentation) are recognised — the shape renderHandoffBody emits.
func handoffSetStatus(content []byte, newStatus string, extra [][2]string) ([]byte, error) {
	text := string(content)
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r") != "---" {
		return nil, fmt.Errorf("note has no frontmatter block")
	}
	closing := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r") == "---" {
			closing = i
			break
		}
	}
	if closing < 0 {
		return nil, fmt.Errorf("unterminated frontmatter block")
	}

	statusSeen := false
	for i := 1; i < closing; i++ {
		switch {
		case strings.HasPrefix(lines[i], "status:"):
			lines[i] = "status: " + newStatus
			statusSeen = true
		case strings.HasPrefix(lines[i], "tags:"):
			lines[i] = statusTagRe.ReplaceAllString(lines[i], "status:"+newStatus)
		}
	}

	var inserts []string
	if !statusSeen {
		inserts = append(inserts, "status: "+newStatus)
	}
	for _, kv := range extra {
		key, val := kv[0], kv[1]
		replaced := false
		for i := 1; i < closing; i++ {
			if strings.HasPrefix(lines[i], key+":") {
				lines[i] = key + ": " + val
				replaced = true
				break
			}
		}
		if !replaced {
			inserts = append(inserts, key+": "+val)
		}
	}

	out := make([]string, 0, len(lines)+len(inserts))
	out = append(out, lines[:closing]...)
	out = append(out, inserts...)
	out = append(out, lines[closing:]...)
	return []byte(strings.Join(out, "\n")), nil
}
