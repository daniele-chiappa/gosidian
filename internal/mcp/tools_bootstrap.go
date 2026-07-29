// Package mcp — memory_bootstrap tool (v1.2, IMP-009).
//
// Single-call aggregate that collapses the Regola-Zero session-start sequence
// (hot, README, tag:status:in-progress, tag:type:skill, recent) into one JSON
// payload. Read-only, scoped by the caller's token project filter.
package mcp

import (
	"context"
	"errors"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/initprompt"
	"github.com/gosidian/gosidian/internal/lint"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/vault"
	"github.com/mark3labs/mcp-go/mcp"
)

// registerBootstrapTool adds the memory_bootstrap tool. Called from
// registerTools() alongside the other v1.2 tools.
func (s *Server) registerBootstrapTool() {
	s.impl.AddTool(mcp.NewTool("memory_bootstrap",
		mcp.WithDescription("Aggregate session-start payload for a project: hot.md + README + instruction file (when present), active plans, skills, agents, 5 recent notes, stats. Call this FIRST each session instead of separate gets. `directives_block` carries the full operational directives rendered for this project — read and FOLLOW it (served fresh every session; regenerate your stub via memory_init_agent only when stub_version is ahead of your stub marker). `capabilities` reports the enabled content formats (html/media/table notes) plus attachment limits/extensions and the HTTP /upload endpoint hint. `maintenance` carries cheap grooming signals (hot.md size/age, broken wikilinks, stale-note count): when its `attention` flag is true, propose the relevant grooming at end of task. `tag_vocabulary` (only on projects with the use_tag_vocabulary flag) reports the extra lint tag vocabulary declared in memory/conventions.md frontmatter. `missing` lists absent vault scaffold; `agent_md.expected_external` means the instruction file lives in the agent's working dir, not the vault. Repeat calls: pass known_directives_version + known_etags (+ known_anchor_metas on anchor-enabled projects) and use mode lite — see the parameter docs."),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project (top-level folder) to bootstrap. Scoped tokens are forced to their project.")),
		mcp.WithString("profile", mcp.Description("CLI/agent profile for agent-anchor materialisation (default \"claude\"). When the master switch + the project's use_anchors flag are on and the profile supports native subagents, the response carries an `anchors` block: thin agent-anchor files to reconcile in the agent's cwd.")),
		mcp.WithNumber("known_directives_version", mcp.Description("The directives_version you already hold from a previous bootstrap: on match, directives_block is omitted (directives_version is always present to detect it).")),
		mcp.WithObject("known_etags", mcp.Description("Map of vault-relative path → etag from a previous bootstrap: files whose etag still matches come back unchanged:true with no body (hot_md, readme, agent_md).")),
		mcp.WithObject("known_anchor_metas", mcp.Description("Map of canonical vault path → meta_version from a previous bootstrap's anchors.items: anchors whose meta still matches come back as {path, canonical, meta_version, unchanged:true} with no content. If an unchanged anchor file is missing from disk, re-bootstrap without this param to get the content back.")),
		mcp.WithString("mode", mcp.Description("auto (default) | full | lite. lite serves hot.md as frontmatter + heading outline (fetch sections via memory_get_section); auto switches to lite only when hot.md crosses the oversize threshold (flagged auto_lite:true).")),
	), s.handleBootstrap)
}

type bootstrapFile struct {
	Present bool   `json:"present"`
	Path    string `json:"path,omitempty"`
	Content string `json:"content,omitempty"`
	ETag    string `json:"etag,omitempty"`
	// ExpectedExternal marks an instruction file that is absent from the vault
	// but expected to live in the agent's working dir (the stub model, ADR-010).
	// Only ever set on the agent_md payload, never on hot.md/README.md.
	ExpectedExternal bool `json:"expected_external,omitempty"`
	// Unchanged marks a file whose etag matched the caller's known_etags —
	// the body was deliberately omitted (Content empty ≠ empty file).
	Unchanged bool `json:"unchanged,omitempty"`
	// Frontmatter+Headings replace Content in lite mode (hot.md only).
	Frontmatter string           `json:"frontmatter,omitempty"`
	Headings    []outlineHeading `json:"headings,omitempty"`
	// AutoLite marks a hot.md served in lite shape because it crossed the
	// oversize threshold while the caller left mode unset (auto). Pass
	// mode:"full" to force the body.
	AutoLite bool `json:"auto_lite,omitempty"`
}

