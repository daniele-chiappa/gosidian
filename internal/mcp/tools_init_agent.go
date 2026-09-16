// Package mcp — memory_init_agent tool (v1.11).
//
// Returns an init-prompt payload for adopting gosidian as the memory
// layer in a new project. The caller (an AI agent) uses the prompt to
// create or augment its agent-native instruction file (AGENTS.md /
// CLAUDE.md / .cursor/rules.mdc / CONVENTIONS.md / …) with the thin
// gosidian_block STUB (Regola Zero → memory_bootstrap + local specifics).
// The full operational directives are served by memory_bootstrap
// (directives_block), not embedded in the file (ADR-010).
//
// Design notes:
//   - Read-only. The tool does NOT write to the vault and does NOT read
//     the agent's cwd. All scanning and file materialisation are
//     delegated to the agent itself.
//   - The filename is NOT chosen by the server. The agent determines it
//     (the tool surfaces an optional filename_hint but doesn't validate).
//   - Two modes: augment (existing_content provided → merge preserving
//     existing sections) and from-scratch (no existing_content → create
//     a new file with cwd-scan guidance).
//   - needs_scaffold flags whether the agent must call
//     memory_project_scaffold before materialising the instruction file.
package mcp

import (
	"context"
	"strings"

	"github.com/gosidian/gosidian/internal/initprompt"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/mark3labs/mcp-go/mcp"
)

func (s *Server) registerInitAgentTool() {
	s.impl.AddTool(mcp.NewTool("memory_init_agent",
		mcp.WithDescription("Produce an init-prompt payload for adopting gosidian as the memory layer in a project. Returns a `prompt` instructing the caller to create/update its agent-native instruction file plus a thin parametric `gosidian_block` stub to innest (Regola Zero pointing at memory_bootstrap; the operational directives are served by bootstrap, NOT embedded — ADR-010). Mode is picked by `existing_content`: augment (merge preserving existing sections) or from-scratch. All filesystem scanning/writing happens agent-side; `needs_scaffold=true` means call memory_project_scaffold first."),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project (top-level folder) to initialise. May not exist yet; check `needs_scaffold` in the response to know whether to call `memory_project_scaffold` first. Scoped tokens are forced to their project.")),
		mcp.WithString("agent_profile", mcp.Description("Target agent identifier. Known values: \"claude\", \"cursor\", \"codex\", \"aider\", \"generic\". Default \"generic\". Influences only the prompt tone and tool references — the gosidian_block is identical across profiles.")),
		mcp.WithString("existing_content", mcp.Description("Content of the agent's native instruction file when it already exists (the output of /init). If non-empty the tool switches to augment mode and the prompt will instruct a merge that preserves every existing section.")),
		mcp.WithString("filename_hint", mcp.Description("Optional filename the agent plans to use, e.g. \"CLAUDE.md\", \"AGENTS.md\", \".cursor/rules.mdc\". Surfaced in the prompt but never validated server-side.")),
		mcp.WithString("cwd_hint", mcp.Description("Absolute path of the agent's cwd. Used informatively in the prompt; the server does not read it.")),
		mcp.WithObject("user_hints", mcp.Description("Optional map with keys {language, code_language, project_type, stack, hot_files, agent_name}. Non-empty values are substituted into the gosidian_block placeholders server-side so the agent doesn't need to ask the user for them later.")),
	), s.handleInitAgent)
}

type initAgentResponse struct {
	Mode               string   `json:"mode"`
	NeedsScaffold      bool     `json:"needs_scaffold"`
	Prompt             string   `json:"prompt"`
	GosidianBlock      string   `json:"gosidian_block"`
	StubVersion        int      `json:"stub_version"`
	SuggestedQuestions []string `json:"suggested_questions"`
	// Anchors lists the local agent-anchor files to materialise for the active
	// profile (empty for profiles without spawnable-subagent support). Read-only
	// contract: the server returns them; the agent writes them to its cwd.
	Anchors []anchorRef `json:"anchors,omitempty"`
}

// anchorRef is one rendered agent anchor: the local target path, the file
// content to write, the meta_version fingerprint (for refresh detection) and
// the canonical vault note it pulls from.
type anchorRef struct {
	Path        string `json:"path"`
	Content     string `json:"content,omitempty"`
	MetaVersion string `json:"meta_version"`
	Canonical   string `json:"canonical"`
	// Unchanged marks an anchor whose meta_version matched the caller's
	// known_anchor_metas — the content was deliberately omitted (bootstrap
	// delta, IMP-068). Content empty ≠ empty anchor.
	Unchanged bool `json:"unchanged,omitempty"`
}

