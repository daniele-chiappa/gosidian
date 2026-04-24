// Package mcp — memory_todos tool (v1.9).
//
// Extracts GitHub-flavored markdown checkboxes (`- [ ]` / `- [x]`) from all
// notes under a project. The atomic unit of work in a gosidian plan is the
// checkbox, and before this tool agents had to load each plan in full and
// grep lines by hand to know "what's still pending".
//
// Scoping rules mirror memory_plans / memory_skills: project is required,
// scoped tokens are forced to their project.
package mcp

import (
	"context"
	"strings"

	"github.com/gosidian/gosidian/internal/parser"
	"github.com/mark3labs/mcp-go/mcp"
)

// todoEntry is one checkbox occurrence in one note.
type todoEntry struct {
	Path          string `json:"path"`
	Line          int    `json:"line"` // 1-based line number in the raw file
	Text          string `json:"text"`
	Checked       bool   `json:"checked"`
	ParentHeading string `json:"parent_heading,omitempty"`
	PlanStatus    string `json:"plan_status,omitempty"`
}

// registerTodosTool adds memory_todos to the MCP surface. Called from
// registerTools().
func (s *Server) registerTodosTool() {
	s.impl.AddTool(mcp.NewTool("memory_todos",
		mcp.WithDescription("Extract GitHub-flavored markdown checkboxes (`- [ ]` / `- [x]`) from all notes under a project. Returns path, line, text, checked state, parent heading context, and plan_status from the frontmatter when the note has type:plan. Use instead of memory_get + manual regex when you need granular pending-work awareness. Scoped tokens are forced to their project."),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project (top-level folder) to scan. Scoped tokens are forced to their project.")),
		mcp.WithBoolean("only_open", mcp.Description("When true, return only unchecked (`- [ ]`) todos. Default false (return all).")),
		mcp.WithString("plan_status", mcp.Description("If set, keep only todos inside notes with frontmatter `type:plan` AND matching status (draft|in-progress|done|archived). Notes without type:plan are excluded when this filter is used.")),
		mcp.WithString("path_prefix", mcp.Description("Optional vault-relative path prefix (e.g. 'gosidian/plans') to further restrict the scan within the project. Must start with the project name or be a sub-path of it.")),
		mcp.WithNumber("limit", mcp.Description("Max todo entries to return (default 200, max 2000). Applied AFTER filtering.")),
	), s.handleTodos)
}

func (s *Server) handleTodos(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	project, err := s.resolveProject(tok, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	onlyOpen := req.GetBool("only_open", false)
	planStatusFilter := strings.TrimSpace(req.GetString("plan_status", ""))
	if planStatusFilter != "" {
		switch planStatusFilter {
		case "draft", "in-progress", "done", "archived":
			// ok
		default:
			return mcp.NewToolResultErrorf("unknown plan_status %q (expected draft, in-progress, done, archived)", planStatusFilter), nil
		}
	}
	pathPrefix := strings.TrimSpace(req.GetString("path_prefix", ""))
	if pathPrefix != "" && !strings.HasPrefix(pathPrefix, project+"/") && pathPrefix != project {
		return mcp.NewToolResultErrorf("path_prefix %q must be inside project %q", pathPrefix, project), nil
	}
	limit := req.GetInt("limit", 200)
	if limit <= 0 || limit > 2000 {
		limit = 200
	}

	notes, err := s.index.NotesByPrefix(project)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("todos lookup failed", err), nil
	}

	out := make([]todoEntry, 0)
	for _, n := range notes {
		if !tok.AllowsPath(n.Path) {
			continue
		}
		if pathPrefix != "" && !strings.HasPrefix(n.Path, pathPrefix) {
			continue
		}

		note, loadErr := s.vault.Load(n.Path)
		if loadErr != nil {
			continue
		}

		// Cheap pre-filter: notes without any "- [" can't contain a checkbox.
		if !strings.Contains(string(note.Content), "- [") {
			continue
		}

		// Plan enrichment: read type + status from frontmatter once.
		isPlan, noteStatus := planInfoFromFrontmatter(note.Content)
		if planStatusFilter != "" {
			if !isPlan || noteStatus != planStatusFilter {
				continue
			}
		}

		todos := extractTodos(note.Content)
		for _, t := range todos {
			if onlyOpen && t.Checked {
				continue
			}
			t.Path = n.Path
			if isPlan {
				t.PlanStatus = noteStatus
			}
			out = append(out, t)
			if len(out) >= limit {
				break
			}
		}
		if len(out) >= limit {
			break
		}
	}
	return mcp.NewToolResultJSON(map[string]any{
		"todos":    out,
		"count":    len(out),
		"project":  project,
		"limit":    limit,
		"filtered": truncatedFlag(len(out), limit),
	})
}