// autoLiteThreshold: with mode unset (auto), a hot.md larger than this is
// served in lite shape (frontmatter + outline). Mirrors the lint
// hot-oversize default — a hot.md past it should be compacted anyway.
const autoLiteThreshold = lint.DefaultHotOversizeBytes

type bootstrapStats struct {
	NotesCount int                 `json:"notes_count"`
	TopTags    []bootstrapTagCount `json:"top_tags"`
}

// bootstrapMaintenance is the per-project grooming digest (ADR-019): numbers
// only, computed live from indexed queries and two stats — never a content
// scan (memory_lint stays on-demand; this block tells the agent WHEN to run
// it). Attention fires on the two failure modes observed in the wild (hot.md
// growing unbounded, broken wikilinks accumulating silently); stale_count is
// informational context, deliberately not a trigger.
type bootstrapMaintenance struct {
	HotSize         int64 `json:"hot_size"`
	HotOversize     bool  `json:"hot_oversize"`
	HotAgeDays      int   `json:"hot_age_days"`
	LogSize         int64 `json:"log_size"`
	BrokenLinks     int   `json:"broken_links"`
	StaleCount      int   `json:"stale_count"`
	StaleCutoffDays int   `json:"stale_cutoff_days"`
	Attention       bool  `json:"attention"`
}

// maintenanceStaleCutoffDays is the digest's stale threshold. Fixed until the
// soak says otherwise (ADR-019): notes untouched for longer, and not tagged
// status:done/status:archived, count as review candidates.
const maintenanceStaleCutoffDays = 90

type bootstrapTagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

// bootstrapPendingInsights is the owner-facing surface for un-triaged
// self-improvement insights (status:pending). Count is the full total;
// Notes is capped to the first few for a quick preview.
type bootstrapPendingInsights struct {
	Project string    `json:"project"`
	Count   int       `json:"count"`
	Notes   []noteRef `json:"notes"`
}

// bootstrapCapabilities advertises this instance's content capabilities at
// session start, so agents learn what note formats and file channels exist
// without having to open individual tool schemas. Flags mirror the live vault
// config; static guidance on WHEN to use each format lives in the directives
// («Formati di nota e allegati»), which cross-reference this block.
type bootstrapCapabilities struct {
	HTMLNotes   bool                      `json:"html_notes"`
	MediaNotes  bool                      `json:"media_notes"`
	TableNotes  bool                      `json:"table_notes"`
	Attachments bootstrapAttachCapability `json:"attachments"`
}

type bootstrapAttachCapability struct {
	MaxMiB             int      `json:"max_mib"`
	Extensions         []string `json:"extensions"`
	UploadEndpointHint string   `json:"upload_endpoint_hint"`
	Tools              []string `json:"tools"`
	// BridgeDir is the server-side staging directory for bridge_filename
	// sources, when configured. Surfacing it here closes the ADR-018
	// chicken-and-egg: a co-located agent learns the cheap path up front
	// instead of after a wasteful base64 upload.
	BridgeDir string `json:"bridge_dir,omitempty"`
	// AllowedUploadRoots are the extra server-side roots source_path may read
	// from (the vault root and the bridge dir are always implicitly allowed).
	AllowedUploadRoots []string `json:"allowed_upload_roots,omitempty"`
	// IngestURLEnabled reports whether the memory_ingest url source has a
	// non-empty allowlist on this instance.
	IngestURLEnabled bool `json:"ingest_url_enabled"`
}

