package mcp

import (
	"context"
	"strings"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/mark3labs/mcp-go/mcp"
)

// registerResourcesAndPrompts wires the read-only "resources" and "prompts"
// surface of MCP. Resources let agents reference notes by URI
// (memory://notes/<vault-relative-path>); prompts package together a
// canned context-loading workflow.
func (s *Server) registerResourcesAndPrompts() {
	// Single resource template that matches every note in the vault.
	template := mcp.NewResourceTemplate(
		"memory://notes/{path}",
		"Vault note",
		mcp.WithTemplateDescription("Read a note by its vault-relative path."),
		mcp.WithTemplateMIMEType("text/markdown"),
	)
	s.impl.AddResourceTemplate(template, s.readNoteResource)

	// "Load project context" prompt: returns CLAUDE.md plus every file under
	// <project>/agents/ as a single multi-message blob the agent can pin.
	prompt := mcp.NewPrompt(
		"memory_load_project_context",
		mcp.WithPromptDescription("Load CLAUDE.md and the agent definitions for a project from the gosidian memory."),
		mcp.WithArgument("project", mcp.ArgumentDescription("Project name to load context from."), mcp.RequiredArgument()),
	)
	s.impl.AddPrompt(prompt, s.handleLoadProjectContext)
}

// readNoteResource serves a single note via the memory:// URI scheme.
// Auth: requires read scope and the path must be inside the token's project
// filter. Returns Markdown.
func (s *Server) readNoteResource(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	tok := s.tokenFromContext(ctx)
	if tok == nil || !tok.HasScope(auth.ScopeRead) {
		return nil, errResource("unauthorized")
	}
	uri := req.Params.URI
	const prefix = "memory://notes/"
	if !strings.HasPrefix(uri, prefix) {
		return nil, errResource("unsupported uri")
	}
	rel := strings.TrimPrefix(uri, prefix)
	if !tok.AllowsPath(rel) {
		return nil, errResource("path outside scope")
	}
	note, err := s.vault.Load(rel)
	if err != nil {
		return nil, err
	}
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      uri,
			MIMEType: "text/markdown",
			Text:     string(note.Content),
		},
	}, nil
}

// handleLoadProjectContext composes the project's CLAUDE.md + every note
// under <project>/agents/*.md into a single user prompt message the agent
// can replay. Useful as a one-shot context bootstrap at session start.
func (s *Server) handleLoadProjectContext(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	tok := s.tokenFromContext(ctx)
	if tok == nil || !tok.HasScope(auth.ScopeRead) {
		return nil, errResource("unauthorized")
	}
	project, _ := req.Params.Arguments["project"]
	if project == "" {
		return nil, errResource("project argument required")
	}
	if tok.ProjectFilter() != "" && project != tok.ProjectFilter() {
		return nil, errResource("project outside scope")
	}

	var sb strings.Builder
	sb.WriteString("# Project context for ")
	sb.WriteString(project)
	sb.WriteString("\n\n")

	if note, err := s.vault.Load(project + "/CLAUDE.md"); err == nil {
		sb.WriteString("## CLAUDE.md\n\n")
		sb.Write(note.Content)
		sb.WriteString("\n\n")
	}

	if notes, err := s.index.NotesByPrefix(project + "/agents"); err == nil {
		for _, n := range notes {
			if !strings.HasSuffix(n.Path, ".md") {
				continue
			}
			content, err := s.vault.Load(n.Path)
			if err != nil {
				continue
			}
			sb.WriteString("## ")
			sb.WriteString(n.Path)
			sb.WriteString("\n\n")
			sb.Write(content.Content)
			sb.WriteString("\n\n")
		}
	}

	return &mcp.GetPromptResult{
		Description: "Project context loaded from gosidian memory",
		Messages: []mcp.PromptMessage{
			mcp.NewPromptMessage(
				mcp.RoleUser,
				mcp.NewTextContent(sb.String()),
			),
		},
	}, nil
}

// errResource is a tiny helper so we don't sprinkle errors.New everywhere.
func errResource(msg string) error {
	return resourceErr(msg)
}

type resourceErr string

func (r resourceErr) Error() string { return string(r) }
