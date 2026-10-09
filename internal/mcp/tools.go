package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/dbschema"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/metrics"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/projectops"
	"github.com/gosidian/gosidian/internal/vault"
	"github.com/gosidian/gosidian/internal/views"
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
		return nil, mcp.NewToolResultErrorf("path %q is outside the token's project scope %q", path, tok.ScopeLabel())
	}
	if !tok.AllowsWrite(path) {
		return nil, mcp.NewToolResultErrorf("the account behind this token may only read project %q", auth.ProjectOf(path))
	}
	return tok, nil
}

// checkWriteLimits enforces the rate limit and per-note size cap. Returns a
// CallToolResult error to be returned to the caller, or nil when the request
// may proceed. Should be called AFTER authorizeWrite (so we know the token).
// Every mutation goes through it — deletes, renames and uploads included,
// with contentSize 0 when there is no body to cap — so the budget the
// operator configured really bounds what a runaway agent can do.
func (s *Server) checkWriteLimits(ctx context.Context, tok *auth.Token, contentSize int) *mcp.CallToolResult {
	if msg, _ := s.writeLimitViolation(ctx, tok, contentSize); msg != "" {
		return mcp.NewToolResultError(msg)
	}
	return nil
}

// writeLimitViolation is checkWriteLimits for non-MCP callers (the HTTP
// upload endpoints): it returns the rejection message, or "" when the write
// may proceed, and for a rate refusal how long until a place frees up (the
// HTTP callers send it as Retry-After).
func (s *Server) writeLimitViolation(ctx context.Context, tok *auth.Token, contentSize int) (string, time.Duration) {
	if s.maxNoteBytes > 0 && int64(contentSize) > s.maxNoteBytes {
		metrics.MCPRateLimitHits.Inc()
		return fmt.Sprintf("note size %d exceeds limit of %d bytes. A body this large usually belongs elsewhere: long tabular data → a table note, an image → a media note, a big generated file already on disk → memory_ingest (bridge_filename/source_path, or transfer:\"http\" for a single-use upload URL)", contentSize, s.maxNoteBytes), 0
	}
	charge := writeChargeOf(ctx)
	if charge != nil && charge.taken.Load() {
		return "", 0
	}
	id := ""
	if tok != nil {
		id = tok.ID
	}
	ok, wait, tokenLevel := s.limiter.Allow(id, sessionFromContext(ctx))
	if ok {
		if charge != nil {
			charge.taken.Store(true)
		}
		return "", 0
	}
	metrics.MCPRateLimitHits.Inc()
	secs := retrySeconds(wait)
	if tokenLevel {
		return fmt.Sprintf("write rate limit exceeded for the token: at most %d writes per minute across all its sessions (each session up to %d). Nothing was written. Retry in %ds: wait, then repeat the same call", s.limiter.tokenMax(), s.limiter.maxPerMinute, secs), wait
	}
	return fmt.Sprintf("write rate limit exceeded for this session: at most %d writes per minute (the token allows %d across its sessions). Nothing was written. Retry in %ds: wait, then repeat the same call", s.limiter.maxPerMinute, s.limiter.tokenMax(), secs), wait
}

// writeRateLimited answers an HTTP write refused by writeLimitViolation: 429
// with Retry-After for a rate refusal (wait > 0), else the given status.
func writeRateLimited(w http.ResponseWriter, status int, msg string, wait time.Duration) {
	if wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(retrySeconds(wait)))
		status = http.StatusTooManyRequests
	}
	writeJSONError(w, status, msg)
}

