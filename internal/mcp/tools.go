package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/index"
	idx "github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/metrics"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/vault"
	"github.com/mark3labs/mcp-go/mcp"
)

// authorizeRead returns the authenticated token if it holds the read scope,
// otherwise a result error suitable for returning from a tool handler.
func (s *Server) authorizeRead(ctx context.Context) (*auth.Token, *mcp.CallToolResult) {
	tok := s.tokenFromContext(ctx)
	if tok == nil {
		return nil, mcp.NewToolResultError("unauthorized")
	}
	if !tok.HasScope(auth.ScopeRead) {
		return nil, mcp.NewToolResultError("token lacks read scope")
	}
	return tok, nil
}

// authorizeWrite verifies the token holds the write scope and is allowed on
// the given vault-relative path.
func (s *Server) authorizeWrite(ctx context.Context, path string) (*auth.Token, *mcp.CallToolResult) {
	tok := s.tokenFromContext(ctx)
	if tok == nil {
		return nil, mcp.NewToolResultError("unauthorized")
	}
	if !tok.HasScope(auth.ScopeWrite) {
		return nil, mcp.NewToolResultError("token lacks write scope")
	}
	if !tok.AllowsPath(path) {
		return nil, mcp.NewToolResultErrorf("path %q is outside the token's project scope %q", path, tok.ProjectFilter())
	}
	return tok, nil
}

// checkWriteLimits enforces the rate limit and per-note size cap. Returns a
// CallToolResult error to be returned to the caller, or nil when the request
// may proceed. Should be called AFTER authorizeWrite (so we know the token).
func (s *Server) checkWriteLimits(tok *auth.Token, contentSize int) *mcp.CallToolResult {
	if s.maxNoteBytes > 0 && int64(contentSize) > s.maxNoteBytes {
		metrics.MCPRateLimitHits.Inc()
		return mcp.NewToolResultErrorf("note size %d exceeds limit of %d bytes", contentSize, s.maxNoteBytes)
	}
	id := ""
	if tok != nil {
		id = tok.ID
	}
	if !s.limiter.Allow(id) {
		metrics.MCPRateLimitHits.Inc()
		return mcp.NewToolResultErrorf("write rate limit exceeded for token (max %d/min)", s.limiter.maxPerMinute)
	}
	return nil
}

