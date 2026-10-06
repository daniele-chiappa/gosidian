package mcp

import (
	"context"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// registerAutomationTool adds memory_automations (IMP-127 iteration 3,
// phase 4).
func (s *Server) registerAutomationTool() {
	s.impl.AddTool(mcp.NewTool("memory_automations",
		mcp.WithDescription("The automations of a project's database notes, the rules under `automations:` (a handoff when a row's date field comes within N days, a snapshot or a handoff at a fixed time each week or day), and a dry run of them: what they would do at as_of (a date, '2026-12-01', or a datetime; default now), with nothing written. Returns each rule with its trigger and action, whether it fires at as_of, the rows it would hand off or the slot it would fire, when it acts next, the rows coming later with their day, those already handed off, the rules that do not parse, and the last runs. run:true runs the project's rules now, as the server's automation identity (needs write access to the project); the server also runs them on its own every few minutes."),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project whose database notes to read. "+scopedProjectNote)),
		mcp.WithString("as_of", mcp.Description("The moment of the dry run: a date (YYYY-MM-DD, read as that day's end) or a datetime (RFC 3339, or 'YYYY-MM-DD HH:MM' in the server's time zone). Default now.")),
		mcp.WithBoolean("run", mcp.Description("Run the rules now instead of planning them (no as_of). Each action happens once, as when the server runs them.")),
	), s.handleAutomations)
}

func (s *Server) handleAutomations(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	project, err := s.resolveProject(tok, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if s.automations == nil {
		return mcp.NewToolResultError("automations are off on this server (automations.enabled)"), nil
	}
	loc := s.automations.Location()
	if req.GetBool("run", false) {
		if strings.TrimSpace(req.GetString("as_of", "")) != "" {
			return mcp.NewToolResultError("as_of is for the dry run: run acts now"), nil
		}
		if _, errRes := s.authorizeWrite(ctx, project+"/handoffs/x.md"); errRes != nil {
			return errRes, nil
		}
		// The writes are the server's, but the call is the token's: it counts
		// against its budget like any other write.
		if errRes := s.checkWriteLimits(ctx, tok, 0); errRes != nil {
			return errRes, nil
		}
		runs := s.automations.Tick(project)
		plan, err := s.automations.Plan(project, time.Time{})
		if err != nil {
			return mcp.NewToolResultErrorFromErr("automations", err), nil
		}
		return mcp.NewToolResultJSON(map[string]any{"project": project, "ran": runs, "plan": plan})
	}
	var asOf time.Time
	if v := strings.TrimSpace(req.GetString("as_of", "")); v != "" {
		t, err := parseAsOf(v, loc)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		asOf = t
	}
	plan, err := s.automations.Plan(project, asOf)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("automations", err), nil
	}
	return mcp.NewToolResultJSON(map[string]any{"project": project, "dry_run": true, "plan": plan})
}

// parseAsOf reads the moment of a dry run; a date alone is its last
// minute, so the plan covers the whole day.
func parseAsOf(v string, loc *time.Location) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02 15:04", v, loc); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation("2006-01-02", v, loc); err == nil {
		return t.Add(24*time.Hour - time.Minute), nil
	}
	return time.Time{}, errAsOf(v)
}

type errAsOf string

func (e errAsOf) Error() string {
	return "as_of: " + string(e) + " is not a date (YYYY-MM-DD) or a datetime (RFC 3339, or YYYY-MM-DD HH:MM)"
}