// truncatedFlag signals whether the caller hit the limit and should re-query
// with path_prefix / higher limit. Kept simple: true iff len == limit (the
// list may or may not be truncated, but it's the point where the caller
// should verify).
func truncatedFlag(have, cap int) bool {
	return have >= cap
}

// planInfoFromFrontmatter returns (isPlan, status) by parsing the note's
// frontmatter. isPlan is true when the tags array contains "type:plan" or
// when the scalar `type` equals "plan".
func planInfoFromFrontmatter(body []byte) (bool, string) {
	raw := parser.ExtractFrontmatterRaw(body)
	if raw == "" {
		return false, ""
	}
	fm := parser.ParseFrontmatterFields(raw)
	isPlan := false
	if v, ok := fm["type"].(string); ok && v == "plan" {
		isPlan = true
	}
	if !isPlan {
		if tags, ok := fm["tags"].([]string); ok {
			for _, tag := range tags {
				if tag == "type:plan" {
					isPlan = true
					break
				}
			}
		}
	}
	status := ""
	if v, ok := fm["status"].(string); ok {
		status = v
	}
	return isPlan, status
}

// extractTodos scans body line-by-line and returns one todoEntry per
// GitHub-flavored checkbox. Skips lines inside fenced code blocks and the
// YAML frontmatter block. Tracks the most recent heading as parent context.
// Line numbers are 1-based and refer to the original body (including
// frontmatter).
func extractTodos(body []byte) []todoEntry {
	// Split on newlines but preserve indexes relative to the raw body so
	// the Line field is useful for callers that want to jump to the spot.
	src := string(body)
	lines := strings.Split(src, "\n")

	var (
		todos         []todoEntry
		parentHeading string
		inFence       bool
		inFrontmatter bool
	)

	// Detect initial frontmatter: a leading "---" on the first line opens a
	// block that closes on the next "---" or "..." line.
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		inFrontmatter = true
	}

	for i, line := range lines {
		// Frontmatter close (only relevant for the initial block, and only
		// when still inside it — a "---" thematic break later in the body
		// is not a frontmatter boundary).
		if inFrontmatter {
			if i > 0 {
				t := strings.TrimSpace(line)
				if t == "---" || t == "..." {
					inFrontmatter = false
				}
			}
			continue
		}

		// Code fence toggle.
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}

		// Heading tracking (only level 1-6 ATX; setext not supported here
		// to keep the scan single-pass and parent context simple).
		if hi := leadingHashes(trim); hi > 0 && hi <= 6 && hi < len(trim) && trim[hi] == ' ' {
			parentHeading = strings.TrimSpace(trim[hi+1:])
			continue
		}

		// Checkbox detection.
		if t, ok := parseCheckboxLine(line); ok {
			t.Line = i + 1
			t.ParentHeading = parentHeading
			todos = append(todos, t)
		}
	}

	return todos
}

// leadingHashes returns the count of leading '#' characters in s.
func leadingHashes(s string) int {
	n := 0
	for n < len(s) && s[n] == '#' {
		n++
	}
	return n
}

// parseCheckboxLine recognises GitHub-flavored checkbox lines:
//
//	- [ ] text
//	- [x] text
//	- [X] text
//
// with any leading whitespace. Returns (entry, true) on match; (zero, false)
// otherwise. Only the `-` bullet is recognised (not `*`, `+`): we fix the
// format deliberately to avoid false positives in permissive markdown.
func parseCheckboxLine(line string) (todoEntry, bool) {
	// Skip leading whitespace.
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	// Need at least "- [ ] x" after the indent.
	if i+6 > len(line) {
		return todoEntry{}, false
	}
	if line[i] != '-' || line[i+1] != ' ' || line[i+2] != '[' {
		return todoEntry{}, false
	}
	marker := line[i+3]
	if marker != ' ' && marker != 'x' && marker != 'X' {
		return todoEntry{}, false
	}
	if line[i+4] != ']' || line[i+5] != ' ' {
		return todoEntry{}, false
	}
	text := strings.TrimSpace(line[i+6:])
	if text == "" {
		return todoEntry{}, false
	}
	return todoEntry{
		Text:    text,
		Checked: marker == 'x' || marker == 'X',
	}, true
}