// buildCapabilities assembles the capabilities block from live config and the
// attach allowlist. Extensions are sorted (dot-less) for stable output.
func (s *Server) buildCapabilities() bootstrapCapabilities {
	exts := make([]string, 0, len(attach.AllowedExt))
	for ext := range attach.AllowedExt {
		exts = append(exts, strings.TrimPrefix(ext, "."))
	}
	sort.Strings(exts)
	return bootstrapCapabilities{
		HTMLNotes:  s.vault.HTMLNotesEnabled(),
		MediaNotes: s.vault.MediaNotesEnabled(),
		TableNotes: s.vault.TableNotesEnabled(),
		Attachments: bootstrapAttachCapability{
			MaxMiB:             attach.MaxBytes >> 20,
			Extensions:         exts,
			UploadEndpointHint: "to save a file use memory_ingest (routes by extension; sources: bridge_filename, source_path, url, or transfer:\"http\" for a single-use upload ticket). Raw bytes can also be POSTed multipart (field 'file', bearer token) to your MCP /sse URL with /sse replaced by /upload — bytes travel over HTTP, not the model context",
			Tools:              []string{"memory_ingest", "memory_upload_attachment", "memory_upload_resource"},
			BridgeDir:          s.bridgeDir,
			AllowedUploadRoots: s.allowedUploadRoots,
			IngestURLEnabled:   len(s.ingestURLAllow) > 0,
		},
	}
}

// conventionFiles maps the relative-to-project filename to the key we expose
// in the JSON payload. Order matters: the `missing` list is emitted in this
// order so repeated calls produce stable output.
var conventionFiles = []struct {
	rel string
	key string
}{
	{"hot.md", "hot_md"},
	{"README.md", "readme"},
}

// agentFileCandidates lists the agent-native instruction file names gosidian
// recognises, in priority order. AGENTS.md is the generic cross-agent default
// (ADR-010); CLAUDE.md / .cursor/rules.mdc / CONVENTIONS.md are detected for
// agent-specific harnesses. gosidian no longer assumes a single name.
var agentFileCandidates = []string{"AGENTS.md", "CLAUDE.md", ".cursor/rules.mdc", "CONVENTIONS.md"}

// anchorReconcileDirective tells the agent how to apply the desired anchor set
// against its cwd. The server is cwd-blind: it only computes the desired files
// + their meta_version; the agent (which has filesystem access) does the diff.
const anchorReconcileDirective = "Riconcilia gli agenti referenziati in `target_dir` (file gitignored). Per ogni item: se il file non esiste → scrivilo con `content`; se esiste con un marker `<!-- gosidian:anchor ... meta=X -->` e X≠`meta_version` → riscrivilo; se X=`meta_version` → lascialo. Item con `unchanged:true` (content omesso, meta già tuo): lascialo com'è; se il file manca dal disco, ripeti il bootstrap senza `known_anchor_metas` per riavere il content. Rimuovi i file con marker `gosidian:anchor` il cui `canonical` non è più fra gli items (orphan). NON toccare file SENZA il marker `gosidian:anchor` (foreign, scritti a mano): segnalali e proponi l'adozione con `memory_promote_agent` (con `adopt_into_existing:true` se la nota canonica type:agent esiste già)."

// anchorsEnabledFor reports whether the bootstrap should surface agent anchors
// for (project, profile): master switch on, the project opted into use_anchors,
// and the profile supports native subagents. Default-off on every axis keeps
// existing bootstrap behaviour unchanged.
func (s *Server) anchorsEnabledFor(project string, profile initprompt.Profile) bool {
	return s.anchorsEnabled && s.projects != nil && s.projects.UsesAnchors(project) && initprompt.SupportsAnchors(profile)
}

