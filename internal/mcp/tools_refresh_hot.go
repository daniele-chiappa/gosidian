// Package mcp — memory_refresh_hot tool (v1.6, F4.4 follow-up).
//
// Regenerates the `## Recent decisions` section of <project>/hot.md based on
// the last N ADRs in decisions.md and the last N closed plans. Opt-in by
// design: the target section must be wrapped in
// <!-- auto:recent-decisions --> … <!-- /auto --> markers; absence of the
// markers makes the tool a no-op so users who curate hot.md by hand are
// never surprised.
package mcp

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/mark3labs/mcp-go/mcp"
)

const (
	recentMarkerOpen  = "<!-- auto:recent-decisions -->"
	recentMarkerClose = "<!-- /auto -->"
)

var adrHeadingRe = regexp.MustCompile(`(?m)^## (ADR-\d+ .*)$`)

// registerRefreshHotTool adds memory_refresh_hot.
func (s *Server) registerRefreshHotTool() {
	s.impl.AddTool(mcp.NewTool("memory_refresh_hot",
		mcp.WithDescription("Rebuild the `## Recent decisions` section of <project>/hot.md from the latest ADRs in memory/decisions.md and the latest plans with status:done. Opt-in: the target section of hot.md must be wrapped in `<!-- auto:recent-decisions -->` / `<!-- /auto -->` markers; without them the tool is a no-op. Safe to re-run; everything outside the markers is preserved verbatim."),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project whose hot.md to refresh. Scoped tokens are forced to their project.")),
		mcp.WithNumber("limit", mcp.Description("Max number of entries to list (default 5).")),
	), s.handleRefreshHot)
}

type refreshHotResult struct {
	Project    string `json:"project"`
	Updated    bool   `json:"updated"`
	Reason     string `json:"reason,omitempty"`
	Entries    int    `json:"entries"`
	HotPath    string `json:"hot_path"`
	NewETag    string `json:"new_etag,omitempty"`
}

func (s *Server) handleRefreshHot(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeWrite(ctx, "")
	if errRes != nil {
		return errRes, nil
	}
	project, err := s.resolveProject(tok, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := req.GetInt("limit", 5)
	if limit <= 0 || limit > 50 {
		limit = 5
	}

	hotPath := project + "/hot.md"
	if !tok.AllowsPath(hotPath) {
		return mcp.NewToolResultErrorf("hot path %q is outside the token's scope", hotPath), nil
	}
	hot, err := s.vault.Load(hotPath)
	if err != nil {
		return mcp.NewToolResultErrorf("cannot read %s: %v", hotPath, err), nil
	}
	body := string(hot.Content)
	openIdx := strings.Index(body, recentMarkerOpen)
	closeIdx := strings.Index(body, recentMarkerClose)
	if openIdx < 0 || closeIdx < 0 || closeIdx < openIdx {
		return mcp.NewToolResultJSON(refreshHotResult{
			Project: project,
			Updated: false,
			HotPath: hotPath,
			Reason:  "no <!-- auto:recent-decisions --> markers found; insert them in hot.md to opt in",
		})
	}

	entries := s.buildRecentEntries(project, limit)
	section := recentMarkerOpen + "\n\n" + renderRecentEntries(entries) + "\n" + recentMarkerClose
	newBody := body[:openIdx] + section + body[closeIdx+len(recentMarkerClose):]
	if newBody == body {
		return mcp.NewToolResultJSON(refreshHotResult{
			Project: project,
			Updated: false,
			HotPath: hotPath,
			Entries: len(entries),
			Reason:  "already up to date",
		})
	}
	if errRes := s.checkWriteLimits(tok, len(newBody)); errRes != nil {
		return errRes, nil
	}
	if err := s.writeAndIndex(hotPath, []byte(newBody)); err != nil {
		return mcp.NewToolResultErrorFromErr("write failed", err), nil
	}
	s.auditWrite(ctx, audit.ActionUpdate, hotPath, "", int64(len(newBody)))

	newNote, _ := s.vault.Load(hotPath)
	newTag := ""
	if newNote != nil {
		newTag = newNote.ETag()
	}
	return mcp.NewToolResultJSON(refreshHotResult{
		Project: project,
		Updated: true,
		HotPath: hotPath,
		Entries: len(entries),
		NewETag: newTag,
	})
}

type recentEntry struct {
	Kind  string // "adr" | "plan"
	Title string
	Ref   string // path + optional anchor
	Date  string
}

func (s *Server) buildRecentEntries(project string, limit int) []recentEntry {
	out := make([]recentEntry, 0, limit*2)

	// ADRs: tail of decisions.md by textual order (append-only).
	decisionsPath := project + "/memory/decisions.md"
	if note, err := s.vault.Load(decisionsPath); err == nil {
		matches := adrHeadingRe.FindAllStringSubmatch(string(note.Content), -1)
		start := 0
		if len(matches) > limit {
			start = len(matches) - limit
		}
		for _, m := range matches[start:] {
			out = append(out, recentEntry{
				Kind:  "adr",
				Title: m[1],
				Ref:   decisionsPath,
			})
		}
	}

	// Plans: type:plan + status:done, sorted by frontmatter `updated` desc.
	plans, err := s.index.NotesByTag("type:plan")
	if err == nil {
		type donePlan struct {
			path, title, updated string
		}
		var done []donePlan
		prefix := project + "/"
		for _, n := range plans {
			if !strings.HasPrefix(n.Path, prefix) {
				continue
			}
			note, loadErr := s.vault.Load(n.Path)
			if loadErr != nil {
				continue
			}
			fm := parser.ParseFrontmatterFields(parser.ExtractFrontmatterRaw(note.Content))
			if fmString(fm, "status") != "done" {
				continue
			}
			done = append(done, donePlan{
				path:    n.Path,
				title:   n.Title,
				updated: fmString(fm, "updated"),
			})
		}
		sort.Slice(done, func(i, j int) bool { return done[i].updated > done[j].updated })
		if len(done) > limit {
			done = done[:limit]
		}
		for _, p := range done {
			out = append(out, recentEntry{
				Kind:  "plan",
				Title: p.title,
				Ref:   p.path,
				Date:  p.updated,
			})
		}
	}
	return out
}

func renderRecentEntries(entries []recentEntry) string {
	if len(entries) == 0 {
		return "_(nessuna decisione recente)_\n"
	}
	var sb strings.Builder
	for _, e := range entries {
		if e.Kind == "adr" {
			fmt.Fprintf(&sb, "- **%s** — [[%s]]\n", e.Title, strings.TrimSuffix(e.Ref, ".md"))
		} else {
			prefix := "plan-closed"
			if e.Date != "" {
				prefix = e.Date + " — plan-closed"
			}
			fmt.Fprintf(&sb, "- **%s** — %s [[%s]]\n", e.Title, prefix, strings.TrimSuffix(e.Ref, ".md"))
		}
	}
	return sb.String()
}

// keep `time` alive for potential future use (e.g. now-based filters).
var _ = time.Now
