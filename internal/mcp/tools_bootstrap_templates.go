// Package mcp — memory_list_bootstrap_templates tool (v1.8).
//
// Enumerates the templates available under <vault>/.gosidian/templates/
// and returns their meta (name, description, prompt, variables,
// file_count) so an agent can pick the right one before calling
// memory_project_scaffold.
//
// Read-only, requires `read` scope. Scoped tokens see the same list —
// templates are a global resource, not scoped by project.
package mcp

import (
	"context"

	"github.com/gosidian/gosidian/internal/scaffold"
	"github.com/mark3labs/mcp-go/mcp"
)

// registerBootstrapTemplatesTool adds memory_list_bootstrap_templates.
func (s *Server) registerBootstrapTemplatesTool() {
	s.impl.AddTool(mcp.NewTool("memory_list_bootstrap_templates",
		mcp.WithDescription("List the bootstrap templates installed under <vault>/.gosidian/templates/. Each entry includes name, description, a human-readable prompt explaining when to pick it, the declared variables, and the number of files that would be created. Pair with memory_project_scaffold(project, template=<name>) to create a new project from a chosen template."),
	), s.handleListBootstrapTemplates)
}

type templateEntry struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Prompt      string              `json:"prompt"`
	Variables   []scaffold.Variable `json:"variables,omitempty"`
	FileCount   int                 `json:"file_count"`
}

func (s *Server) handleListBootstrapTemplates(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if _, errRes := s.authorizeRead(ctx); errRes != nil {
		return errRes, nil
	}
	tmpls, err := scaffold.ListTemplates(s.vault.Root)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("templates list failed", err), nil
	}
	out := make([]templateEntry, 0, len(tmpls))
	for _, t := range tmpls {
		out = append(out, templateEntry{
			Name:        t.Name,
			Description: t.Description,
			Prompt:      t.Prompt,
			Variables:   t.Variables,
			FileCount:   t.FileCount,
		})
	}
	return mcp.NewToolResultJSON(map[string]any{"templates": out})
}