func (s *Server) handleBootstrap(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	project, err := s.resolveProject(tok, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	// mode: "" (default) = auto — full unless hot.md crosses the oversize
	// threshold, then lite with auto_lite:true. Explicit "full"/"lite" win.
	mode := strings.TrimSpace(req.GetString("mode", ""))
	switch mode {
	case "", "auto", "full", "lite":
		// ok
	default:
		return mcp.NewToolResultErrorf("unknown mode %q (expected auto, full or lite)", mode), nil
	}
	knownEtags := extractStringMap(req.GetArguments()["known_etags"])

	payload := map[string]any{
		"project": project,
		// Directives are SERVED here (ADR-010): directives_block is the full
		// operational ruleset, rendered generic (parameterised on project).
		// The instruction file on disk is a thin stub that points here, so
		// projects never embed — and never drift from — the directives.
		// stub_version lets an agent know when its (rarely changing) stub
		// must be regenerated via memory_init_agent.
		"directives_version": initprompt.DirectivesVersion,
		"stub_version":       initprompt.StubVersion,
		// capabilities: per-instance content-format discovery (plan
		// 20260706-capability-discovery-bootstrap). Always present — an agent
		// must see html_notes:false, not silence, to know .html is off here.
		"capabilities": s.buildCapabilities(),
	}
	// Version negotiation: a caller that already holds the current directives
	// skips the whole block — directives_version above is the match signal.
	if req.GetInt("known_directives_version", 0) != initprompt.DirectivesVersion {
		if block, _, derr := initprompt.RenderDirectives(project); derr == nil {
			payload["directives_block"] = block
		}
	}
	var missing []string

	for _, f := range conventionFiles {
		full := path.Join(project, f.rel)
		file := s.loadBootstrapFile(full)
		file = applyKnownEtag(file, knownEtags)
		liteHot := mode == "lite"
		if (mode == "" || mode == "auto") && f.key == "hot_md" &&
			int64(len(file.Content)) > autoLiteThreshold {
			liteHot = true
			file.AutoLite = true
		}
		if liteHot && f.key == "hot_md" && file.Present && !file.Unchanged {
			// Lite: the session cache tends to be the payload's heaviest part.
			// Serve its shape (frontmatter + outline), not its body; the agent
			// pulls the sections it needs via memory_get_section.
			content := []byte(file.Content)
			file.Frontmatter = parser.FrontmatterRawForPath(file.Path, content)
			for _, h := range parser.ExtractHeadings(content) {
				file.Headings = append(file.Headings, outlineHeading{Level: h.Level, Text: h.Text, ID: h.ID})
			}
			file.Content = ""
		}
		payload[f.key] = file
		if !file.Present {
			missing = append(missing, f.rel)
		}
	}

	// agent_md: agent-agnostic detection of the project's instruction file
	// (ADR-010) — no longer assumes CLAUDE.md. Reports the first existing
	// candidate (as a vault note) and its name. In the stub model the
	// instruction file lives in the agent's working dir, OUTSIDE the vault, so
	// its absence from the vault is expected — not a missing scaffold (IMP-050).
	// Flag it expected_external instead of polluting `missing` with a perpetual
	// false positive; `missing` stays reserved for real vault files
	// (hot.md / README.md).
	agentFile, agentName := s.detectAgentFile(project)
	if agentFile.Present {
		payload["agent_md_name"] = agentName
		agentFile = applyKnownEtag(agentFile, knownEtags)
	} else {
		agentFile.ExpectedExternal = true
	}
	payload["agent_md"] = agentFile

	active, err := s.filterByTagAndProject("status:in-progress", project, tok)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("active_plans lookup failed", err), nil
	}
	// Intersect with type:plan — only plans count as "active plans" here.
	active = s.intersectWithTag(active, "type:plan")
	payload["active_plans"] = active

	skills, err := s.filterByTagAndProject("type:skill", project, tok)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("skills lookup failed", err), nil
	}
	payload["available_skills"] = s.mergeGlobals(skills, "type:skill", project, tok)

	agents, err := s.filterByTagAndProject("type:agent", project, tok)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("agents lookup failed", err), nil
	}
	payload["available_agents"] = s.mergeGlobals(agents, "type:agent", project, tok)

	// anchors: local agent-anchor materialisation set (plan 20260630). Gated by
	// the master switch + the project's use_anchors flag + profile capability.
	// Cwd-blind: the server returns the desired set + a reconcile directive; the
	// agent diffs against its cwd using each file's marker. Default-off → the key
	// is absent and bootstrap behaves exactly as before.
	if profile := initprompt.Profile(strings.TrimSpace(req.GetString("profile", "claude"))); s.anchorsEnabledFor(project, profile) {
		items, aerr := s.buildAgentAnchors(project, profile, tok)
		if aerr != nil {
			return mcp.NewToolResultErrorFromErr("anchors lookup failed", aerr), nil
		}
		// Delta on repeat bootstraps (IMP-068): anchors whose meta_version the
		// caller already holds ship without content, mirroring known_etags.
		if knownMetas := extractStringMap(req.GetArguments()["known_anchor_metas"]); len(knownMetas) > 0 {
			for i := range items {
				if knownMetas[items[i].Canonical] == items[i].MetaVersion {
					items[i].Content = ""
					items[i].Unchanged = true
				}
			}
		}
		anchors := map[string]any{
			"profile":    string(profile),
			"target_dir": initprompt.AnchorDir(profile),
			"items":      items,
		}
		// The reconcile directive is ~500 chars shipped on EVERY bootstrap of
		// an anchor-enabled project: with an empty desired set there is
		// nothing to write, so skip it (an agent holding stale anchor files
		// still sees target_dir + items:[] and the files self-describe via
		// their gosidian:anchor marker).
		if len(items) > 0 {
			anchors["reconcile"] = anchorReconcileDirective
		}
		payload["anchors"] = anchors
	}

	recent, err := s.index.RecentNotes(project, 0, 5)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("recent lookup failed", err), nil
	}
	recentOut := make([]recentNoteResponse, 0, len(recent))
	for _, n := range recent {
		if !tok.AllowsPath(n.Path) {
			continue
		}
		recentOut = append(recentOut, recentNoteResponse{Path: n.Path, Title: n.Title, Mtime: n.Mtime})
	}
	payload["recent_notes"] = recentOut

	projNotes, err := s.index.NotesByPrefix(project)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("notes count failed", err), nil
	}
	tagCounts, err := s.index.TagsByProject(project)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("tag counts failed", err), nil
	}
	top := make([]bootstrapTagCount, 0, 5)
	for i, t := range tagCounts {
		if i >= 5 {
			break
		}
		top = append(top, bootstrapTagCount{Tag: t.Tag, Count: t.Count})
	}
	payload["stats"] = bootstrapStats{
		NotesCount: len(projNotes),
		TopTags:    top,
	}

	// maintenance digest (ADR-019): best-effort like pending_insights — a
	// lookup error degrades to an absent block, never a failed bootstrap.
	staleBefore := time.Now().AddDate(0, 0, -maintenanceStaleCutoffDays).Unix()
	attachExts := make([]string, 0, len(attach.AllowedExt))
	for ext := range attach.AllowedExt {
		attachExts = append(attachExts, ext)
	}
	if broken, stale, merr := s.index.MaintenanceCounts(project, staleBefore, attachExts); merr == nil {
		m := bootstrapMaintenance{
			BrokenLinks:     broken,
			StaleCount:      stale,
			StaleCutoffDays: maintenanceStaleCutoffDays,
		}
		hotThreshold := s.lintHotOversizeBytes
		if hotThreshold <= 0 {
			hotThreshold = lint.DefaultHotOversizeBytes
		}
		if abs, aerr := s.vault.Abs(project + "/hot.md"); aerr == nil {
			if fi, serr := os.Stat(abs); serr == nil {
				m.HotSize = fi.Size()
				m.HotOversize = fi.Size() > hotThreshold
				m.HotAgeDays = int(time.Since(fi.ModTime()).Hours() / 24)
			}
		}
		if abs, aerr := s.vault.Abs(project + "/log.md"); aerr == nil {
			if fi, serr := os.Stat(abs); serr == nil {
				m.LogSize = fi.Size()
			}
		}
		m.Attention = m.HotOversize || m.BrokenLinks > 0
		payload["maintenance"] = m
	}

	// tag_vocabulary (IMP-075): when the project opted in (use_tag_vocabulary
	// flag), surface the effective extra vocabulary declared in
	// memory/conventions.md frontmatter — the extension stays visible at
	// session start instead of acting silently. Best-effort: absent note or
	// empty declaration degrades to {enabled:true, extra:[]}.
	if s.projects != nil && s.projects.UsesTagVocabulary(project) {
		var extra []string
		if note, verr := s.vault.Load(lint.ProjectVocabularyNote(project)); verr == nil {
			raw := parser.FrontmatterRawForPath(note.Path, note.Content)
			extra = lint.ValidVocabularyEntries(parser.FrontmatterList(raw, "tag_vocabulary"))
		}
		if extra == nil {
			extra = []string{}
		}
		payload["tag_vocabulary"] = map[string]any{
			"enabled": true,
			"note":    lint.ProjectVocabularyNote(project),
			"extra":   extra,
		}
	}

	// pending_insights surfaces the owner's un-triaged self-improvement
	// insights (status:pending) regardless of which project is being
	// bootstrapped, so they're seen at every session start. Only present
	// when the loop is enabled, and only populated for tokens that can read
	// the insights project. Best-effort: a lookup error degrades to absent
	// rather than failing the whole bootstrap.
	if s.selfImproveEnabled {
		insProject := s.selfImproveProject
		if insProject == "" {
			insProject = "insights"
		}
		if pending, perr := s.filterByTagAndProject("status:pending", insProject, tok); perr == nil {
			pending = s.intersectWithTag(pending, "type:insight")
			notes := pending
			if len(notes) > 10 {
				notes = notes[:10]
			}
			payload["pending_insights"] = bootstrapPendingInsights{
				Project: insProject,
				Count:   len(pending),
				Notes:   notes,
			}
		}
	}

	if missing == nil {
		missing = []string{}
	}
	payload["missing"] = missing

	return mcp.NewToolResultJSON(payload)
}