func (s *Server) registerTools() {
	s.impl.AddTool(mcp.NewTool("memory_search",
		mcp.WithDescription("Search notes in the vault using full-text search. Returns notes whose title or body match the query. Pass include_outline=true or include_frontmatter=true to enrich each hit with the note's heading outline or parsed frontmatter in the same call — avoids N extra memory_get_outline/memory_get_frontmatter round-trips when exploring many results. Pass `projects` (array of top-level folder names) to restrict results to a specific set; empty = vault-wide (subject to the caller's token scope)."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Free-text query. Multiple words are ANDed; prefix search is automatic.")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of hits (default 20, max 200).")),
		mcp.WithBoolean("include_outline", mcp.Description("When true, each hit also carries an `outline` array (heading level/text/id). Default false.")),
		mcp.WithBoolean("include_frontmatter", mcp.Description("When true, each hit also carries a `frontmatter` map with the parsed YAML fields. Default false.")),
		mcp.WithArray("projects", mcp.Description("Optional list of top-level folder names (e.g. [\"gosidian\",\"dockers\"]) to restrict results to. Empty = vault-wide. Scoped tokens silently intersect this list with their project scope (never expand it).")),
	), s.handleSearch)

	s.impl.AddTool(mcp.NewTool("memory_list_notes",
		mcp.WithDescription("List notes in the vault, optionally filtered by project (top-level folder)."),
		mcp.WithString("project", mcp.Description("Optional project name to scope the listing.")),
	), s.handleListNotes)

	s.impl.AddTool(mcp.NewTool("memory_list_projects",
		mcp.WithDescription("List all projects (top-level directories) in the vault with their note counts."),
	), s.handleListProjects)

	s.impl.AddTool(mcp.NewTool("memory_list_tags",
		mcp.WithDescription("List tags with usage counts. When `project` is given, counts are scoped to notes under that project prefix; otherwise they are vault-wide. Scoped tokens are forced to their project."),
		mcp.WithString("project", mcp.Description("Optional project (top-level folder) to scope the tag counts. Empty = vault-wide.")),
	), s.handleListTags)

	s.impl.AddTool(mcp.NewTool("memory_notes_by_tag",
		mcp.WithDescription("List notes that carry a specific tag. Pass `project` to restrict the results to one top-level folder. Scoped tokens are forced to their project."),
		mcp.WithString("tag", mcp.Required(), mcp.Description("Tag name, without the leading '#'.")),
		mcp.WithString("project", mcp.Description("Optional project (top-level folder) to filter by.")),
	), s.handleNotesByTag)

	s.impl.AddTool(mcp.NewTool("memory_get",
		mcp.WithDescription("Read the full content of a note by its vault-relative path (e.g. 'project/note.md')."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault-relative path to the .md file.")),
	), s.handleGet)

	s.impl.AddTool(mcp.NewTool("memory_get_section",
		mcp.WithDescription("Read a single section of a note (heading + content up to the next heading of equal or higher level). Use when a note is long and you only need one section — much cheaper than memory_get on large files."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault-relative path to the .md file.")),
		mcp.WithString("heading", mcp.Required(), mcp.Description("The heading text to retrieve, without the leading '#'s. Match is case-insensitive.")),
	), s.handleGetSection)

	s.impl.AddTool(mcp.NewTool("memory_batch_get",
		mcp.WithDescription("Read multiple notes in a single call. Use this instead of N sequential memory_get calls when reconstructing context at session start. Each entry in the result has either content (success) or error (path not found / outside scope). The call itself does not fail because one path is missing."),
		mcp.WithArray("paths", mcp.Required(), mcp.Description("Array of vault-relative paths to read (max 50).")),
	), s.handleBatchGet)

	s.impl.AddTool(mcp.NewTool("memory_recent",
		mcp.WithDescription("List the most recently modified notes. Use to catch up with 'what changed since I was last here'. Returns path, title, and mtime (unix seconds) ordered by descending mtime."),
		mcp.WithString("project", mcp.Description("Optional project (top-level folder) to scope the query. Scoped tokens are forced to their project.")),
		mcp.WithNumber("limit", mcp.Description("Max notes to return (default 20, max 500).")),
		mcp.WithString("since", mcp.Description("Lower bound on mtime. Accepts a relative duration ('1h', '24h', '7d') or an RFC3339 timestamp. Empty means 'no lower bound'.")),
	), s.handleRecent)

	s.impl.AddTool(mcp.NewTool("memory_get_frontmatter",
		mcp.WithDescription("Read only the YAML frontmatter of a note. Use this for cheap triage (check tag/status/type) before deciding whether to fetch the full body with memory_get. Returns both the raw YAML block and a parsed map of common fields."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault-relative path to the .md file.")),
	), s.handleGetFrontmatter)

	s.impl.AddTool(mcp.NewTool("memory_get_outline",
		mcp.WithDescription("Read the heading outline of a note (level + text + anchor id). Use to discover the structure of a long note so you can target a specific section with memory_get_section instead of fetching the whole body."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault-relative path to the .md file.")),
	), s.handleGetOutline)

	s.impl.AddTool(mcp.NewTool("memory_create",
		mcp.WithDescription("Create a new note at the given path. Fails if the note already exists."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault-relative path, e.g. 'project/new-note.md'.")),
		mcp.WithString("content", mcp.Required(), mcp.Description("Full markdown content of the note.")),
	), s.handleCreate)

	s.impl.AddTool(mcp.NewTool("memory_update",
		mcp.WithDescription("Overwrite an existing note's content. Fails if the note does not exist. Pass if_match (the etag returned by a previous memory_get) for optimistic locking: the call is rejected if the note has changed since you last read it."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault-relative path of the note to update.")),
		mcp.WithString("content", mcp.Required(), mcp.Description("New full markdown content.")),
		mcp.WithString("if_match", mcp.Description("Optional etag from a previous memory_get. When provided, the call fails if the note's current etag differs — reload and retry.")),
	), s.handleUpdate)

	s.impl.AddTool(mcp.NewTool("memory_append",
		mcp.WithDescription("Append content to a note. Creates the note if it does not exist. Use this to log observations incrementally. Pass if_match for optimistic locking against concurrent writes (only checked when the note already exists)."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault-relative path of the note.")),
		mcp.WithString("content", mcp.Required(), mcp.Description("Markdown to append. A blank line is inserted before the new content if the file is non-empty.")),
		mcp.WithString("if_match", mcp.Description("Optional etag from a previous memory_get. When provided and the note exists, the call fails if the note's current etag differs.")),
	), s.handleAppend)

	s.impl.AddTool(mcp.NewTool("memory_edit",
		mcp.WithDescription("Replace a specific substring inside an existing note. Same semantics as the Edit tool in Claude Code: old_string must match exactly (whitespace included). With replace_all=false (default) the match must be unique; with replace_all=true every occurrence is replaced. Use this instead of memory_update when changing only part of a large note — orders of magnitude cheaper in tokens. Pass if_match for optimistic locking."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault-relative path of the note to edit.")),
		mcp.WithString("old_string", mcp.Required(), mcp.Description("Exact substring to replace. Must match the file content verbatim.")),
		mcp.WithString("new_string", mcp.Required(), mcp.Description("Replacement text. May be empty to delete the old substring.")),
		mcp.WithBoolean("replace_all", mcp.Description("Replace every occurrence instead of failing on duplicates. Default false.")),
		mcp.WithString("if_match", mcp.Description("Optional etag from a previous memory_get. When provided, the call fails if the note's current etag differs.")),
	), s.handleEdit)

	s.impl.AddTool(mcp.NewTool("memory_delete",
		mcp.WithDescription("Delete a note from the vault and the index."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault-relative path of the note to delete.")),
	), s.handleDelete)

	s.impl.AddTool(mcp.NewTool("memory_rename_note",
		mcp.WithDescription("Rename a note. Updates the index and rewrites wiki-links in other notes that referenced the old name. Both the source and destination paths must be inside the token's scope."),
		mcp.WithString("from", mcp.Required(), mcp.Description("Current vault-relative path of the note.")),
		mcp.WithString("to", mcp.Required(), mcp.Description("New vault-relative path. The .md extension is added if missing.")),
	), s.handleRenameNote)

	s.impl.AddTool(mcp.NewTool("memory_move_note",
		mcp.WithDescription("Move a note to a different project. Both the current and target locations must be inside the token's scope."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Current vault-relative path of the note.")),
		mcp.WithString("project", mcp.Required(), mcp.Description("Destination project name. Empty string moves the note to the vault root.")),
	), s.handleMoveNote)

	s.impl.AddTool(mcp.NewTool("memory_create_project",
		mcp.WithDescription("Create a new project (top-level folder) in the vault."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Project name (no slashes, no leading dot).")),
	), s.handleCreateProject)

	s.impl.AddTool(mcp.NewTool("memory_delete_project",
		mcp.WithDescription("Delete a project (top-level folder) and all notes inside, recursively. Admin tokens only."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Project name.")),
	), s.handleDeleteProject)

	s.impl.AddTool(mcp.NewTool("memory_rename_project",
		mcp.WithDescription("Rename a project (top-level folder). All note paths under the project are reindexed under the new prefix. Admin tokens only."),
		mcp.WithString("from", mcp.Required(), mcp.Description("Current project name.")),
		mcp.WithString("to", mcp.Required(), mcp.Description("New project name.")),
	), s.handleRenameProject)

	s.impl.AddTool(mcp.NewTool("memory_backlinks",
		mcp.WithDescription("List notes that reference (via wiki-link) the given note path."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault-relative path of the target note.")),
	), s.handleBacklinks)

	s.impl.AddTool(mcp.NewTool("memory_outlinks",
		mcp.WithDescription("List wiki-links leaving the given note, with their resolved paths (if any). Pass include_cross_project=true to also flag links that point into a different top-level project via `cross_project: true` on each entry — scoped tokens always ignore this flag (they cannot see outside their project)."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault-relative path of the source note.")),
		mcp.WithBoolean("include_cross_project", mcp.Description("When true, each outlink carries `cross_project: true` if its resolved path is in a different top-level project. Default false.")),
	), s.handleOutlinks)

	s.registerAttachmentTools()
	s.registerAuditTools()
	s.registerBootstrapTool()
	s.registerDiscoveryTools()
	s.registerImportanceTool()
	s.registerHandoffTools()
	s.registerCompactTool()
	s.registerSelfStatsTool()
	s.registerScaffoldTool()
	s.registerBootstrapTemplatesTool()
	s.registerRefreshHotTool()
	s.registerTodosTool()
	s.registerLintTool()
	s.registerAskTool()
}

// ---- handlers ----

type searchHit struct {
	Path        string           `json:"path"`
	Title       string           `json:"title"`
	Snippet     string           `json:"snippet"`
	Outline     []outlineHeading `json:"outline,omitempty"`
	Frontmatter map[string]any   `json:"frontmatter,omitempty"`
}

func (s *Server) handleSearch(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	q, err := req.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := req.GetInt("limit", 20)
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	includeOutline := req.GetBool("include_outline", false)
	includeFrontmatter := req.GetBool("include_frontmatter", false)

	// Optional project filter. Scoped tokens silently intersect with their
	// project (never expand). We fetch a few extra hits when a filter is
	// active so the final result still has up to `limit` entries after
	// filtering.
	filter := buildProjectsFilter(req.GetStringSlice("projects", nil), tok.ProjectFilter())
	fetchLimit := limit
	if filter.active {
		fetchLimit = limit * 4
		if fetchLimit > 500 {
			fetchLimit = 500
		}
	}
	// Short-circuit: active filter with empty allowed list → zero hits.
	if filter.active && len(filter.allowed) == 0 {
		return mcp.NewToolResultJSON(map[string]any{"hits": []searchHit{}})
	}

	hits, err := s.index.Search(q, fetchLimit)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("search failed", err), nil
	}
	out := make([]searchHit, 0, len(hits))
	for _, h := range hits {
		if !tok.AllowsPath(h.Path) {
			continue
		}
		if !filter.matches(h.Path) {
			continue
		}
		if len(out) >= limit {
			break
		}
		hit := searchHit{
			Path:    h.Path,
			Title:   h.Title,
			Snippet: stripMarkTags(h.Snippet),
		}
		if includeOutline || includeFrontmatter {
			// One load per hit, LRU cache absorbs repeats and subsequent calls.
			note, loadErr := s.vault.Load(h.Path)
			if loadErr == nil {
				if includeOutline {
					headings := parser.ExtractHeadings(note.Content)
					hit.Outline = make([]outlineHeading, 0, len(headings))
					for _, hd := range headings {
						hit.Outline = append(hit.Outline, outlineHeading{
							Level: hd.Level,
							Text:  hd.Text,
							ID:    hd.ID,
						})
					}
				}
				if includeFrontmatter {
					raw := parser.ExtractFrontmatterRaw(note.Content)
					hit.Frontmatter = parser.ParseFrontmatterFields(raw)
				}
			}
		}
		out = append(out, hit)
	}
	return mcp.NewToolResultJSON(map[string]any{"hits": out})
}

type noteRef struct {
	Path  string `json:"path"`
	Title string `json:"title"`
}

func (s *Server) handleListNotes(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	project := req.GetString("project", "")
	// If the token is scoped, force the filter to its project and reject any
	// attempt to list a different one.
	if tok.ProjectFilter() != "" {
		if project != "" && project != tok.ProjectFilter() {
			return mcp.NewToolResultErrorf("project %q is outside the token's scope %q", project, tok.ProjectFilter()), nil
		}
		project = tok.ProjectFilter()
	}

	var notes []index.NoteRow
	var err error
	if project == "" {
		notes, err = s.index.AllNotes()
	} else {
		notes, err = s.index.NotesByPrefix(project)
	}
	if err != nil {
		return mcp.NewToolResultErrorFromErr("list failed", err), nil
	}
	out := make([]noteRef, 0, len(notes))
	for _, n := range notes {
		if !tok.AllowsPath(n.Path) {
			continue
		}
		out = append(out, noteRef{Path: n.Path, Title: n.Title})
	}
	return mcp.NewToolResultJSON(map[string]any{"notes": out})
}

type projectEntry struct {
	Name      string `json:"name"`
	NoteCount int    `json:"noteCount"`
}

func (s *Server) handleListProjects(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	projs, err := s.vault.Projects()
	if err != nil {
		return mcp.NewToolResultErrorFromErr("projects failed", err), nil
	}
	out := make([]projectEntry, 0, len(projs))
	for _, p := range projs {
		if tok.ProjectFilter() != "" && p.Name != tok.ProjectFilter() {
			continue
		}
		out = append(out, projectEntry{Name: p.Name, NoteCount: p.NoteCount})
	}
	return mcp.NewToolResultJSON(map[string]any{"projects": out})
}

type tagEntry struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

func (s *Server) handleListTags(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	project := strings.TrimSpace(req.GetString("project", ""))
	// Scoped tokens are forced to their project (parity with memory_list_notes).
	if scope := tok.ProjectFilter(); scope != "" {
		if project != "" && project != scope {
			return mcp.NewToolResultErrorf("project %q is outside the token's scope %q", project, scope), nil
		}
		project = scope
	}

	var (
		tags []index.TagCount
		err  error
	)
	if project == "" {
		tags, err = s.index.Tags()
	} else {
		tags, err = s.index.TagsByProject(project)
	}
	if err != nil {
		return mcp.NewToolResultErrorFromErr("tags failed", err), nil
	}
	out := make([]tagEntry, 0, len(tags))
	for _, t := range tags {
		out = append(out, tagEntry{Tag: t.Tag, Count: t.Count})
	}
	return mcp.NewToolResultJSON(map[string]any{"tags": out})
}

func (s *Server) handleNotesByTag(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	tag, err := req.RequireString("tag")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	project := strings.TrimSpace(req.GetString("project", ""))
	if scope := tok.ProjectFilter(); scope != "" {
		if project != "" && project != scope {
			return mcp.NewToolResultErrorf("project %q is outside the token's scope %q", project, scope), nil
		}
		project = scope
	}

	var (
		notes []index.NoteRow
	)
	if project != "" {
		notes, err = s.index.NotesByTagInProject(tag, project)
	} else {
		notes, err = s.index.NotesByTag(tag)
	}
	if err != nil {
		return mcp.NewToolResultErrorFromErr("lookup failed", err), nil
	}
	out := make([]noteRef, 0, len(notes))
	for _, n := range notes {
		if !tok.AllowsPath(n.Path) {
			continue
		}
		out = append(out, noteRef{Path: n.Path, Title: n.Title})
	}
	return mcp.NewToolResultJSON(map[string]any{"notes": out})
}

type noteContent struct {
	Path    string `json:"path"`
	Title   string `json:"title"`
	Content string `json:"content"`
	ETag    string `json:"etag"`
}

func (s *Server) handleGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if !tok.AllowsPath(path) {
		return mcp.NewToolResultErrorf("path %q is outside the token's project scope", path), nil
	}
	note, err := s.vault.Load(path)
	if err != nil {
		return mcp.NewToolResultErrorf("cannot read %q: %v", path, err), nil
	}
	return mcp.NewToolResultJSON(noteContent{
		Path:    note.Path,
		Title:   note.Title,
		Content: string(note.Content),
		ETag:    note.ETag(),
	})
}

// checkIfMatch verifies that the caller's expected etag matches the current
// note state. Returns nil on match (or when if_match is empty). Returns a
// tool error result on mismatch.
func checkIfMatch(note *vault.Note, ifMatch string) *mcp.CallToolResult {
	if ifMatch == "" {
		return nil
	}
	current := note.ETag()
	if current != ifMatch {
		return mcp.NewToolResultErrorf(
			"etag mismatch: expected %q but note is now %q (reload before retrying)",
			ifMatch, current,
		)
	}
	return nil
}

type pathResult struct {
	Path string `json:"path"`
}

func (s *Server) handleGetSection(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	heading, err := req.RequireString("heading")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if !tok.AllowsPath(path) {
		return mcp.NewToolResultErrorf("path %q is outside the token's project scope", path), nil
	}
	note, err := s.vault.Load(path)
	if err != nil {
		return mcp.NewToolResultErrorf("cannot read %q: %v", path, err), nil
	}
	section := parser.ExtractSection(note.Content, heading)
	if section == "" {
		return mcp.NewToolResultErrorf("heading %q not found in %q", heading, path), nil
	}
	return mcp.NewToolResultJSON(map[string]any{
		"path":    path,
		"heading": heading,
		"content": section,
		"etag":    note.ETag(),
	})
}

func (s *Server) handleCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	content, err := req.RequireString("content")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	rel, err := s.vault.Rel(path)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("invalid path", err), nil
	}
	tok, errRes := s.authorizeWrite(ctx, rel)
	if errRes != nil {
		return errRes, nil
	}
	if errRes := s.checkWriteLimits(tok, len(content)); errRes != nil {
		return errRes, nil
	}
	if _, err := s.vault.Load(rel); err == nil {
		return mcp.NewToolResultErrorf("note %q already exists", rel), nil
	}
	if err := s.writeAndIndex(rel, []byte(content)); err != nil {
		return mcp.NewToolResultErrorFromErr("write failed", err), nil
	}
	s.auditWrite(ctx, audit.ActionCreate, rel, "", int64(len(content)))
	return mcp.NewToolResultJSON(pathResult{Path: rel})
}

func (s *Server) handleUpdate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	content, err := req.RequireString("content")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	rel, err := s.vault.Rel(path)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("invalid path", err), nil
	}
	tok, errRes := s.authorizeWrite(ctx, rel)
	if errRes != nil {
		return errRes, nil
	}
	if errRes := s.checkWriteLimits(tok, len(content)); errRes != nil {
		return errRes, nil
	}
	existing, err := s.vault.Load(rel)
	if err != nil {
		return mcp.NewToolResultErrorf("note %q does not exist", rel), nil
	}
	if errRes := checkIfMatch(existing, req.GetString("if_match", "")); errRes != nil {
		return errRes, nil
	}
	if err := s.writeAndIndex(rel, []byte(content)); err != nil {
		return mcp.NewToolResultErrorFromErr("write failed", err), nil
	}
	s.auditWrite(ctx, audit.ActionUpdate, rel, "", int64(len(content)))
	// Return the new etag so the caller can pipeline further edits without a
	// re-read. Note: reloading here is cheap (likely cache hit on the write).
	out := map[string]any{"path": rel}
	if fresh, err := s.vault.Load(rel); err == nil {
		out["etag"] = fresh.ETag()
	}
	return mcp.NewToolResultJSON(out)
}

func (s *Server) handleAppend(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	addition, err := req.RequireString("content")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	rel, err := s.vault.Rel(path)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("invalid path", err), nil
	}
	tok, errRes := s.authorizeWrite(ctx, rel)
	if errRes != nil {
		return errRes, nil
	}
	var existing []byte
	ifMatch := req.GetString("if_match", "")
	if note, err := s.vault.Load(rel); err == nil {
		existing = note.Content
		if errRes := checkIfMatch(note, ifMatch); errRes != nil {
			return errRes, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) && !strings.Contains(err.Error(), "no such file") {
		return mcp.NewToolResultErrorFromErr("load failed", err), nil
	} else if ifMatch != "" {
		// Client provided if_match but the note doesn't exist — that's a
		// mismatch too (they thought it was there).
		return mcp.NewToolResultErrorf("etag mismatch: note %q does not exist", rel), nil
	}
	var merged []byte
	if len(existing) == 0 {
		merged = []byte(addition)
	} else {
		sep := "\n"
		if !strings.HasSuffix(string(existing), "\n") {
			sep = "\n\n"
		} else if !strings.HasSuffix(string(existing), "\n\n") {
			sep = "\n"
		} else {
			sep = ""
		}
		merged = []byte(string(existing) + sep + addition)
	}
	if errRes := s.checkWriteLimits(tok, len(merged)); errRes != nil {
		return errRes, nil
	}
	if err := s.writeAndIndex(rel, merged); err != nil {
		return mcp.NewToolResultErrorFromErr("write failed", err), nil
	}
	s.auditWrite(ctx, audit.ActionAppend, rel, "", int64(len(merged)))
	out := map[string]any{"path": rel}
	if fresh, err := s.vault.Load(rel); err == nil {
		out["etag"] = fresh.ETag()
	}
	return mcp.NewToolResultJSON(out)
}

func (s *Server) handleEdit(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	oldS, err := req.RequireString("old_string")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	newS, err := req.RequireString("new_string")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if oldS == "" {
		return mcp.NewToolResultError("old_string must not be empty"), nil
	}
	if oldS == newS {
		return mcp.NewToolResultError("old_string and new_string are identical"), nil
	}
	replaceAll := req.GetBool("replace_all", false)

	rel, err := s.vault.Rel(path)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("invalid path", err), nil
	}
	tok, errRes := s.authorizeWrite(ctx, rel)
	if errRes != nil {
		return errRes, nil
	}
	note, err := s.vault.Load(rel)
	if err != nil {
		return mcp.NewToolResultErrorf("note %q does not exist", rel), nil
	}
	if errRes := checkIfMatch(note, req.GetString("if_match", "")); errRes != nil {
		return errRes, nil
	}
	body := string(note.Content)

	count := strings.Count(body, oldS)
	if count == 0 {
		return mcp.NewToolResultErrorf("old_string not found in %q", rel), nil
	}
	if count > 1 && !replaceAll {
		return mcp.NewToolResultErrorf("old_string matches %d occurrences in %q; pass replace_all=true or include more context to make it unique", count, rel), nil
	}

	var updated string
	if replaceAll {
		updated = strings.ReplaceAll(body, oldS, newS)
	} else {
		updated = strings.Replace(body, oldS, newS, 1)
	}

	if errRes := s.checkWriteLimits(tok, len(updated)); errRes != nil {
		return errRes, nil
	}
	if err := s.writeAndIndex(rel, []byte(updated)); err != nil {
		return mcp.NewToolResultErrorFromErr("write failed", err), nil
	}
	s.auditWrite(ctx, audit.ActionUpdate, rel, "", int64(len(updated)))
	out := map[string]any{
		"path":         rel,
		"replacements": count,
		"new_size":     len(updated),
	}
	if fresh, err := s.vault.Load(rel); err == nil {
		out["etag"] = fresh.ETag()
	}
	return mcp.NewToolResultJSON(out)
}

func (s *Server) handleDelete(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	rel, err := s.vault.Rel(path)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("invalid path", err), nil
	}
	if _, errRes := s.authorizeWrite(ctx, rel); errRes != nil {
		return errRes, nil
	}
	if err := s.vault.Delete(rel); err != nil {
		return mcp.NewToolResultErrorFromErr("delete failed", err), nil
	}
	if err := s.index.Delete(rel); err != nil {
		return mcp.NewToolResultErrorFromErr("index delete failed", err), nil
	}
	s.auditWrite(ctx, audit.ActionDelete, rel, "", 0)
	return mcp.NewToolResultJSON(map[string]any{"deleted": true, "path": rel})
}

func (s *Server) handleCreateProject(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok := s.tokenFromContext(ctx)
	if tok == nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	if !tok.HasScope(auth.ScopeWrite) {
		return mcp.NewToolResultError("token lacks write scope"), nil
	}
	// Only admin tokens may create projects — scoped tokens are confined to
	// their existing project.
	if tok.ProjectFilter() != "" {
		return mcp.NewToolResultError("project-scoped tokens cannot create projects"), nil
	}
	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	clean, err := s.vault.CreateProject(name)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("create project failed", err), nil
	}
	s.auditWrite(ctx, audit.ActionCreateProject, clean, "", 0)
	return mcp.NewToolResultJSON(map[string]any{"name": clean})
}

func (s *Server) handleRenameNote(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	from, err := req.RequireString("from")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	to, err := req.RequireString("to")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	fromRel, err := s.vault.Rel(from)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("invalid from", err), nil
	}
	toRel, err := s.vault.Rel(to)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("invalid to", err), nil
	}
	// Both endpoints must be inside the token's scope (write).
	if _, errRes := s.authorizeWrite(ctx, fromRel); errRes != nil {
		return errRes, nil
	}
	if _, errRes := s.authorizeWrite(ctx, toRel); errRes != nil {
		return errRes, nil
	}
	rewritten, err := s.vault.RenameNote(s.index, fromRel, toRel)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("rename failed", err), nil
	}
	// RenameNote may have appended .md to to; recompute the canonical path.
	canonical := toRel
	if !strings.HasSuffix(strings.ToLower(canonical), ".md") {
		canonical += ".md"
	}
	s.auditWrite(ctx, audit.ActionRename, fromRel, canonical, 0)
	return mcp.NewToolResultJSON(map[string]any{
		"from":      fromRel,
		"to":        canonical,
		"rewritten": rewritten,
	})
}

func (s *Server) handleMoveNote(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	from, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	project := req.GetString("project", "")
	fromRel, err := s.vault.Rel(from)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("invalid path", err), nil
	}
	if _, errRes := s.authorizeWrite(ctx, fromRel); errRes != nil {
		return errRes, nil
	}
	// Compute destination and check scope on the target side as well.
	target := strings.TrimSuffix(strings.TrimPrefix(fromRel, ""), "")
	base := target
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	dest := base
	if project != "" {
		dest = project + "/" + base
	}
	if _, errRes := s.authorizeWrite(ctx, dest); errRes != nil {
		return errRes, nil
	}
	rewritten, err := s.vault.MoveNote(s.index, fromRel, project)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("move failed", err), nil
	}
	s.auditWrite(ctx, audit.ActionRename, fromRel, dest, 0)
	return mcp.NewToolResultJSON(map[string]any{
		"from":      fromRel,
		"to":        dest,
		"rewritten": rewritten,
	})
}