func (s *Server) registerTools() {
	s.impl.AddTool(mcp.NewTool("memory_search",
		mcp.WithDescription("Search notes in the vault using full-text search. Hits are ranked by text match (title weighs most, then frontmatter such as tags and description, then body) with small boosts for backlinks, importance, recent edits and the pinned tag; each hit carries `score` (relative to the best hit of this response, 1 = best) and `why` (the signals behind it). A link is searchable by the words it shows (its alias, or the linked note's name and heading), not by its folder path: to find the notes that link a page use memory_backlinks. Snippets are short excerpts around the match: before reporting a detail (a number, a date, a list), read the note with memory_get or memory_get_section. The search is lexical, not semantic (each word also matches its English inflections, so \"retry\" finds \"retries\"): when hits are few or missing, call again with `any_of` listing synonyms, translations (Italian/English) or other forms of the query — the lists are fused and `why` shows which phrasing matched. Pass include_outline=true or include_frontmatter=true to enrich each hit with the note's heading outline or parsed frontmatter in the same call — avoids N extra memory_get_outline/memory_get_frontmatter round-trips when exploring many results. Pass `projects` (array of top-level folder names) to restrict results to a specific set; empty = vault-wide (subject to the caller's token scope). `project` is shorthand for a single name; given together with `projects`, the two are merged (the result notes it)."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Free-text query. Multiple words are ANDed; prefix search is automatic.")),
		mcp.WithArray("any_of", mcp.Description("Optional alternative phrasings searched alongside `query` (max 8), e.g. [\"credenziali\", \"secrets\"] for query \"segreti\". A note matching any of them is returned; notes matched by several phrasings rank higher.")),
		mcp.WithNumber("limit", mcp.Description("Maximum number of hits (default 20, max 200). When more notes match, the result carries `truncated: true`.")),
		mcp.WithBoolean("include_outline", mcp.Description("When true, each hit also carries an `outline` array (heading level/text/id). Default false.")),
		mcp.WithBoolean("include_frontmatter", mcp.Description("When true, each hit also carries a `frontmatter` map with the parsed YAML fields. Default false.")),
		mcp.WithArray("projects", mcp.Description("Optional list of top-level folder names (e.g. [\"gosidian\",\"dockers\"]) to restrict results to. Empty = vault-wide. Scoped tokens silently intersect this list with their project scope (never expand it).")),
		mcp.WithString("project", mcp.Description("A single top-level folder name: shorthand for projects: [project], merged with projects when both are given.")),
	), s.handleSearch)

	s.impl.AddTool(mcp.NewTool("memory_list_notes",
		mcp.WithDescription("List notes in the vault, optionally filtered by project (top-level folder)."),
		mcp.WithString("project", mcp.Description("Optional project name to scope the listing.")),
	), s.handleListNotes)

	s.impl.AddTool(mcp.NewTool("memory_list_projects",
		mcp.WithDescription("List all projects (top-level directories) in the vault with their note counts."),
	), s.handleListProjects)

	s.impl.AddTool(mcp.NewTool("memory_list_tags",
		mcp.WithDescription("List tags with usage counts. When `project` is given, counts are scoped to notes under that project prefix; otherwise they are vault-wide. "+scopedProjectNote),
		mcp.WithString("project", mcp.Description("Optional project (top-level folder) to scope the tag counts. Empty = vault-wide.")),
	), s.handleListTags)

	s.impl.AddTool(mcp.NewTool("memory_notes_by_tag",
		mcp.WithDescription("List notes that carry a specific tag. Pass `project` to restrict the results to one top-level folder. "+scopedProjectNote),
		mcp.WithString("tag", mcp.Required(), mcp.Description("Tag name, without the leading '#'.")),
		mcp.WithString("project", mcp.Description("Optional project (top-level folder) to filter by.")),
	), s.handleNotesByTag)

	s.impl.AddTool(mcp.NewTool("memory_get",
		mcp.WithDescription("Read a note by its vault-relative path (e.g. 'project/note.md'). Oversize guard: when the body exceeds 24 KiB (and raw is not set) the response is truncated — frontmatter + heading outline + the first chunk, with truncated:true, the full size, and the note's real etag (if_match still works). Fetch just the section you need via memory_get_section, or pass raw:true only when you really need the whole body. To get a large note onto your own disk without spending context tokens, GET the HTTP /download endpoint instead (your MCP base URL plus /download?path=<path>, bearer token), or pass transfer:\"http\" for a single-use URL that needs no bearer — see bootstrap capabilities. An Obsidian base ('project/books.base') reads as a note too, read-only: kind \"base\", its views translated into ```view blocks (render_views computes them), what has no equivalent in a warning, and its YAML in source."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault-relative path to the .md file; an Obsidian .base or .canvas reads too, read-only, with kind base or canvas.")),
		mcp.WithBoolean("raw", mcp.Description("Bypass the oversize guard and return the full body regardless of size; for a .canvas, also its JSON in source.")),
		mcp.WithNumber("max_bytes", mcp.Description("Explicit body cap in bytes — truncates even below the default threshold. Ignored when raw:true.")),
		mcp.WithBoolean("render_views", mcp.Description("Also compute the note's ```view blocks: each block stays and its result follows it between gosidian:view-result markers. Off by default, so a note read to be edited comes back as it is on disk (the response's hint then says how many blocks were left uncomputed); never write the computed result back.")),
		mcp.WithString("transfer", mcp.Description("\"http\": instead of the body, a single-use URL (5 min) to GET the note's raw bytes with no Authorization header, plus its etag and size. For a body you want on your own disk without spending context tokens when you cannot read your bearer for /download (a cautious client blocks reading it from its config).")),
	), s.handleGet)

	s.impl.AddTool(mcp.NewTool("memory_get_section",
		mcp.WithDescription("Read a single section of a note (heading + content up to the next heading of equal or higher level). Use when a note is long and you only need one section — much cheaper than memory_get on large files."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault-relative path to the .md file.")),
		mcp.WithString("heading", mcp.Required(), mcp.Description("The heading text to retrieve, without the leading '#'s. Match is case-insensitive. Its start is enough when it names one heading, such as an ID: \"BUG-014\" finds \"BUG-014 — …\"; the response's heading is the full one.")),
		mcp.WithBoolean("render_views", mcp.Description("Also compute the note's ```view blocks: each block stays and its result follows it between gosidian:view-result markers. Off by default, so a note read to be edited comes back as it is on disk (the response's hint then says how many blocks were left uncomputed); never write the computed result back.")),
	), s.handleGetSection)

	s.impl.AddTool(mcp.NewTool("memory_batch_get",
		mcp.WithDescription("Read multiple notes in a single call. Use this instead of N sequential memory_get calls when reconstructing context at session start. Each entry in the result has either content (success) or error (path not found / outside scope). The call itself does not fail because one path is missing. Result size is controllable: mode=outline|frontmatter skips bodies entirely, max_bytes_per_note truncates long bodies (entry gets truncated:true) — use them to keep bulk reads inside your context budget and fetch the few notes you actually need in full afterwards."),
		mcp.WithArray("paths", mcp.Required(), mcp.Description("Array of vault-relative paths to read (max 50).")),
		mcp.WithString("mode", mcp.Description("What to return per note: content (default, full body), outline (headings only; views:N marks a section holding ```view blocks), frontmatter (raw frontmatter block only). Content comes as stored: a note with views says so in hint. outline/frontmatter cost a fraction of the tokens.")),
		mcp.WithNumber("max_bytes_per_note", mcp.Description("Optional cap on the returned content bytes per note (content mode only). Longer bodies are cut at the cap and flagged truncated:true; the etag still stamps the full note.")),
	), s.handleBatchGet)

	s.impl.AddTool(mcp.NewTool("memory_recent",
		mcp.WithDescription("List the most recently modified notes. Use to catch up with 'what changed since I was last here'. Returns path, title, and mtime (unix seconds) ordered by descending mtime."),
		mcp.WithString("project", mcp.Description("Optional project (top-level folder) to scope the query. "+scopedProjectNote)),
		mcp.WithNumber("limit", mcp.Description("Max notes to return (default 20, max 500).")),
		mcp.WithString("since", mcp.Description("Lower bound on mtime. Accepts a relative duration ('1h', '24h', '7d') or an RFC3339 timestamp. Empty means 'no lower bound'.")),
	), s.handleRecent)

	s.impl.AddTool(mcp.NewTool("memory_get_frontmatter",
		mcp.WithDescription("Read only the YAML frontmatter of a note. Use this for cheap triage (check tag/status/type) before deciding whether to fetch the full body with memory_get. Returns both the raw YAML block and a parsed map of common fields."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault-relative path to the .md file.")),
	), s.handleGetFrontmatter)

	s.impl.AddTool(mcp.NewTool("memory_get_outline",
		mcp.WithDescription("Read the heading outline of a note (level + text + anchor id). Use to discover the structure of a long note so you can target a specific section with memory_get_section instead of fetching the whole body. A heading with views:N holds N ```view blocks: read that section with render_views:true to see their rows."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault-relative path to the .md file.")),
	), s.handleGetOutline)

	s.impl.AddTool(mcp.NewTool("memory_create",
		mcp.WithDescription("Create a new note at the given path. Fails if the note already exists. Markdown (.md) is the default note format; a path ending in .html creates a native single-file HTML note (frontmatter in a leading HTML comment, indexed/linked like a .md, rendered sandboxed in the web UI) when the instance has html_notes enabled — see `capabilities` in memory_bootstrap. Reserve .html for intrinsically-HTML content (generated reports, self-contained dashboards); prefer .md otherwise. Size guard: content is capped (default 1 MiB) and costs ~1 token/char through the context — for a large body already on disk use memory_ingest instead."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault-relative path, e.g. 'project/new-note.md'.")),
		mcp.WithString("content", mcp.Required(), mcp.Description("Full content of the note: markdown for .md paths, a self-contained HTML document for .html paths.")),
	), s.handleCreate)

	s.impl.AddTool(mcp.NewTool("memory_update",
		mcp.WithDescription("Overwrite an existing note's content. Fails if the note does not exist. Pass if_match (the etag returned by a previous memory_get) for optimistic locking: the call is rejected if the note has changed since you last read it. Size guard: content is capped (default 1 MiB) — for a large body already on disk use memory_ingest with overwrite:true instead. Shrink guard: a content that would empty a note (by default under 10% of a note of 1 KiB or more, as a placeholder written by mistake does) is refused unless allow_shrink is true; to change only a part, use memory_edit."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault-relative path of the note to update.")),
		mcp.WithString("content", mcp.Required(), mcp.Description("New full markdown content.")),
		mcp.WithString("if_match", mcp.Description("Optional etag from a previous memory_get. When provided, the call fails if the note's current etag differs — reload and retry.")),
		mcp.WithBoolean("allow_shrink", mcp.Description("Confirm a content much shorter than the note (a note split into others, or reset on purpose). Default false: the shrink guard refuses it.")),
	), s.handleUpdate)

	s.impl.AddTool(mcp.NewTool("memory_append",
		mcp.WithDescription("Append content to a note. Creates the note if it does not exist, with a minimal frontmatter (title, project tag) unless the content starts with its own. Use this to log observations incrementally. Pass if_match for optimistic locking against concurrent writes (only checked when the note already exists). Size guard: the merged note is capped (default 1 MiB)."),
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
		mcp.WithDescription("Delete a note from the vault and the index. With the server's trash on (trash_id in the result) the note can be restored from the web UI's trash, as one deleted there; otherwise it is gone."),
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
		mcp.WithDescription("Delete a project (top-level folder) and all notes inside, recursively. With the server's trash on (trash_id in the result) the project can be restored from the web UI's trash, with its access; otherwise it is gone. MCP tokens scoped to this project alone are revoked, and it is removed from the scope of tokens that list others (tokens_revoked, tokens_narrowed). Admin tokens only."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Project name.")),
	), s.handleDeleteProject)

	s.impl.AddTool(mcp.NewTool("memory_rename_project",
		mcp.WithDescription("Rename a project (top-level folder). All note paths under the project are reindexed under the new prefix. Admin tokens only."),
		mcp.WithString("from", mcp.Required(), mcp.Description("Current project name.")),
		mcp.WithString("to", mcp.Required(), mcp.Description("New project name.")),
	), s.handleRenameProject)

	s.impl.AddTool(mcp.NewTool("memory_backlinks",
		mcp.WithDescription("List notes that reference (via wiki-link) the given note path: from the body, or from a frontmatter value such as related: \"[[x]]\", whose keys `fields` names."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault-relative path of the target note.")),
	), s.handleBacklinks)

	s.impl.AddTool(mcp.NewTool("memory_outlinks",
		mcp.WithDescription("List wiki-links leaving the given note, with their resolved paths (if any); a link written in a frontmatter value carries its key in `field`. Pass include_cross_project=true to also flag links that point into a different top-level project via `cross_project: true` on each entry — scoped tokens always ignore this flag (they cannot see outside their project)."),
		mcp.WithString("path", mcp.Required(), mcp.Description("Vault-relative path of the source note.")),
		mcp.WithBoolean("include_cross_project", mcp.Description("When true, each outlink carries `cross_project: true` if its resolved path is in a different top-level project. Default false.")),
	), s.handleOutlinks)

	s.registerAttachmentTools()
	s.registerMediaTools()
	s.registerTableTools()
	s.registerIngestTool()
	s.registerAuditTools()
	s.registerBootstrapTool()
	s.registerDiscoveryTools()
	s.registerImportanceTool()
	s.registerQueryTool()
	s.registerHandoffTools()
	s.registerCompactTool()
	s.registerSelfStatsTool()
	s.registerScaffoldTool()
	s.registerBootstrapTemplatesTool()
	s.registerInitAgentTool()
	s.registerPromoteAgentTool()
	s.registerRefreshHotTool()
	s.registerTodosTool()
	s.registerLintTool()
	s.registerGraphTools()
	s.registerAskTool()
	s.registerSelfImproveTool()
	s.registerGlobalCheckTool()
	s.registerWaitTool()
	s.registerSnapshotTool()
	s.registerAutomationTool()
}

// ---- handlers ----

type searchHit struct {
	Path        string           `json:"path"`
	Title       string           `json:"title"`
	Snippet     string           `json:"snippet"`
	Score       float64          `json:"score"`
	Why         []string         `json:"why,omitempty"`
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
	limit := limitArg(ctx, req, 20, 200)
	includeOutline := req.GetBool("include_outline", false)
	includeFrontmatter := req.GetBool("include_frontmatter", false)
	variants := req.GetStringSlice("any_of", nil)
	if len(variants) > index.MaxVariants {
		return mcp.NewToolResultErrorf("any_of accepts at most %d phrasings", index.MaxVariants), nil
	}

	// Optional project filter. Scoped tokens silently intersect with their
	// project (never expand). The filter runs inside the index query, so the
	// limit counts only notes the caller may see (BUG-058).
	requestedProjects := req.GetStringSlice("projects", nil)
	// `project` (singular, as most tools take it) is shorthand for one entry.
	// Next to a non-empty `projects` it widens the filter, which a caller
	// that passed both by mistake would not expect: the result says so.
	merged := ""
	if p := strings.TrimSpace(req.GetString("project", "")); p != "" && !slices.Contains(requestedProjects, p) {
		if len(requestedProjects) > 0 {
			merged = p
		}
		requestedProjects = append(requestedProjects, p)
	}
	// Reject explicit hidden projects with a clear error so the caller knows
	// why the result is empty. Vault-wide search (no projects[] arg) silently
	// drops hits from hidden projects further down.
	for _, p := range requestedProjects {
		if res := s.rejectIfHidden(strings.TrimSpace(p)); res != nil {
			return res, nil
		}
	}
	filter := buildProjectsFilter(requestedProjects, tok.ProjectList())
	mergedNote := ""
	if merged != "" {
		// The effective list: a scoped token may have dropped some.
		searched := "none of them (outside the token's scope)"
		if len(filter.allowed) > 0 {
			searched = strings.Join(filter.allowed, ", ")
		}
		mergedNote = fmt.Sprintf("Note: memory_search merged project %q into projects: it searched %s.", merged, searched)
	}
	// One hit more than asked tells a full page from a cut one (IMP-122).
	opts := index.SearchOptions{Limit: limit + 1, Exclude: s.hiddenProjects(), Variants: variants}
	if filter.active {
		opts.Projects = append([]string{}, filter.allowed...) // non-nil: empty matches nothing
	}

	hits, err := s.index.SearchWith(q, opts)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("search failed", err), nil
	}
	// The checks below repeat the query's filter as defence in depth.
	out := make([]searchHit, 0, len(hits))
	truncated := false
	for _, h := range hits {
		if !tok.AllowsPath(h.Path) {
			continue
		}
		if !filter.matches(h.Path) {
			continue
		}
		if s.pathInHiddenProject(h.Path) {
			continue
		}
		if len(out) >= limit {
			truncated = true
			break
		}
		hit := searchHit{
			Path:    h.Path,
			Title:   h.Title,
			Snippet: stripMarkTags(h.Snippet),
			Score:   h.Score,
			Why:     h.Why,
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
					raw := parser.FrontmatterRawForPath(h.Path, note.Content)
					hit.Frontmatter = parser.ParseFrontmatterFields(raw)
				}
			}
		}
		out = append(out, hit)
	}
	metrics.CountSearch("mcp", len(out))
	body := map[string]any{"hits": out}
	if truncated {
		body["truncated"] = true
	}
	res, err := mcp.NewToolResultJSON(body)
	if err == nil && mergedNote != "" {
		appendNotice(res, mergedNote)
	}
	return res, err
}

type noteRef struct {
	Path   string       `json:"path"`
	Title  string       `json:"title"`
	Source string       `json:"source,omitempty"` // local | global | global-private (skill/agent merge in bootstrap)
	Bundle *skillBundle `json:"bundle,omitempty"` // skills only: their reference notes (IMP-115)
}

func (s *Server) handleListNotes(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	// If the token is scoped, force the filter to its project(s) and reject
	// any attempt to list a different one.
	project, err := scopedProject(tok, req.GetString("project", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	// Per-project visibility: an explicit hidden project is rejected; an
	// implicit vault-wide listing silently drops notes from hidden projects
	// further down.
	if project != "" {
		if res := s.rejectIfHidden(project); res != nil {
			return res, nil
		}
	}

	var notes []index.NoteRow
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
		if s.pathInHiddenProject(n.Path) {
			continue
		}
		out = append(out, noteRef{Path: n.Path, Title: n.Title})
	}
	return mcp.NewToolResultJSON(map[string]any{"notes": out})
}

type projectEntry struct {
	Name       string `json:"name"`
	NoteCount  int    `json:"noteCount"`
	Visibility string `json:"visibility,omitempty"` // public | internal | private
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
		if !tok.AllowsProject(p.Name) {
			continue
		}
		if s.projectHidden(p.Name) {
			continue
		}
		out = append(out, projectEntry{Name: p.Name, NoteCount: p.NoteCount, Visibility: s.projectVisibility(p.Name)})
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
	// The project goes through scopedProject (parity with memory_list_notes).
	project, err := scopedProject(tok, req.GetString("project", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if project != "" {
		if res := s.rejectIfHidden(project); res != nil {
			return res, nil
		}
	}

	var tags []index.TagCount
	if project == "" {
		tags, err = s.index.Tags()
	} else {
		tags, err = s.index.TagsByProject(project)
	}
	if err != nil {
		return mcp.NewToolResultErrorFromErr("tags failed", err), nil
	}
	// When listing vault-wide, the index aggregates all tags across projects.
	// Without a per-tag project breakdown we cannot strip hidden-project
	// occurrences, so vault-wide tag totals may include counts from hidden
	// projects. The leak is bounded (only the tag counter, not note bodies).
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
	project, err := scopedProject(tok, req.GetString("project", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if project != "" {
		if res := s.rejectIfHidden(project); res != nil {
			return res, nil
		}
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
		if s.pathInHiddenProject(n.Path) {
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
	// ViewsRendered says Content carries computed views (render_views), so
	// it is not the file as stored; ETag is still the stored file's.
	ViewsRendered bool            `json:"views_rendered,omitempty"`
	Kind          string          `json:"kind,omitempty"`  // "image" for a resolved media note (ADR-013), "base" for an Obsidian base, "canvas" for an Obsidian canvas; empty otherwise
	Media         *vault.MediaRef `json:"media,omitempty"` // resolved image payload when Kind=="image"
	// Source is an Obsidian base's YAML as written (Kind "base"); Content
	// is then its views translated (IMP-118). For a canvas (Kind "canvas",
	// IMP-144), its JSON, with raw:true.
	Source string `json:"source,omitempty"`
	// Oversize-guard fields (plan 20260706-token-economy-round2): set only
	// when the body was truncated. ETag always stamps the FULL note, so
	// optimistic locking works unchanged on a truncated read.
	Truncated   bool             `json:"truncated,omitempty"`
	Size        int64            `json:"size,omitempty"` // full body size in bytes
	Frontmatter string           `json:"frontmatter,omitempty"`
	Headings    []outlineHeading `json:"headings,omitempty"`
	// OutlineTotal is the real heading count; Headings is capped (see
	// getTruncMaxHeadings) so a 300-section log can't flood via its outline.
	OutlineTotal int    `json:"outline_total,omitempty"`
	Hint         string `json:"hint,omitempty"`
}

// getBodySoftCap is the memory_get oversize threshold: a larger body comes
// back truncated (outline + first chunk) unless raw:true. Append-only files
// like log.md grow to hundreds of KiB — one unguarded read would flood the
// caller's context.
const getBodySoftCap = 24 << 10

// getTruncChunk is the body prefix served with a truncated response.
const getTruncChunk = 4 << 10

// getTruncMaxHeadings caps the outline of a truncated response: an append-only
// log can carry 300+ sections and its outline alone would dwarf the body chunk
// (observed: 334 headings ≈ 50 KiB). We keep the first headings for structure
// and the tail for recency.
const (
	getTruncMaxHeadings  = 80
	getTruncHeadHeadings = 20
)

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
	if vault.IsBaseFile(path) {
		return s.getBase(tok, path, req.GetBool("render_views", false)), nil
	}
	if vault.IsCanvasFile(path) {
		return s.getCanvas(tok, path, req.GetBool("raw", false)), nil
	}
	note, err := s.vault.Load(path)
	if err != nil {
		return readNoteError(path, err), nil
	}
	if req.GetString("transfer", "") == "http" {
		return s.mintDownloadTicket(ctx, tok, note.Path, note.ETag(), len(note.Content)), nil
	}
	nc := noteContent{
		Path:    note.Path,
		Title:   note.Title,
		Content: string(note.Content),
		ETag:    note.ETag(),
	}
	if ref, kind := s.vault.MediaRefForNote(note.Path, note.Content); kind != "" {
		nc.Kind = kind
		nc.Media = ref
	}
	viewsNote := ""
	if req.GetBool("render_views", false) {
		if out, hash := s.renderViews(tok, note.Path, note.Content); hash != "" {
			nc.Content, nc.ViewsRendered = string(out), true
		}
		// A row of a database: its row views after the body (IMP-139).
		if out, ok := s.withRowViews(tok, note.Path, note.Content, []byte(nc.Content)); ok {
			nc.Content, nc.ViewsRendered = string(out), true
		}
	} else {
		viewsNote = joinHints(joinHints(viewsHint(note.Content, "pass render_views:true"), s.embedsHint(tok, note.Path, note.Content, "pass render_views:true")),
			s.rowViewsHint(tok, note.Path, note.Content, "pass render_views:true"))
	}

	// Oversize guard: truncate the body (default threshold, or the caller's
	// explicit max_bytes) unless raw:true. The truncated response carries the
	// frontmatter + outline so the caller can target a section next.
	limit := 0
	if !req.GetBool("raw", false) {
		if mb := req.GetInt("max_bytes", 0); mb > 0 && len(note.Content) > mb {
			limit = mb
		} else if mb == 0 && len(note.Content) > getBodySoftCap {
			limit = getTruncChunk
		}
	}
	if limit > 0 {
		// Cut what is served: with render_views, the body with its views.
		cut := []byte(nc.Content)
		if limit < len(cut) {
			cut = cut[:limit]
		}
		if i := bytes.LastIndexByte(cut, '\n'); i > 0 {
			cut = cut[:i+1]
		}
		nc.Content = string(cut)
		nc.Truncated = true
		nc.Size = note.Size
		nc.Frontmatter = parser.FrontmatterRawForPath(note.Path, note.Content)
		hs := outline(note.Content)
		nc.OutlineTotal = len(hs)
		if len(hs) > getTruncMaxHeadings {
			capped := make([]outlineHeading, 0, getTruncMaxHeadings)
			capped = append(capped, hs[:getTruncHeadHeadings]...)
			capped = append(capped, hs[len(hs)-(getTruncMaxHeadings-getTruncHeadHeadings):]...)
			hs = capped
		}
		nc.Headings = hs
		nc.Hint = fmt.Sprintf("body truncated (%d of %d bytes): fetch one section with memory_get_section, pass raw:true for the full body, or GET it onto your disk without context tokens via the HTTP /download endpoint (your MCP base URL plus /download?path=<path>, bearer token; the ETag header works as if_match)", len(nc.Content), note.Size)
		if nc.OutlineTotal > len(nc.Headings) {
			nc.Hint += fmt.Sprintf("; outline capped to %d of %d headings (first %d + most recent)", len(nc.Headings), nc.OutlineTotal, getTruncHeadHeadings)
		}
	}
	if viewsNote != "" {
		if nc.Hint != "" {
			nc.Hint += "; "
		}
		nc.Hint += viewsNote
	}
	return mcp.NewToolResultJSON(nc)
}

// checkIfMatch verifies that the caller's expected etag matches the current
// note state. Returns nil on match (or when if_match is empty). Returns a
// tool error result on mismatch.
func checkIfMatch(note *vault.Note, ifMatch string) *mcp.CallToolResult {
	if ifMatch == "" {
		return nil
	}
	// The HTTP /download endpoint returns the stamp as an RFC 7232 quoted
	// ETag header; accept it verbatim so the caller need not unquote it.
	ifMatch = strings.Trim(strings.TrimSpace(ifMatch), "\"")
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
		return readNoteError(path, err), nil
	}
	content := note.Content
	rendered := req.GetBool("render_views", false)
	// The heading itself, or the one that starts with it, an ID alone such
	// as "BUG-014" (IMP-130), looked up in the text as written; the section's
	// views and embeds are computed after. An embedded section brings
	// headings of its own, which would end the section early (BUG-094). A
	// heading that only an embed holds is looked up in the computed note.
	resolved, candidates := parser.ResolveHeading(content, heading)
	section := ""
	switch {
	case resolved != "":
		section = parser.ExtractSection(content, resolved)
		if rendered && section != "" {
			if out, hash := views.RenderNote([]byte(section), true, s.viewContext(tok, note.Path, note.Content), s.viewQuery(tok)); hash != "" {
				section = string(out)
			}
		}
	case rendered:
		if out, hash := s.renderViews(tok, note.Path, content); hash != "" {
			if r, c := parser.ResolveHeading(out, heading); r != "" {
				resolved, section = r, parser.ExtractSection(out, r)
			} else if len(c) > 0 {
				candidates = c
			}
		}
	}
	if resolved == "" {
		if len(candidates) > 0 {
			return mcp.NewToolResultErrorf("heading %q not found in %q; headings that start with it or contain it: %s — pass one of them",
				heading, path, quotedList(candidates)), nil
		}
		return mcp.NewToolResultErrorf("heading %q not found in %q; memory_get_outline lists the headings", heading, path), nil
	}
	if section == "" {
		return mcp.NewToolResultErrorf("heading %q not found in %q", heading, path), nil
	}
	if rendered {
		// A row of a database: its row views after the section (IMP-139).
		if withRows, ok := s.withRowViews(tok, note.Path, note.Content, []byte(section)); ok {
			section = string(withRows)
		}
	}
	out := map[string]any{
		"path":    path,
		"heading": resolved,
		"content": section,
		"etag":    note.ETag(),
	}
	if !rendered {
		if hint := joinHints(joinHints(viewsHint([]byte(section), "pass render_views:true"), s.embedsHint(tok, note.Path, []byte(section), "pass render_views:true")),
			s.rowViewsHint(tok, note.Path, note.Content, "pass render_views:true")); hint != "" {
			out["hint"] = hint
		}
	}
	return mcp.NewToolResultJSON(out)
}

// joinHints joins the non-empty hints with "; ".
func joinHints(hints ...string) string {
	var out []string
	for _, h := range hints {
		if h != "" {
			out = append(out, h)
		}
	}
	return strings.Join(out, "; ")
}

// quotedList joins items as "a", "b", "c".
func quotedList(items []string) string {
	q := make([]string, len(items))
	for i, it := range items {
		q[i] = strconv.Quote(it)
	}
	return strings.Join(q, ", ")
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
	if strings.HasSuffix(strings.ToLower(rel), ".html") && !s.vault.HTMLNotesEnabled() {
		return mcp.NewToolResultError("html notes are disabled on this instance — a per-project flag an admin can flip live from the web UI project toggles (or [vault] html_notes / GOSIDIAN_VAULT_HTML_NOTES)"), nil
	}
	tok, errRes := s.authorizeWrite(ctx, rel)
	if errRes != nil {
		return errRes, nil
	}
	if _, werr := s.writeNote(ctx, tok, noteWrite{
		rel: rel, mode: writeCreate, content: fixedContent([]byte(content)),
		action: audit.ActionCreate, schemaCheck: true,
	}); werr != nil {
		return werr.result(), nil
	}
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
	res, werr := s.writeNote(ctx, tok, noteWrite{
		rel: rel, mode: writeReplace, ifMatch: req.GetString("if_match", ""),
		content: fixedContent([]byte(content)), action: audit.ActionUpdate, schemaCheck: true,
		shrinkGuard: !req.GetBool("allow_shrink", false),
	})
	if werr != nil {
		return werr.result(), nil
	}
	// The new etag lets the caller pipeline further edits without a re-read.
	out := map[string]any{"path": rel}
	if res.ETag != "" {
		out["etag"] = res.ETag
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
	res, aerr := s.appendNote(ctx, tok, rel, addition, req.GetString("if_match", ""))
	if aerr != nil {
		return mcp.NewToolResultError(aerr.msg), nil
	}
	out := map[string]any{"path": res.Path}
	if res.ETag != "" {
		out["etag"] = res.ETag
	}
	return mcp.NewToolResultJSON(out)
}

// appendOutcome is what a successful append reports: the note path, its new
// ETag and whether the note was created by this append.
type appendOutcome struct {
	Path    string
	ETag    string
	Created bool
}

// appendNote is the append behind memory_append and POST /mcp/append
// (IMP-094): the separator-aware merge, through writeNote. A note it creates
// gets the minimal frontmatter. Authorization (scope and path) is the
// caller's job — both surfaces check it before getting here.
func (s *Server) appendNote(ctx context.Context, tok *auth.Token, rel, addition, ifMatch string) (appendOutcome, *writeError) {
	res, werr := s.writeNote(ctx, tok, noteWrite{
		rel: rel, mode: writeUpsert, ifMatch: ifMatch, action: audit.ActionAppend, schemaCheck: true,
		content: func(existing *vault.Note) ([]byte, error) {
			if existing == nil || len(existing.Content) == 0 {
				return []byte(withMinimalFrontmatter(rel, addition)), nil
			}
			body := string(existing.Content)
			sep := ""
			switch {
			case !strings.HasSuffix(body, "\n"):
				sep = "\n\n"
			case !strings.HasSuffix(body, "\n\n"):
				sep = "\n"
			}
			return []byte(body + sep + addition), nil
		},
	})
	if werr != nil {
		return appendOutcome{}, werr
	}
	return appendOutcome{Path: res.Path, ETag: res.ETag, Created: res.Created}, nil
}

// withMinimalFrontmatter gives a markdown note that an append creates the
// frontmatter every note is expected to have (its title, and its project as
// tag) unless the text brings its own: such a note used to fail lint with
// frontmatter-missing (IMP-119).
func withMinimalFrontmatter(rel, text string) string {
	if !strings.EqualFold(path.Ext(rel), ".md") {
		return text
	}
	// Text that brings its own frontmatter keeps it, moved to the top where
	// the parser looks for it; a leading "---" that is a horizontal rule
	// does not count.
	if lead := strings.TrimLeft(strings.TrimPrefix(text, "\ufeff"), "\r\n"); parser.ExtractFrontmatterRaw([]byte(lead)) != "" {
		return lead
	}
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "title: %s\n", yamlQuote(strings.TrimSuffix(path.Base(rel), path.Ext(rel))))
	if project, _, nested := strings.Cut(rel, "/"); nested {
		// A quoted block item: a project name may hold "," "#" or brackets,
		// which an inline list would split or cut.
		fmt.Fprintf(&b, "tags:\n  - %s\n", yamlQuote(project))
	}
	b.WriteString("---\n\n")
	b.WriteString(text)
	return b.String()
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
	count := 0
	res, werr := s.writeNote(ctx, tok, noteWrite{
		rel: rel, mode: writeReplace, ifMatch: req.GetString("if_match", ""),
		action: audit.ActionUpdate, schemaCheck: true,
		content: func(note *vault.Note) ([]byte, error) {
			body := string(note.Content)
			count = strings.Count(body, oldS)
			if count == 0 {
				return nil, contentError("old_string not found in %q", rel)
			}
			if count > 1 && !replaceAll {
				return nil, contentError("old_string matches %d occurrences in %q; pass replace_all=true or include more context to make it unique", count, rel)
			}
			if replaceAll {
				return []byte(strings.ReplaceAll(body, oldS, newS)), nil
			}
			return []byte(strings.Replace(body, oldS, newS, 1)), nil
		},
	})
	if werr != nil {
		return werr.result(), nil
	}
	out := map[string]any{
		"path":         rel,
		"replacements": count,
		"new_size":     res.Size,
	}
	if res.ETag != "" {
		out["etag"] = res.ETag
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
	tok, errRes := s.authorizeWrite(ctx, rel)
	if errRes != nil {
		return errRes, nil
	}
	if errRes := s.checkWriteLimits(ctx, tok, 0); errRes != nil {
		return errRes, nil
	}
	if !s.vault.IsNoteFile(rel) {
		return mcp.NewToolResultErrorf("%q is not a note: attachments are removed with memory_delete_attachment", rel), nil
	}
	unlock := s.vault.LockPath(rel)
	defer unlock()
	// With the trash on, a note goes there as one deleted from the web UI
	// does, and can be restored (BUG-097); without it, it is removed.
	out := map[string]any{"deleted": true, "path": rel}
	if s.trash != nil {
		if _, err := s.vault.Load(rel); err != nil {
			return mcp.NewToolResultErrorFromErr("delete failed", err), nil
		}
		id, err := s.trash.DiscardNote(rel)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("trash failed", err), nil
		}
		out["trash_id"] = id
	} else if err := s.vault.Delete(rel); err != nil {
		return mcp.NewToolResultErrorFromErr("delete failed", err), nil
	}
	if err := s.index.Delete(rel); err != nil {
		return mcp.NewToolResultErrorFromErr("index delete failed", err), nil
	}
	s.auditWrite(ctx, audit.ActionDelete, rel, "", 0)
	s.publishNoteChange("delete", rel, "", true)
	return mcp.NewToolResultJSON(out)
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
	// their existing project(s).
	if !tok.IsAdmin() {
		return mcp.NewToolResultError("project-scoped tokens cannot create projects"), nil
	}
	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if errRes := s.checkWriteLimits(ctx, tok, 0); errRes != nil {
		return errRes, nil
	}
	// The visibility is pinned at creation (the store default now), from a
	// fresh access entry; a name MCP tokens are scoped to is refused
	// (IMP-124).
	clean, err := projectops.Create(s.vault, s.projects, s.tokens, name, "", "")
	if err != nil {
		return mcp.NewToolResultErrorFromErr("create project failed", err), nil
	}
	s.auditWrite(ctx, audit.ActionCreateProject, clean, "", 0)
	s.publishTreeChange("create_project", clean, nil)
	s.publishSidebar("create", clean)
	return mcp.NewToolResultJSON(map[string]any{"name": clean, "visibility": s.projectVisibility(clean)})
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
	// Keep the source's note extension when the target carries none, then
	// refuse any target that is not a note (ADR-021) before touching disk.
	if path.Ext(toRel) == "" {
		toRel += path.Ext(fromRel)
	}
	if !s.vault.IsNoteFile(toRel) {
		return mcp.NewToolResultErrorf("to %q is not a note path: keep the .md (or .html) extension", toRel), nil
	}
	// Both endpoints must be inside the token's scope (write).
	tok, errRes := s.authorizeWrite(ctx, fromRel)
	if errRes != nil {
		return errRes, nil
	}
	if _, errRes := s.authorizeWrite(ctx, toRel); errRes != nil {
		return errRes, nil
	}
	if errRes := s.checkWriteLimits(ctx, tok, 0); errRes != nil {
		return errRes, nil
	}
	// Lock the source path only: it serializes rename-vs-update on the note
	// being moved. The target-exists probe inside RenameNote keeps its own
	// (benign) race window; locking both endpoints would need deadlock-safe
	// ordering for little gain.
	var rewritten []string
	err = s.underLock(fromRel, func() (err error) {
		rewritten, err = s.vault.RenameNote(s.index, fromRel, toRel)
		return err
	})
	if err != nil {
		return mcp.NewToolResultErrorFromErr("rename failed", err), nil
	}
	// The extension was resolved above, so toRel already is the canonical path
	// (this used to force .md, which mislabelled .html notes).
	canonical := toRel
	s.auditWrite(ctx, audit.ActionRename, fromRel, canonical, 0)
	s.publishRename(fromRel, canonical, rewritten)
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
	tok, errRes := s.authorizeWrite(ctx, dest)
	if errRes != nil {
		return errRes, nil
	}
	if errRes := s.checkWriteLimits(ctx, tok, 0); errRes != nil {
		return errRes, nil
	}
	var rewritten []string
	err = s.underLock(fromRel, func() (err error) {
		rewritten, err = s.vault.MoveNote(s.index, fromRel, project)
		return err
	})
	if err != nil {
		return mcp.NewToolResultErrorFromErr("move failed", err), nil
	}
	s.auditWrite(ctx, audit.ActionRename, fromRel, dest, 0)
	s.publishRename(fromRel, dest, rewritten)
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
	if !tok.IsAdmin() {
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
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	if from == to {
		return mcp.NewToolResultError("from and to name the same project"), nil
	}
	// A project hidden from MCP is out of an agent's reach, to change as to
	// read (IMP-161): it is renamed and deleted from the web UI.
	if errRes := s.rejectIfHidden(from); errRes != nil {
		return errRes, nil
	}
	if errRes := s.checkWriteLimits(ctx, tok, 0); errRes != nil {
		return errRes, nil
	}
	moved, err := projectops.Rename(s.vault, s.index, s.projects, s.tokens, from, to)
	if err != nil && !errors.Is(err, projectops.ErrIncomplete) {
		return mcp.NewToolResultErrorFromErr("rename project failed", err), nil
	}
	// An incomplete rename still stands: audited and announced, with a note
	// on what did not follow.
	s.auditWrite(ctx, audit.ActionRenameProject, from, to, 0)
	s.publishTreeChange("rename_project", from, map[string]any{"to": to})
	s.publishSidebar("update", to)
	res, jerr := mcp.NewToolResultJSON(map[string]any{"from": from, "to": to, "tokens_moved": moved})
	if err != nil && jerr == nil {
		appendNotice(res, "Note: "+err.Error())
	}
	return res, jerr
}

func (s *Server) handleDeleteProject(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok := s.tokenFromContext(ctx)
	if tok == nil {
		return mcp.NewToolResultError("unauthorized"), nil
	}
	if !tok.HasScope(auth.ScopeWrite) {
		return mcp.NewToolResultError("token lacks write scope"), nil
	}
	if !tok.IsAdmin() {
		return mcp.NewToolResultError("project-scoped tokens cannot delete projects"), nil
	}
	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if errRes := s.rejectIfHidden(strings.TrimSpace(name)); errRes != nil {
		return errRes, nil
	}
	if errRes := s.checkWriteLimits(ctx, tok, 0); errRes != nil {
		return errRes, nil
	}
	// The same path as the web UI: into the trash when it is on, with the
	// access saved for the restore, then out of the index, the access store
	// and the MCP token scopes (IMP-124).
	res, err := projectops.Delete(s.vault, s.index, s.projects, s.tokens, s.trash, name)
	if err != nil && !errors.Is(err, projectops.ErrIncomplete) {
		return mcp.NewToolResultErrorFromErr("delete project failed", err), nil
	}
	removed := res.Removed
	s.auditWrite(ctx, audit.ActionDeleteProject, res.Name, res.TrashID, int64(len(removed)))
	s.publishTreeChange("delete_project", res.Name, map[string]any{"removed_notes": len(removed)})
	s.publishSidebar("delete", res.Name)
	out, jerr := mcp.NewToolResultJSON(map[string]any{
		"deleted":         true,
		"name":            res.Name,
		"trash_id":        res.TrashID,
		"tokens_revoked":  res.TokensRevoked,
		"tokens_narrowed": res.TokensNarrowed,
		"removed_notes":   removed,
	})
	if err != nil && jerr == nil {
		appendNotice(out, "Note: "+err.Error())
	}
	return out, jerr
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
	path = s.notePathArg(tok, path)
	if !tok.AllowsPath(path) {
		return mcp.NewToolResultErrorf("path %q is outside the token's scope", path), nil
	}
	bl, err := s.index.Backlinks(path)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("backlinks failed", err), nil
	}
	out := make([]backlinkEntry, 0, len(bl))
	for _, b := range bl {
		if !tok.AllowsPath(b.Path) {
			continue
		}
		out = append(out, backlinkEntry{Path: b.Path, Title: b.Title, Fields: b.Fields})
	}
	return mcp.NewToolResultJSON(map[string]any{"backlinks": out})
}

// underLock runs fn holding rel's path lock, released by a deferred call:
// when fn panics, recoverMiddleware turns the panic into the call's error,
// and a lock released by hand after fn stayed taken for good (IMP-161,
// S3-13).
func (s *Server) underLock(rel string, fn func() error) error {
	defer s.vault.LockPath(rel)()
	return fn()
}

// notePathArg resolves the path argument of a link tool: a note path, or
// the same path without its extension, as a wikilink writes it
// (gosidian/docs/bugs/BUG-091 for …/BUG-091.md). Before, such a path gave
// an empty list, with nothing to say why (BUG-092). It looks only inside
// what tok reads: the extension it found outside, in the refusal that
// followed, said which note existed there (IMP-161, S3-12).
func (s *Server) notePathArg(tok *auth.Token, p string) string {
	p = strings.TrimSpace(p)
	low := strings.ToLower(p)
	if strings.HasSuffix(low, ".md") || strings.HasSuffix(low, ".html") {
		return p
	}
	if tok == nil || !tok.AllowsPath(p) || s.pathInHiddenProject(p) {
		return p
	}
	for _, ext := range []string{".md", ".html"} {
		if n, err := s.index.Note(p + ext); err == nil && n != nil {
			return p + ext
		}
	}
	return p
}

// backlinkEntry is a note that links to the one asked about; Fields are
// the frontmatter keys whose values link there, absent when the links are
// in the body only (IMP-127 iteration 2).
type backlinkEntry struct {
	Path   string   `json:"path"`
	Title  string   `json:"title"`
	Fields []string `json:"fields,omitempty"`
}

type outlinkEntry struct {
	Target       string `json:"target"`
	ResolvedPath string `json:"resolvedPath"`
	Alias        string `json:"alias,omitempty"`
	Field        string `json:"field,omitempty"`
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
	path = s.notePathArg(tok, path)
	if !tok.AllowsPath(path) {
		return mcp.NewToolResultErrorf("path %q is outside the token's scope", path), nil
	}
	// Scoped tokens never see cross-project info (they cannot access other
	// projects anyway), so ignore the flag.
	crossProject := req.GetBool("include_cross_project", false) && tok.IsAdmin()
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
			Field:        o.Field,
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
	Path        string           `json:"path"`
	Title       string           `json:"title,omitempty"`
	Content     string           `json:"content,omitempty"`
	Frontmatter string           `json:"frontmatter,omitempty"`
	Headings    []outlineHeading `json:"headings,omitempty"`
	ETag        string           `json:"etag,omitempty"`
	Truncated   bool             `json:"truncated,omitempty"`
	Hint        string           `json:"hint,omitempty"`
	Error       string           `json:"error,omitempty"`
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
	mode := strings.TrimSpace(req.GetString("mode", "content"))
	rowSchemas := map[string]*dbschema.Schema{} // by folder
	switch mode {
	case "content", "outline", "frontmatter":
		// ok
	default:
		return mcp.NewToolResultErrorf("unknown mode %q (expected content, outline, frontmatter)", mode), nil
	}
	maxBytes := req.GetInt("max_bytes_per_note", 0)
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
			if errors.Is(err, vault.ErrNotNote) {
				entry.Error = "not a note"
			}
			out = append(out, entry)
			continue
		}
		entry.Title = note.Title
		entry.ETag = note.ETag()
		switch mode {
		case "outline":
			entry.Headings = outline(note.Content)
		case "frontmatter":
			entry.Frontmatter = parser.FrontmatterRawForPath(p, note.Content)
		default:
			body := string(note.Content)
			if maxBytes > 0 && len(body) > maxBytes {
				body = body[:maxBytes]
				entry.Truncated = true
			}
			entry.Content = body
			// The database of a folder is read once; whether the note is one
			// of its rows depends on the note (rows: {type: plan}).
			dir := path.Dir(p)
			sc, seen := rowSchemas[dir]
			if !seen {
				sc = s.viewSchema(tok)(dir)
				rowSchemas[dir] = sc
			}
			rowHint := ""
			if sc != nil && len(sc.RowViews) > 0 && sc.IsRow(p, parser.FrontmatterRawForPath(p, note.Content)) {
				rowHint = s.rowViewsHint(tok, p, note.Content, "read it with memory_get and render_views:true")
			}
			entry.Hint = joinHints(joinHints(viewsHint(note.Content, "read it with memory_get and render_views:true"),
				s.embedsHint(tok, p, note.Content, "read it with memory_get and render_views:true")), rowHint)
		}
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
	project, err := scopedProject(tok, req.GetString("project", ""))
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := limitArg(ctx, req, 20, 500)

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
		return readNoteError(path, err), nil
	}
	raw := parser.FrontmatterRawForPath(path, note.Content)
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
	// Views counts the ```view blocks in the heading's own lines (up to the
	// next heading): read that section with render_views to see their rows.
	Views int `json:"views,omitempty"`
	// Embeds counts the lines that include another note's section
	// (![[note#Heading]]): render_views brings their text, and its views.
	Embeds int `json:"embeds,omitempty"`
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
		return readNoteError(path, err), nil
	}
	return mcp.NewToolResultJSON(map[string]any{
		"path":     path,
		"headings": outline(note.Content),
		"etag":     note.ETag(),
	})
}

// writeAndIndex persists the note and upserts it into the index synchronously
// so that a search immediately after the write reflects the change.
func (s *Server) writeAndIndex(rel string, content []byte) error {
	// ADR-021: write tools only touch notes. The vault layer enforces the same
	// rule; checking here first yields a didactic error before any I/O.
	if !s.vault.IsNoteFile(rel) {
		return fmt.Errorf("%w: %q is not a note (.md, or .html when html notes are enabled) — files go through memory_ingest (bridge_filename, source_path, transfer:\"http\") or memory_upload_attachment", vault.ErrNotNote, rel)
	}
	if err := s.vault.Save(rel, content); err != nil {
		return err
	}
	note, err := s.vault.Load(rel)
	if err != nil {
		return fmt.Errorf("reload: %w", err)
	}
	return s.index.Upsert(index.NoteDoc{
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
func buildProjectsFilter(requested []string, scope []string) projectsFilter {
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
	if len(scope) == 0 {
		return projectsFilter{active: len(clean) > 0, allowed: clean}
	}
	if len(clean) == 0 {
		return projectsFilter{active: true, allowed: scope}
	}
	// Scoped token with an explicit request: intersect. Projects outside the
	// scope silently drop out (empty intersection → zero hits).
	allowed := make([]string, 0, len(clean))
	inScope := map[string]struct{}{}
	for _, p := range scope {
		inScope[p] = struct{}{}
	}
	for _, p := range clean {
		if _, ok := inScope[p]; ok {
			allowed = append(allowed, p)
		}
	}
	return projectsFilter{active: true, allowed: allowed}
}

// readNoteError maps a Vault.Load failure into a tool error. A path that is
// not a note gets a didactic hint (IMP-080): attachment bytes never travel
// through the note tools.
func readNoteError(path string, err error) *mcp.CallToolResult {
	if errors.Is(err, vault.ErrNotNote) {
		return mcp.NewToolResultErrorf("%q is not a note: attachments are described by memory_attachment_info and served at /vault-files/%s (session cookie or Authorization: Bearer); table and media notes are the .md next to the file", path, path)
	}
	return mcp.NewToolResultErrorf("cannot read %q: %v", path, err)
}

// writeNoteError is readNoteError's counterpart for the load-before-write of
// memory_update / memory_edit: a non-note path points at the file tools.
func writeNoteError(rel string, err error) *mcp.CallToolResult {
	if errors.Is(err, vault.ErrNotNote) && vault.IsBaseFile(rel) {
		return mcp.NewToolResultErrorf("%q is an Obsidian base, read-only in gosidian: memory_get shows its views; it is edited in Obsidian", rel)
	}
	if errors.Is(err, vault.ErrNotNote) && vault.IsCanvasFile(rel) {
		return mcp.NewToolResultErrorf("%q is an Obsidian canvas, read-only in gosidian: memory_get shows its cards as text; it is edited in Obsidian", rel)
	}
	if errors.Is(err, vault.ErrNotNote) {
		return mcp.NewToolResultErrorf("%q is not a note (.md, or .html when html notes are enabled) — files go through memory_ingest (bridge_filename, source_path, transfer:\"http\") or memory_upload_attachment", rel)
	}
	return mcp.NewToolResultErrorf("note %q does not exist", rel)
}