func (s *Server) handleInitAgent(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	project, err := req.RequireString("project")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	project = strings.TrimSpace(project)
	if project == "" {
		return mcp.NewToolResultError("project must not be empty"), nil
	}
	if !tok.AllowsProject(project) {
		return mcp.NewToolResultErrorf("project %q is outside the token's scope %q", project, tok.ScopeLabel()), nil
	}
	if res := s.rejectIfHidden(project); res != nil {
		return res, nil
	}

	profileStr := strings.TrimSpace(req.GetString("agent_profile", "generic"))
	if profileStr == "" {
		profileStr = "generic"
	}
	profile := initprompt.Profile(profileStr)
	if !initprompt.IsKnownProfile(profile) {
		return mcp.NewToolResultErrorf("unknown agent_profile %q; known values: claude, cursor, codex, aider, generic", profileStr), nil
	}

	existing := req.GetString("existing_content", "")
	mode := initprompt.ModeFromScratch
	if strings.TrimSpace(existing) != "" {
		mode = initprompt.ModeAugment
	}

	hintsMap := extractStringMap(req.GetArguments()["user_hints"])
	hints := initprompt.Hints{
		Language:     hintsMap["language"],
		CodeLanguage: hintsMap["code_language"],
		ProjectType:  hintsMap["project_type"],
		Stack:        hintsMap["stack"],
		HotFiles:     hintsMap["hot_files"],
		AgentName:    hintsMap["agent_name"],
		FilenameHint: req.GetString("filename_hint", ""),
		CwdHint:      req.GetString("cwd_hint", ""),
	}

	projects, err := s.vault.Projects()
	if err != nil {
		return mcp.NewToolResultErrorFromErr("projects lookup failed", err), nil
	}
	exists := false
	for _, p := range projects {
		if p.Name == project {
			exists = true
			break
		}
	}

	res, err := initprompt.Render(project, profile, mode, hints, exists)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("render failed", err), nil
	}

	anchors, err := s.buildAgentAnchors(project, profile, tok)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("anchor render failed", err), nil
	}

	return mcp.NewToolResultJSON(initAgentResponse{
		Mode:               string(res.Mode),
		NeedsScaffold:      res.NeedsScaffold,
		Prompt:             res.Prompt,
		GosidianBlock:      res.GosidianBlock,
		StubVersion:        res.StubVersion,
		SuggestedQuestions: res.SuggestedQuestions,
		Anchors:            anchors,
	})
}

// buildAgentAnchors renders the local anchor files for every type:agent note
// in the project, for profiles that support anchors. Returns nil for profiles
// without anchor support. Read-only: the rendered files are returned for the
// agent to materialise — the server never writes outside the vault. Reused by
// the bootstrap reconciler (M2).
func (s *Server) buildAgentAnchors(project string, profile initprompt.Profile, tok tokenScoped) ([]anchorRef, error) {
	if !initprompt.SupportsAnchors(profile) {
		return nil, nil
	}
	agents, err := s.filterByTagAndProject("type:agent", project, tok)
	if err != nil {
		return nil, err
	}
	out := make([]anchorRef, 0, len(agents))
	for _, a := range agents {
		note, err := s.vault.Load(a.Path)
		if err != nil {
			continue
		}
		in := anchorInputFromNote(a.Path, note.Content)
		if !in.Materialize {
			continue
		}
		ar, err := initprompt.RenderAgentAnchor(profile, in)
		if err != nil {
			continue
		}
		out = append(out, anchorRef{
			Path:        ar.Path,
			Content:     ar.Content,
			MetaVersion: ar.MetaVersion,
			Canonical:   a.Path,
		})
	}
	return out, nil
}

// anchorInputFromNote builds the anchor metadata from a type:agent note,
// applying defaults (name/description) and the optional `harness:` frontmatter
// overrides (name, description, model, tools, materialize).
func anchorInputFromNote(path string, content []byte) initprompt.AnchorInput {
	raw := parser.FrontmatterRawForPath(path, content)
	fields := parser.ParseFrontmatterFields(raw)
	slug := anchorSlug(path)
	in := initprompt.AnchorInput{CanonicalPath: path, Slug: slug, Name: slug, Materialize: true}
	if d, ok := fields["description"].(string); ok {
		in.Description = d
	}
	if h := parser.ExtractFrontmatterBlock(raw, "harness"); h != nil {
		if v, ok := h["name"].(string); ok && strings.TrimSpace(v) != "" {
			in.Name = v
		}
		if v, ok := h["description"].(string); ok && strings.TrimSpace(v) != "" {
			in.Description = v
		}
		if v, ok := h["model"].(string); ok && strings.TrimSpace(v) != "" {
			in.Model = v
		}
		switch v := h["tools"].(type) {
		case []string:
			if len(v) > 0 {
				in.Tools = v
			}
		case string:
			// Scalar form: `tools: all` inherits the CLI's full toolset.
			if strings.EqualFold(strings.TrimSpace(v), "all") {
				in.ToolsAll = true
			}
		}
		// The block parser is untyped: booleans arrive as strings.
		if v, ok := h["materialize"].(string); ok && isFrontmatterFalse(v) {
			in.Materialize = false
		}
	}
	return in
}

// isFrontmatterFalse reports whether an untyped harness scalar spells a
// negative boolean.
func isFrontmatterFalse(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "false", "no", "off":
		return true
	}
	return false
}

// anchorSlug derives the anchor file basename (without extension) from the
// vault note path: "plancia/agents/frontend-engineer.md" → "frontend-engineer".
func anchorSlug(path string) string {
	base := path
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	low := strings.ToLower(base)
	for _, ext := range []string{".md", ".html"} {
		if strings.HasSuffix(low, ext) {
			return base[:len(base)-len(ext)]
		}
	}
	return base
}