func (s *Server) handleRenameProject(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok := s.tokenFromContext(ctx)
	if tok == nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	if !tok.HasScope(auth.ScopeWrite) {
		return mcp.NewToolResultError("token lacks write scope"), nil
	}
	if tok.ProjectFilter() != "" {
		return mcp.NewToolResultError("project-scoped tokens cannot rename projects"), nil
	}
	from, err := req.RequireString("from")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	to, err := req.RequireString("to")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := s.vault.RenameProject(s.index, from, to); err != nil {
		return mcp.NewToolResultErrorFromErr("rename project failed", err), nil
	}
	s.auditWrite(ctx, audit.ActionRenameProject, from, to, 0)
	return mcp.NewToolResultJSON(map[string]any{"from": from, "to": to})
}

func (s *Server) handleDeleteProject(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok := s.tokenFromContext(ctx)
	if tok == nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	if !tok.HasScope(auth.ScopeWrite) {
		return mcp.NewToolResultError("token lacks write scope"), nil
	}
	if tok.ProjectFilter() != "" {
		return mcp.NewToolResultError("project-scoped tokens cannot delete projects"), nil
	}
	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	removed, err := s.vault.DeleteProject(name)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("delete project failed", err), nil
	}
	for _, p := range removed {
		_ = s.index.Delete(p)
	}
	s.auditWrite(ctx, audit.ActionDeleteProject, name, "", int64(len(removed)))
	return mcp.NewToolResultJSON(map[string]any{
		"deleted":       true,
		"name":          name,
		"removed_notes": removed,
	})
}

