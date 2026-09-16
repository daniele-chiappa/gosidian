// Package mcp — memory_promote_agent tool (plan 20260630-agent-anchors, M4).
//
// Adopts a foreign, hand-written local agent file (a CLI subagent definition
// NOT produced by gosidian — no `gosidian:anchor` marker) into a canonical
// vault `type:agent` note, so it becomes a shared, version-controlled role. The
// agent then replaces the local file with the returned anchor (which pulls the
// role from the vault at spawn). Human-gated: the agent invokes this only on
// the user's explicit confirmation, surfaced by the bootstrap reconcile flow.
package mcp

import (
	"context"
	"strings"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/initprompt"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/mark3labs/mcp-go/mcp"
)

func (s *Server) registerPromoteAgentTool() {
	s.impl.AddTool(mcp.NewTool("memory_promote_agent",
		mcp.WithDescription("Adopt a foreign, hand-written local agent file (a CLI subagent definition not produced by gosidian — no `gosidian:anchor` marker) into a canonical vault `type:agent` note, then return the anchor to replace it with. Use this when the bootstrap `anchors.reconcile` flow reports a foreign file and the USER confirms adoption: the file's content becomes a shared, version-controlled role at `<project>/agents/<slug>.md` (its system-prompt body preserved; name/tools captured into a `harness:` block), and the returned `anchor` is the thin local file (pulling the role from the vault) to write in place of the original. When the canonical note ALREADY exists, pass adopt_into_existing:true: the existing body is never touched (it stays the source of truth), a `harness:` block is added only if missing, and the foreign body comes back in `foreign_body_for_review` for a manual fold-check. One source of truth, no content lost. Writes to the vault (requires write access)."),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project (top-level folder) that will own the promoted agent. Scoped tokens are forced to their project.")),
		mcp.WithString("slug", mcp.Required(), mcp.Description("Agent slug — the note basename without extension, e.g. \"rc-database\". The canonical note is created at <project>/agents/<slug>.md.")),
		mcp.WithString("content", mcp.Required(), mcp.Description("Full content of the foreign local agent file (frontmatter name/description/tools + system-prompt body). Parsed to build the canonical note; the body is preserved verbatim.")),
		mcp.WithString("profile", mcp.Description("CLI profile for the returned anchor. Default \"claude\".")),
		mcp.WithBoolean("adopt_into_existing", mcp.Description("When the canonical note already exists, adopt the foreign file into it instead of failing: the existing body is preserved untouched, a harness: block is inserted only if absent, and the foreign body is returned in foreign_body_for_review for a manual fold-check via memory_edit. No-op when the canonical does not exist (normal promote). Default false.")),
	), s.handlePromoteAgent)
}

type promoteAgentResponse struct {
	Path   string     `json:"path"`             // canonical vault note created (or adopted into)
	Anchor *anchorRef `json:"anchor,omitempty"` // anchor to write in place of the foreign file
	// AdoptedIntoExisting marks the adopt path: the canonical note already
	// existed and its body was left untouched (harness block added only if
	// missing). The foreign body is echoed back for a manual fold-check.
	AdoptedIntoExisting  bool   `json:"adopted_into_existing,omitempty"`
	ForeignBodyForReview string `json:"foreign_body_for_review,omitempty"`
	Review               string `json:"review,omitempty"`
}

// adoptReviewInstruction tells the agent how to finish an adopt-into-existing:
// the merge of the role text is agent judgment, not server mechanics.
const adoptReviewInstruction = "La canonica esistente è la fonte di verità: il suo body NON è stato toccato. Fold-check manuale: integra nella canonica i contenuti unici di `foreign_body_for_review` via memory_edit, poi sostituisci il file foreign locale con `anchor`."

