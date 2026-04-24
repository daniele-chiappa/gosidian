// Package mcp — memory_audit_tail tool.
//
// Exposes a filtered view of the audit log to agents over MCP. Read-only, no
// rate limit. Read-scoped tokens with a ProjectFilter are constrained to
// entries under their project prefix (parity with memory_list_notes scoping).
package mcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/mark3labs/mcp-go/mcp"
)

// registerAuditTools adds the audit-introspection tool. Called from
// registerTools() alongside registerAttachmentTools().
func (s *Server) registerAuditTools() {
	s.impl.AddTool(mcp.NewTool("memory_audit_tail",
		mcp.WithDescription("Read the most recent audit log entries, optionally filtered. Use this to introspect what write operations have been performed on the vault (by any agent or HTTP user) — essential for self-introspection after a task. Filters are all optional and combined with AND. Returns entries oldest→newest, capped at limit."),
		mcp.WithString("since", mcp.Description("Lower bound on timestamp. Relative duration ('1h', '24h', '7d') or RFC3339. Empty = no lower bound.")),
		mcp.WithString("until", mcp.Description("Upper bound on timestamp. Relative duration (ago) or RFC3339. Empty = no upper bound.")),
		mcp.WithString("actor", mcp.Description("Exact match on actor (token name, possibly suffixed with @<correlation_id> for MCP sessions).")),
		mcp.WithString("action", mcp.Description("Exact match on action. One of: create, update, append, delete, rename, create_project, delete_project, rename_project, upload_attachment, delete_attachment.")),
		mcp.WithString("path_prefix", mcp.Description("Prefix match on the vault-relative path. Scoped tokens always prepend their project.")),
		mcp.WithString("source", mcp.Description("Filter by source: 'http' (web UI) or 'mcp' (agent). Empty = both.")),
		mcp.WithNumber("limit", mcp.Description("Max entries to return (default 50, max 500).")),
	), s.handleAuditTail)
}

// auditEntryOut is the JSON shape returned to the MCP caller. Mirrors
// audit.Entry but renders TS as an RFC3339 string for easy client consumption.
type auditEntryOut struct {
	TS     string `json:"ts"`
	Source string `json:"source"`
	Token  string `json:"token,omitempty"`
	Actor  string `json:"actor,omitempty"`
	Action string `json:"action"`
	Path   string `json:"path"`
	To     string `json:"to,omitempty"`
	Size   int64  `json:"size,omitempty"`
}

func (s *Server) handleAuditTail(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	if s.audit == nil {
		return mcp.NewToolResultJSON(map[string]any{"entries": []auditEntryOut{}})
	}

	opts := audit.TailOpts{
		Actor:      strings.TrimSpace(req.GetString("actor", "")),
		PathPrefix: strings.TrimSpace(req.GetString("path_prefix", "")),
		Limit:      req.GetInt("limit", 50),
	}

	if raw := strings.TrimSpace(req.GetString("action", "")); raw != "" {
		// Validate against the known enum so callers get a friendly error
		// instead of a silently-empty result from a typo.
		if !knownAction(audit.Action(raw)) {
			return mcp.NewToolResultErrorf("unknown action %q", raw), nil
		}
		opts.Action = audit.Action(raw)
	}
	if raw := strings.TrimSpace(req.GetString("source", "")); raw != "" {
		if raw != string(audit.SourceHTTP) && raw != string(audit.SourceMCP) {
			return mcp.NewToolResultErrorf("unknown source %q (expected 'http' or 'mcp')", raw), nil
		}
		opts.Source = audit.Source(raw)
	}

	if raw := strings.TrimSpace(req.GetString("since", "")); raw != "" {
		t, err := parseTimeBound(raw)
		if err != nil {
			return mcp.NewToolResultErrorf("since: %v", err), nil
		}
		opts.Since = t
	}
	if raw := strings.TrimSpace(req.GetString("until", "")); raw != "" {
		t, err := parseTimeBound(raw)
		if err != nil {
			return mcp.NewToolResultErrorf("until: %v", err), nil
		}
		opts.Until = t
	}

	// Scoping: a project-scoped token may only read audit entries whose Path
	// starts with its project. An explicit path_prefix is allowed only if it
	// refines the scope further (i.e. is inside the token's project).
	if scope := tok.ProjectFilter(); scope != "" {
		scopePrefix := scope + "/"
		if opts.PathPrefix == "" {
			opts.PathPrefix = scopePrefix
		} else if !strings.HasPrefix(opts.PathPrefix, scopePrefix) && opts.PathPrefix != scope {
			return mcp.NewToolResultErrorf("path_prefix %q is outside the token's project scope %q", opts.PathPrefix, scope), nil
		}
	}

	entries, err := s.audit.TailFiltered(opts)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("audit read failed", err), nil
	}

	out := make([]auditEntryOut, 0, len(entries))
	for _, e := range entries {
		// Belt-and-suspenders: even after the path_prefix filter, reject any
		// entry that slipped through outside the token's scope. Cheap check.
		if !tok.AllowsPath(e.Path) && e.Path != "" {
			continue
		}
		out = append(out, auditEntryOut{
			TS:     e.TS.UTC().Format(time.RFC3339),
			Source: string(e.Source),
			Token:  e.Token,
			Actor:  e.Actor,
			Action: string(e.Action),
			Path:   e.Path,
			To:     e.To,
			Size:   e.Size,
		})
	}
	return mcp.NewToolResultJSON(map[string]any{"entries": out})
}

// knownAction returns true for the enumerated audit actions. Kept as a
// function (instead of a set) to mirror the constant list 1:1 — any new
// action constant must be added here or the tool rejects it.
func knownAction(a audit.Action) bool {
	switch a {
	case audit.ActionCreate, audit.ActionUpdate, audit.ActionAppend,
		audit.ActionDelete, audit.ActionRename,
		audit.ActionCreateProject, audit.ActionDeleteProject, audit.ActionRenameProject,
		audit.ActionUploadAttachment, audit.ActionDeleteAttachment:
		return true
	}
	return false
}

// parseTimeBound accepts either a relative duration ("24h", "7d") or an
// RFC3339 timestamp and returns the absolute instant. 'd' suffix is expanded
// to hours since time.ParseDuration doesn't support it natively.
func parseTimeBound(raw string) (time.Time, error) {
	dur := raw
	if strings.HasSuffix(dur, "d") {
		var days int
		if _, err := fmt.Sscanf(dur, "%dd", &days); err == nil {
			dur = fmt.Sprintf("%dh", days*24)
		}
	}
	if d, err := time.ParseDuration(dur); err == nil {
		return time.Now().Add(-d), nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("expected duration (24h, 7d) or RFC3339 timestamp, got %q", raw)
}