func (s *Server) handleBacklinks(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if !tok.AllowsPath(path) {
		return mcp.NewToolResultErrorf("path %q is outside the token's scope", path), nil
	}
	bl, err := s.index.Backlinks(path)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("backlinks failed", err), nil
	}
	out := make([]noteRef, 0, len(bl))
	for _, b := range bl {
		if !tok.AllowsPath(b.Path) {
			continue
		}
		out = append(out, noteRef{Path: b.Path, Title: b.Title})
	}
	return mcp.NewToolResultJSON(map[string]any{"backlinks": out})
}

type outlinkEntry struct {
	Target       string `json:"target"`
	ResolvedPath string `json:"resolvedPath"`
	Alias        string `json:"alias,omitempty"`
	CrossProject bool   `json:"cross_project,omitempty"`
}

func (s *Server) handleOutlinks(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if !tok.AllowsPath(path) {
		return mcp.NewToolResultErrorf("path %q is outside the token's scope", path), nil
	}
	// Scoped tokens never see cross-project info (they cannot access other
	// projects anyway), so ignore the flag.
	crossProject := req.GetBool("include_cross_project", false) && tok.ProjectFilter() == ""
	srcProject := topLevelProject(path)

	outs, err := s.index.Outlinks(path)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("outlinks failed", err), nil
	}
	result := make([]outlinkEntry, 0, len(outs))
	for _, o := range outs {
		entry := outlinkEntry{
			Target:       o.Target,
			ResolvedPath: o.TargetPath,
			Alias:        o.Alias,
		}
		if crossProject && o.TargetPath != "" {
			if tgt := topLevelProject(o.TargetPath); tgt != "" && tgt != srcProject {
				entry.CrossProject = true
			}
		}
		result = append(result, entry)
	}
	return mcp.NewToolResultJSON(map[string]any{"outlinks": result})
}