func (s *Server) handlePromoteAgent(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	project, err := req.RequireString("project")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	project = strings.TrimSpace(project)
	slug := strings.TrimSpace(req.GetString("slug", ""))
	if slug == "" || strings.ContainsAny(slug, "/\\") {
		return mcp.NewToolResultError("slug must be a non-empty basename without slashes"), nil
	}
	foreign, err := req.RequireString("content")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if res := s.rejectIfHidden(project); res != nil {
		return res, nil
	}

	rel, err := s.vault.Rel(project + "/agents/" + slug + ".md")
	if err != nil {
		return mcp.NewToolResultErrorFromErr("invalid path", err), nil
	}
	tok, errRes := s.authorizeWrite(ctx, rel)
	if errRes != nil {
		return errRes, nil
	}
	if !tok.AllowsProject(project) {
		return mcp.NewToolResultErrorf("project %q is outside the token's scope %q", project, tok.ScopeLabel()), nil
	}
	// Probe and write under the per-path lock: two concurrent promotes of the
	// same slug must not both take the fresh-create branch.
	unlock := s.vault.LockPath(rel)
	defer unlock()
	if existing, err := s.vault.Load(rel); err == nil {
		if !req.GetBool("adopt_into_existing", false) {
			return mcp.NewToolResultErrorf("canonical agent %q already exists; pass adopt_into_existing:true to adopt the foreign file into it (existing body preserved, foreign body returned for manual fold-check)", rel), nil
		}
		return s.adoptIntoExisting(ctx, req, tok, rel, slug, existing.Content, []byte(foreign))
	}

	canonical := buildCanonicalAgentNote(project, slug, []byte(foreign))
	if errRes := s.checkWriteLimits(tok, len(canonical)); errRes != nil {
		return errRes, nil
	}
	if err := s.writeAndIndex(rel, []byte(canonical)); err != nil {
		return mcp.NewToolResultErrorFromErr("write failed", err), nil
	}
	s.auditWrite(ctx, audit.ActionCreate, rel, "", int64(len(canonical)))
	if fresh, lerr := s.vault.Load(rel); lerr == nil {
		s.publishNoteChange("create", rel, fresh.ETag(), true)
	}

	resp := promoteAgentResponse{Path: rel}
	profile := initprompt.Profile(strings.TrimSpace(req.GetString("profile", "claude")))
	if initprompt.SupportsAnchors(profile) {
		if ar, aerr := initprompt.RenderAgentAnchor(profile, anchorInputFromNote(rel, []byte(canonical))); aerr == nil {
			resp.Anchor = &anchorRef{Path: ar.Path, Content: ar.Content, MetaVersion: ar.MetaVersion, Canonical: rel}
		}
	}
	return mcp.NewToolResultJSON(resp)
}

// foreignAgentFile is the parsed shape of a hand-written CLI subagent file:
// frontmatter name/title/description/tools plus the system-prompt body.
type foreignAgentFile struct {
	Name  string
	Title string
	Desc  string
	Tools []string
	Body  string
}

// parseForeignAgentFile extracts the promotable metadata from a foreign local
// agent file. Name/title default to the slug; tools come from the CLI's
// comma-separated scalar form.
func parseForeignAgentFile(slug string, foreign []byte) foreignAgentFile {
	raw := parser.FrontmatterRawForPath(slug+".md", foreign)
	fields := parser.ParseFrontmatterFields(raw)
	f := foreignAgentFile{Name: slug}
	if v, ok := fields["name"].(string); ok && strings.TrimSpace(v) != "" {
		f.Name = strings.TrimSpace(v)
	}
	f.Title = f.Name
	if v, ok := fields["title"].(string); ok && strings.TrimSpace(v) != "" {
		f.Title = strings.TrimSpace(v)
	}
	if v, ok := fields["description"].(string); ok {
		f.Desc = strings.TrimSpace(v)
	}
	if v, ok := fields["tools"].(string); ok && strings.TrimSpace(v) != "" {
		// Both "a, b" and the YAML inline list "[a, b]" (Claude Code's own
		// subagent format) are accepted; the brackets would otherwise nest.
		v = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(v), "["), "]")
		for _, t := range strings.Split(v, ",") {
			if t = strings.Trim(strings.TrimSpace(t), `"'`); t != "" {
				f.Tools = append(f.Tools, t)
			}
		}
	}
	f.Body = strings.TrimLeft(parser.BodyAfterFrontmatter(foreign), "\n")
	return f
}

// harnessBlockLines renders the harness: frontmatter block preserving the
// foreign name/tools, or "" when neither deviates from the defaults.
func harnessBlockLines(slug string, f foreignAgentFile) string {
	if f.Name == slug && len(f.Tools) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("harness:\n")
	if f.Name != slug {
		b.WriteString("  name: " + f.Name + "\n")
	}
	if len(f.Tools) > 0 {
		b.WriteString("  tools: [" + strings.Join(f.Tools, ", ") + "]\n")
	}
	return b.String()
}