// loadBootstrapFile reads one convention file into a bootstrapFile, including
// its etag. A missing file is not an error — it returns {Present: false}.
// Any other error (permission denied, index mismatch) also surfaces as absent
// so the tool never fails the whole call on a single missing file.
func (s *Server) loadBootstrapFile(rel string) bootstrapFile {
	note, err := s.vault.Load(rel)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || isNoteNotFound(err) {
			return bootstrapFile{Present: false}
		}
		return bootstrapFile{Present: false}
	}
	return bootstrapFile{
		Present: true,
		Path:    rel,
		Content: string(note.Content),
		ETag:    note.ETag(),
	}
}

// applyKnownEtag omits the body of a file whose etag matches the caller's
// known_etags entry, flagging it Unchanged so the client can tell "omitted
// because you have it" from "empty file". Keys are vault-relative paths as
// returned by a previous bootstrap.
func applyKnownEtag(file bootstrapFile, known map[string]string) bootstrapFile {
	if !file.Present || file.Path == "" || len(known) == 0 {
		return file
	}
	if et, ok := known[file.Path]; ok && et != "" && et == file.ETag {
		file.Content = ""
		file.Unchanged = true
	}
	return file
}

// detectAgentFile returns the first existing instruction file (as a vault note)
// among agentFileCandidates under the project, plus the name that matched.
// Returns {Present:false}, "" when none exists. Agent-agnostic (ADR-010).
func (s *Server) detectAgentFile(project string) (bootstrapFile, string) {
	for _, name := range agentFileCandidates {
		f := s.loadBootstrapFile(path.Join(project, name))
		if f.Present {
			return f, name
		}
	}
	return bootstrapFile{Present: false}, ""
}