// topLevelProject returns the first path segment of a vault-relative path, or
// empty when the path has no `/` separator (root-level file).
func topLevelProject(p string) string {
	if i := strings.IndexByte(p, '/'); i > 0 {
		return p[:i]
	}
	return ""
}

// ---- batch & triage reads ----

type batchGetEntry struct {
	Path    string `json:"path"`
	Title   string `json:"title,omitempty"`
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) handleBatchGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	paths := req.GetStringSlice("paths", nil)
	if len(paths) == 0 {
		return mcp.NewToolResultError("paths must be a non-empty array"), nil
	}
	if len(paths) > 50 {
		return mcp.NewToolResultErrorf("too many paths: %d (max 50)", len(paths)), nil
	}
	out := make([]batchGetEntry, 0, len(paths))
	for _, p := range paths {
		entry := batchGetEntry{Path: p}
		if !tok.AllowsPath(p) {
			entry.Error = "outside token scope"
			out = append(out, entry)
			continue
		}
		note, err := s.vault.Load(p)
		if err != nil {
			entry.Error = "not found"
			out = append(out, entry)
			continue
		}
		entry.Title = note.Title
		entry.Content = string(note.Content)
		out = append(out, entry)
	}
	return mcp.NewToolResultJSON(map[string]any{"results": out})
}