// buildCanonicalAgentNote turns a foreign local agent file (CLI subagent
// frontmatter name/description/tools + body) into a canonical vault type:agent
// note: gosidian frontmatter (title/description/tags/type) + an optional
// harness: block preserving the foreign name/tools, + the original body. The
// role text is not lost; it becomes the shared source of truth.
func buildCanonicalAgentNote(project, slug string, foreign []byte) string {
	f := parseForeignAgentFile(slug, foreign)

	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("title: " + f.Title + "\n")
	if f.Desc != "" {
		b.WriteString("description: " + f.Desc + "\n")
	}
	b.WriteString("tags: [" + project + ", type:agent]\n")
	b.WriteString("type: agent\n")
	b.WriteString(harnessBlockLines(slug, f))
	b.WriteString("---\n\n")
	if f.Body != "" {
		b.WriteString(f.Body)
		if !strings.HasSuffix(f.Body, "\n") {
			b.WriteString("\n")
		}
	} else {
		b.WriteString("# " + f.Title + "\n")
	}
	return b.String()
}

// adoptIntoExisting is the promote path for a canonical note that already
// exists (IMP-071). The server does only the mechanical part: it never touches
// the existing body (the canonical is the source of truth), inserts a harness:
// block only when the note lacks one, and echoes the foreign body back so the
// agent can fold-check the unique content via memory_edit.
func (s *Server) adoptIntoExisting(ctx context.Context, req mcp.CallToolRequest, tok *auth.Token, rel, slug string, canonical, foreign []byte) (*mcp.CallToolResult, error) {
	f := parseForeignAgentFile(slug, foreign)

	// Any existing harness key — block or inline scalar — must be left alone:
	// ExtractFrontmatterBlock returns nil for both "absent" and "inline", so
	// it cannot be the guard against inserting a duplicate.
	raw := parser.FrontmatterRawForPath(rel, canonical)
	if !parser.HasFrontmatterKey(raw, "harness") {
		if updated, ok := insertHarnessBlock(canonical, slug, f); ok {
			if errRes := s.checkWriteLimits(tok, len(updated)); errRes != nil {
				return errRes, nil
			}
			if err := s.writeAndIndex(rel, updated); err != nil {
				return mcp.NewToolResultErrorFromErr("write failed", err), nil
			}
			s.auditWrite(ctx, audit.ActionUpdate, rel, "", int64(len(updated)))
			if fresh, lerr := s.vault.Load(rel); lerr == nil {
				s.publishNoteChange("update", rel, fresh.ETag(), true)
			}
			canonical = updated
		}
	}

	resp := promoteAgentResponse{
		Path:                 rel,
		AdoptedIntoExisting:  true,
		ForeignBodyForReview: f.Body,
		Review:               adoptReviewInstruction,
	}
	profile := initprompt.Profile(strings.TrimSpace(req.GetString("profile", "claude")))
	if initprompt.SupportsAnchors(profile) {
		if ar, aerr := initprompt.RenderAgentAnchor(profile, anchorInputFromNote(rel, canonical)); aerr == nil {
			resp.Anchor = &anchorRef{Path: ar.Path, Content: ar.Content, MetaVersion: ar.MetaVersion, Canonical: rel}
		}
	}
	return mcp.NewToolResultJSON(resp)
}

// insertHarnessBlock adds the foreign-derived harness: block just before the
// closing frontmatter fence of an existing canonical note. Returns the original
// content and false when there is nothing to add or no frontmatter to extend.
func insertHarnessBlock(content []byte, slug string, f foreignAgentFile) ([]byte, bool) {
	block := harnessBlockLines(slug, f)
	if block == "" {
		return content, false
	}
	s := string(content)
	if !strings.HasPrefix(s, "---\n") {
		return content, false
	}
	end := strings.Index(s[4:], "\n---")
	if end < 0 {
		return content, false
	}
	at := 4 + end + 1 // start of the closing fence line
	return []byte(s[:at] + block + s[at:]), true
}
