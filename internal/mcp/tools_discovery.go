// Package mcp — structured discovery and pinned/stale tools (v1.2).
//
// Four read-only aggregate tools that save agents from combining tags
// client-side:
//   - memory_plans: type:plan + optional status filter, frontmatter-enriched
//   - memory_skills: type:skill + optional trigger_phrase substring match
//   - memory_pinned: notes tagged 'pinned' (convention: frontmatter tag)
//   - memory_stale: notes not modified in N time, archive candidates
//
// All tools are scoped by the caller's token project filter.
package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/mark3labs/mcp-go/mcp"
)

// registerDiscoveryTools adds the 4 v1.2 discovery tools. Called from
// registerTools().
func (s *Server) registerDiscoveryTools() {
	s.impl.AddTool(mcp.NewTool("memory_plans",
		mcp.WithDescription("List plans (notes with frontmatter type:plan) under a project, optionally filtered by status (draft/in-progress/done/archived). Returns path, title, status, updated and description from the frontmatter — saves the agent from combining memory_notes_by_tag results with per-note frontmatter reads."),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project (top-level folder) to scope the listing. Scoped tokens are forced to their project.")),
		mcp.WithString("status", mcp.Description("Optional status filter. One of: draft, in-progress, done, archived. Empty returns all.")),
	), s.handlePlans)

	s.impl.AddTool(mcp.NewTool("memory_skills",
		mcp.WithDescription("List skills (notes with frontmatter type:skill) under a project. Optionally filter by a substring matching the '## Trigger phrase' section of the skill body. Returns path, title, description from frontmatter, and the first ~400 chars of the trigger phrase section as an excerpt."),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project (top-level folder) to scope the listing. Scoped tokens are forced to their project.")),
		mcp.WithString("trigger_phrase", mcp.Description("Optional case-insensitive substring to match against the '## Trigger phrase' section of each skill. Empty returns all skills.")),
	), s.handleSkills)

	s.impl.AddTool(mcp.NewTool("memory_pinned",
		mcp.WithDescription("List pinned notes in a project. Convention: a note is pinned by adding 'pinned' to its frontmatter tags array. Pinned notes are those the author wants surfaced in every session — keep the list small."),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project (top-level folder). Scoped tokens are forced to their project.")),
	), s.handlePinned)

	s.impl.AddTool(mcp.NewTool("memory_stale",
		mcp.WithDescription("List notes in a project that have NOT been modified recently — archive candidates. Returns path, title, mtime (unix seconds) in ascending mtime order (oldest first)."),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project (top-level folder). Scoped tokens are forced to their project.")),
		mcp.WithString("older_than", mcp.Description("Cutoff age. Relative duration ('30d', '180d', '1h') or RFC3339 timestamp. Default '30d'.")),
		mcp.WithNumber("limit", mcp.Description("Max notes to return (default 20, max 500).")),
	), s.handleStale)
}

// ---- memory_plans ----

type planEntry struct {
	Path        string `json:"path"`
	Title       string `json:"title"`
	Status      string `json:"status,omitempty"`
	Updated     string `json:"updated,omitempty"`
	Description string `json:"description,omitempty"`
}