// filterByTagAndProject returns note refs tagged with `tag`, restricted to
// paths under `<project>/` and allowed by the caller's token scope.
func (s *Server) filterByTagAndProject(tag, project string, tok tokenScoped) ([]noteRef, error) {
	notes, err := s.index.NotesByTag(tag)
	if err != nil {
		return nil, err
	}
	out := make([]noteRef, 0)
	prefix := project + "/"
	for _, n := range notes {
		if !strings.HasPrefix(n.Path, prefix) {
			continue
		}
		if !tok.AllowsPath(n.Path) {
			continue
		}
		out = append(out, noteRef{Path: n.Path, Title: n.Title})
	}
	return out, nil
}

// intersectWithTag returns the subset of `candidates` whose path also carries
// the given tag. Used to intersect `status:in-progress` with `type:plan`
// without a second full scan: the candidate list is already small.
func (s *Server) intersectWithTag(candidates []noteRef, tag string) []noteRef {
	tagged, err := s.index.NotesByTag(tag)
	if err != nil {
		return candidates
	}
	set := make(map[string]struct{}, len(tagged))
	for _, n := range tagged {
		set[n.Path] = struct{}{}
	}
	out := make([]noteRef, 0, len(candidates))
	for _, c := range candidates {
		if _, ok := set[c.Path]; ok {
			out = append(out, c)
		}
	}
	return out
}

