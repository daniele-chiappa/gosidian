// Package mcp — memory_global_check tool (global project, Phase 3).
//
// Human-gate for the private→public propagation: before publishing a project,
// an operator runs this to see which global-private notes the project links to,
// and which OTHER projects also link to each — so promoting one (deliberately,
// via memory_move_note) is an informed choice, never an automatic side-effect.
// Read-only: it never moves or changes anything.
// See plan 20260608-global-project-shared-skills.
package mcp

import (
	"context"
	"sort"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// registerGlobalCheckTool wires memory_global_check into the MCP surface.
func (s *Server) registerGlobalCheckTool() {
	s.impl.AddTool(mcp.NewTool("memory_global_check",
		mcp.WithDescription("Before publishing a project (private→public), list the global-private notes it references via wikilinks, and for each which OTHER projects also reference it. Promoting any of them to the public global (e.g. with memory_move_note) is a deliberate, human-gated step — this tool only reports, it never moves anything. Returns an empty list when the global feature is off or nothing is referenced."),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project to check (the one you intend to publish). "+scopedProjectNote)),
	), s.handleGlobalCheck)
}

type globalRef struct {
	Path       string   `json:"path"`
	AlsoUsedBy []string `json:"also_used_by"`
}

type globalCheckResult struct {
	Project       string      `json:"project"`
	GlobalPrivate string      `json:"global_private"`
	Referenced    []globalRef `json:"referenced"`
}

func (s *Server) handleGlobalCheck(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	project, err := s.resolveProject(tok, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	res := globalCheckResult{Project: project, GlobalPrivate: s.globalPrivate, Referenced: []globalRef{}}
	if !s.globalEnabled || s.globalPrivate == "" {
		return mcp.NewToolResultJSON(res)
	}

	notes, err := s.index.NotesByPrefix(project)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("notes lookup failed", err), nil
	}
	privPrefix := s.globalPrivate + "/"
	refSet := map[string]struct{}{}
	for _, n := range notes {
		outs, err := s.index.Outlinks(n.Path)
		if err != nil {
			continue
		}
		for _, o := range outs {
			if o.TargetPath != "" && strings.HasPrefix(o.TargetPath, privPrefix) {
				refSet[o.TargetPath] = struct{}{}
			}
		}
	}

	paths := make([]string, 0, len(refSet))
	for p := range refSet {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		bls, _ := s.index.Backlinks(p)
		others := map[string]struct{}{}
		for _, bl := range bls {
			proj := topLevelProject(bl.Path)
			if proj == "" || proj == project || proj == s.globalPrivate {
				continue
			}
			others[proj] = struct{}{}
		}
		list := make([]string, 0, len(others))
		for o := range others {
			list = append(list, o)
		}
		sort.Strings(list)
		res.Referenced = append(res.Referenced, globalRef{Path: p, AlsoUsedBy: list})
	}
	return mcp.NewToolResultJSON(res)
}