func (s *Server) handlePlans(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	project, err := s.resolveProject(tok, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	status := strings.TrimSpace(req.GetString("status", ""))
	if status != "" {
		switch status {
		case "draft", "in-progress", "done", "archived":
			// ok
		default:
			return mcp.NewToolResultErrorf("unknown status %q (expected draft, in-progress, done, archived)", status), nil
		}
	}

	notes, err := s.index.NotesByTag("type:plan")
	if err != nil {
		return mcp.NewToolResultErrorFromErr("plans lookup failed", err), nil
	}
	out := make([]planEntry, 0)
	prefix := project + "/"
	for _, n := range notes {
		if !strings.HasPrefix(n.Path, prefix) {
			continue
		}
		if !tok.AllowsPath(n.Path) {
			continue
		}
		entry := planEntry{Path: n.Path, Title: n.Title}
		fm := s.loadFrontmatter(n.Path)
		if v, ok := fm["status"].(string); ok {
			entry.Status = v
		}
		if v, ok := fm["updated"].(string); ok {
			entry.Updated = v
		}
		if v, ok := fm["description"].(string); ok {
			entry.Description = v
		}
		if status != "" && entry.Status != status {
			continue
		}
		out = append(out, entry)
	}
	return mcp.NewToolResultJSON(map[string]any{"plans": out})
}

// ---- memory_skills ----

type skillEntry struct {
	Path           string `json:"path"`
	Title          string `json:"title"`
	Description    string `json:"description,omitempty"`
	TriggerExcerpt string `json:"trigger_excerpt,omitempty"`
}

func (s *Server) handleSkills(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	project, err := s.resolveProject(tok, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	needle := strings.ToLower(strings.TrimSpace(req.GetString("trigger_phrase", "")))

	notes, err := s.index.NotesByTag("type:skill")
	if err != nil {
		return mcp.NewToolResultErrorFromErr("skills lookup failed", err), nil
	}
	out := make([]skillEntry, 0)
	prefix := project + "/"
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
		trigger := parser.ExtractSection(note.Content, "Trigger phrase")
		if needle != "" && !strings.Contains(strings.ToLower(trigger), needle) {
			continue
		}
		entry := skillEntry{
			Path:           n.Path,
			Title:          n.Title,
			TriggerExcerpt: truncateExcerpt(trigger, 400),
		}
		raw := parser.ExtractFrontmatterRaw(note.Content)
		fm := parser.ParseFrontmatterFields(raw)
		if v, ok := fm["description"].(string); ok {
			entry.Description = v
		}
		out = append(out, entry)
	}
	return mcp.NewToolResultJSON(map[string]any{"skills": out})
}

// ---- memory_pinned ----

func (s *Server) handlePinned(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	project, err := s.resolveProject(tok, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	notes, err := s.index.NotesByTag("pinned")
	if err != nil {
		return mcp.NewToolResultErrorFromErr("pinned lookup failed", err), nil
	}
	out := make([]noteRef, 0)
	prefix := project + "/"
	for _, n := range notes {
		if !strings.HasPrefix(n.Path, prefix) {
			continue
		}
		if !tok.AllowsPath(n.Path) {
			continue
		}
		out = append(out, noteRef{Path: n.Path, Title: n.Title})
	}
	return mcp.NewToolResultJSON(map[string]any{"notes": out})
}

// ---- memory_stale ----

func (s *Server) handleStale(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	project, err := s.resolveProject(tok, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	rawOlder := strings.TrimSpace(req.GetString("older_than", "30d"))
	cutoff, err := parseOlderThan(rawOlder)
	if err != nil {
		return mcp.NewToolResultErrorf("older_than %q: %v", rawOlder, err), nil
	}
	limit := req.GetInt("limit", 20)
	if limit <= 0 || limit > 500 {
		limit = 20
	}

	notes, err := s.index.StaleNotes(project, cutoff, limit)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("stale lookup failed", err), nil
	}
	out := make([]recentNoteResponse, 0, len(notes))
	for _, n := range notes {
		if !tok.AllowsPath(n.Path) {
			continue
		}
		out = append(out, recentNoteResponse{Path: n.Path, Title: n.Title, Mtime: n.Mtime})
	}
	return mcp.NewToolResultJSON(map[string]any{"notes": out})
}

// ---- helpers ----

// resolveProject pulls the project parameter and enforces token project
// scoping. Unlike memory_list_notes, memory_plans/skills/pinned/stale require
// project to be non-empty: operating on the whole vault is deliberately not
// supported (the semantic of "plans" is tied to a project).
func (s *Server) resolveProject(tok interface {
	ProjectFilter() string
}, req mcp.CallToolRequest) (string, error) {
	project := strings.TrimSpace(req.GetString("project", ""))
	scope := tok.ProjectFilter()
	if scope != "" {
		if project != "" && project != scope {
			return "", fmt.Errorf("project %q is outside the token's scope %q", project, scope)
		}
		return scope, nil
	}
	if project == "" {
		return "", fmt.Errorf("project is required")
	}
	return project, nil
}

// loadFrontmatter returns the parsed frontmatter of the note at path, or an
// empty map if the note can't be read (e.g. on disk but not in index).
func (s *Server) loadFrontmatter(path string) map[string]any {
	note, err := s.vault.Load(path)
	if err != nil {
		return map[string]any{}
	}
	raw := parser.ExtractFrontmatterRaw(note.Content)
	return parser.ParseFrontmatterFields(raw)
}

// truncateExcerpt returns s clipped to max runes with an ellipsis appended
// when truncated. Preserves word boundaries best-effort by walking back to
// the last whitespace. Empty input passes through unchanged.
func truncateExcerpt(s string, max int) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) <= max {
		return s
	}
	cut := s[:max]
	if sp := strings.LastIndexAny(cut, " \t\n"); sp > max/2 {
		cut = cut[:sp]
	}
	return cut + "…"
}

// parseOlderThan accepts either a relative duration ("30d", "24h") or an
// RFC3339 timestamp and returns the absolute unix-seconds cutoff. "d" is
// expanded to hours. A positive duration means "older than X ago".
func parseOlderThan(raw string) (int64, error) {
	dur := raw
	if strings.HasSuffix(dur, "d") {
		var days int
		if _, err := fmt.Sscanf(dur, "%dd", &days); err == nil {
			dur = fmt.Sprintf("%dh", days*24)
		}
	}
	if d, err := time.ParseDuration(dur); err == nil {
		return time.Now().Add(-d).Unix(), nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.Unix(), nil
	}
	return 0, fmt.Errorf("expected duration (30d, 24h) or RFC3339 timestamp")
}

// compile-time check: index.RecentNote is what StaleNotes returns.
var _ = func() index.RecentNote { return index.RecentNote{} }