// mergeGlobals augments a project's local skills/agents with those from the
// shared global projects, when the global feature is on and the project opted
// in (projects.Flags.UseGlobals). global-public entries are shared with every
// token; global-private entries only with tokens that can read that project
// (admin / scoped to it). Deduplication is local-overrides-global by title: a
// local entry shadows a global one with the same title. Each entry carries its
// source (local | global | global-private).
func (s *Server) mergeGlobals(local []noteRef, tag, project string, tok tokenScoped) []noteRef {
	if !s.globalEnabled || s.projects == nil || !s.projects.UsesGlobals(project) {
		return local
	}
	seen := make(map[string]struct{}, len(local))
	out := make([]noteRef, 0, len(local))
	for _, r := range local {
		r.Source = "local"
		seen[r.Title] = struct{}{}
		out = append(out, r)
	}
	add := func(globalProject, source string, applyScope bool) {
		if globalProject == "" || globalProject == project {
			return
		}
		if applyScope && !tok.AllowsPath(globalProject+"/") {
			return
		}
		rows, err := s.index.NotesByTagInProject(tag, globalProject)
		if err != nil {
			return
		}
		tmplPrefix := globalProject + "/templates/"
		for _, n := range rows {
			// Template definition files live under <global>/templates/ and
			// are scaffold sources (often with {{PLACEHOLDER}} content), not
			// usable skills/agents — never surface them in the merge.
			if strings.HasPrefix(n.Path, tmplPrefix) {
				continue
			}
			if _, dup := seen[n.Title]; dup {
				continue
			}
			seen[n.Title] = struct{}{}
			out = append(out, noteRef{Path: n.Path, Title: n.Title, Source: source})
		}
	}
	add(s.globalPublic, "global", false)         // shared surface: bypass token scope
	add(s.globalPrivate, "global-private", true) // gated: admin / owner-scoped only
	return out
}

// tokenScoped is a narrow subset of *auth.Token, declared locally so helpers
// don't depend on the concrete type and tests can stub if needed.
type tokenScoped interface {
	AllowsPath(path string) bool
}

// isNoteNotFound checks for the "file does not exist" condition coming from
// vault.Load when the underlying os.Stat returns ENOENT.
func isNoteNotFound(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, os.ErrNotExist)
}

// compile-time check: vault.Note is what Load returns.
var _ = func() *vault.Note { return nil }