type recentNoteResponse struct {
	Path  string `json:"path"`
	Title string `json:"title"`
	Mtime int64  `json:"mtime"`
}

func (s *Server) handleRecent(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	project := req.GetString("project", "")
	if tok.ProjectFilter() != "" {
		if project != "" && project != tok.ProjectFilter() {
			return mcp.NewToolResultErrorf("project %q is outside the token's scope %q", project, tok.ProjectFilter()), nil
		}
		project = tok.ProjectFilter()
	}
	limit := req.GetInt("limit", 20)
	if limit <= 0 || limit > 500 {
		limit = 20
	}

	var since int64
	if raw := strings.TrimSpace(req.GetString("since", "")); raw != "" {
		// Try relative duration first (e.g. "24h", "7d"). We support 'd' as
		// days by rewriting to hours, since time.ParseDuration doesn't.
		dur := raw
		if strings.HasSuffix(dur, "d") {
			var days int
			if _, err := fmt.Sscanf(dur, "%dd", &days); err == nil {
				dur = fmt.Sprintf("%dh", days*24)
			}
		}
		if d, err := time.ParseDuration(dur); err == nil {
			since = time.Now().Add(-d).Unix()
		} else if t, err := time.Parse(time.RFC3339, raw); err == nil {
			since = t.Unix()
		} else {
			return mcp.NewToolResultErrorf("since %q: expected duration (24h, 7d) or RFC3339 timestamp", raw), nil
		}
	}

	notes, err := s.index.RecentNotes(project, since, limit)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("recent failed", err), nil
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

func (s *Server) handleGetFrontmatter(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if !tok.AllowsPath(path) {
		return mcp.NewToolResultErrorf("path %q is outside the token's scope", path), nil
	}
	note, err := s.vault.Load(path)
	if err != nil {
		return mcp.NewToolResultErrorf("cannot read %q: %v", path, err), nil
	}
	raw := parser.ExtractFrontmatterRaw(note.Content)
	parsed := parser.ParseFrontmatterFields(raw)
	return mcp.NewToolResultJSON(map[string]any{
		"path":   path,
		"raw":    raw,
		"parsed": parsed,
		"etag":   note.ETag(),
	})
}

type outlineHeading struct {
	Level int    `json:"level"`
	Text  string `json:"text"`
	ID    string `json:"id"`
}

func (s *Server) handleGetOutline(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	path, err := req.RequireString("path")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if !tok.AllowsPath(path) {
		return mcp.NewToolResultErrorf("path %q is outside the token's scope", path), nil
	}
	note, err := s.vault.Load(path)
	if err != nil {
		return mcp.NewToolResultErrorf("cannot read %q: %v", path, err), nil
	}
	heads := parser.ExtractHeadings(note.Content)
	out := make([]outlineHeading, 0, len(heads))
	for _, h := range heads {
		out = append(out, outlineHeading{Level: h.Level, Text: h.Text, ID: h.ID})
	}
	return mcp.NewToolResultJSON(map[string]any{
		"path":     path,
		"headings": out,
		"etag":     note.ETag(),
	})
}

// writeAndIndex persists the note and upserts it into the index synchronously
// so that a search immediately after the write reflects the change.
func (s *Server) writeAndIndex(rel string, content []byte) error {
	if err := s.vault.Save(rel, content); err != nil {
		return err
	}
	note, err := s.vault.Load(rel)
	if err != nil {
		return fmt.Errorf("reload: %w", err)
	}
	return s.index.Upsert(idx.NoteDoc{
		Path:    note.Path,
		Title:   note.Title,
		Body:    string(note.Content),
		ModTime: note.ModTime.Unix(),
		Size:    note.Size,
	})
}

// stripMarkTags removes the <mark>…</mark> highlights from FTS snippets so
// the text is clean when returned as JSON.
func stripMarkTags(s string) string {
	s = strings.ReplaceAll(s, "<mark>", "")
	s = strings.ReplaceAll(s, "</mark>", "")
	return s
}

// projectsFilter represents the effective project restriction for a
// search call. `active=false` means "no filter" (everything the token
// can see). `active=true` + empty `allowed` means "match nothing" (the
// intersection between the caller's requested list and the token's scope
// was empty — silently honoured, no error).
type projectsFilter struct {
	active  bool
	allowed []string
}

func (f projectsFilter) matches(path string) bool {
	if !f.active {
		return true
	}
	for _, p := range f.allowed {
		if strings.HasPrefix(path, p+"/") || path == p {
			return true
		}
	}
	return false
}

// buildProjectsFilter deduplicates, trims and intersects the requested
// list with the token's own scope.
//
//   - Admin token (scope==""), empty request → no filter.
//   - Admin token, non-empty request → filter to the requested set.
//   - Scoped token, empty request → filter to the scope.
//   - Scoped token, non-empty request → strict intersection. If the
//     scope is outside the request, the effective filter matches no
//     path (silent "no hits" — never an error).
func buildProjectsFilter(requested []string, scope string) projectsFilter {
	seen := map[string]struct{}{}
	clean := make([]string, 0, len(requested))
	for _, p := range requested {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		clean = append(clean, p)
	}
	if scope == "" {
		return projectsFilter{active: len(clean) > 0, allowed: clean}
	}
	if len(clean) == 0 {
		return projectsFilter{active: true, allowed: []string{scope}}
	}
	if _, ok := seen[scope]; ok {
		return projectsFilter{active: true, allowed: []string{scope}}
	}
	// Scoped token asked for projects outside its scope — silent empty.
	return projectsFilter{active: true, allowed: nil}
}
