package lint

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/dbschema"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/views"
)

// allRules is the registry of baseline rules run by default, in stable
// declaration order (so the output is deterministic).
var allRules = []ruleSpec{
	{name: "broken-wikilink", defaultSeverity: SeverityWarning, fn: checkBrokenWikilink},
	{name: "orphan-note", defaultSeverity: SeverityInfo, fn: checkOrphanNote},
	{name: "frontmatter-missing", defaultSeverity: SeverityError, fn: checkFrontmatterMissing},
	{name: "frontmatter-tag-unknown", defaultSeverity: SeverityWarning, fn: checkFrontmatterTagUnknown},
	{name: "status-incoherent", defaultSeverity: SeverityWarning, fn: checkStatusIncoherent},
	{name: "hot-oversize", defaultSeverity: SeverityWarning, fn: checkHotOversize},
	{name: "skill-oversize", defaultSeverity: SeverityWarning, fn: checkSkillOversize},
	{name: "database-field-invalid", defaultSeverity: SeverityWarning, fn: checkDatabaseFields},
}

// checkDatabaseFields checks the rows of each database note of the project
// against its schema (IMP-127): without it agents drift, inventing field
// names (`resolved:` for `closed:`) that a query on the declared field then
// misses. A database note whose schema does not parse is reported too, since
// its rows would otherwise go unchecked silently.
func checkDatabaseFields(_ context.Context, l *Linter, project string) ([]Issue, error) {
	schemas, bad, err := dbschema.ForProject(l.index, l.vault, project)
	if err != nil {
		return nil, err
	}
	var issues []Issue
	badPaths := make([]string, 0, len(bad))
	for p := range bad {
		badPaths = append(badPaths, p)
	}
	sort.Strings(badPaths)
	for _, p := range badPaths {
		issues = append(issues, Issue{
			Severity: SeverityWarning,
			File:     p,
			Rule:     "database-field-invalid",
			Message:  "database schema does not parse: " + bad[p].Error(),
			FixHint:  "declare `fields` as a map of name to {type, options, required}; types: " + strings.Join(dbschema.Types, ", "),
		})
	}
	if len(schemas) == 0 {
		return issues, nil
	}
	notes, err := l.notesInProject(project)
	if err != nil {
		return nil, err
	}
	for _, n := range notes {
		for _, s := range schemas {
			if !s.Covers(n.Path) {
				continue
			}
			for _, p := range s.Validate(n.Path, rawFrontmatter(n)) {
				issues = append(issues, Issue{
					Severity: SeverityWarning,
					File:     n.Path,
					Rule:     "database-field-invalid",
					Message:  p.Message,
					FixHint:  "write only the fields declared in " + s.Path + ", with values of their type; details go in the body",
				})
			}
		}
	}
	return issues, nil
}

// DefaultSkillOversizeBytes is the skill-oversize threshold when the caller
// passes none: memory_get's truncation cap (getBodySoftCap in internal/mcp,
// which passes its own value), above which a note comes back as its outline
// and first chunk.
const DefaultSkillOversizeBytes = 24 << 10

// checkSkillOversize warns about a type:skill note that memory_get would
// truncate: an agent following the procedure gets its outline and first
// chunk only, and can miss the steps that follow (IMP-115).
func checkSkillOversize(_ context.Context, l *Linter, project string) ([]Issue, error) {
	notes, err := l.notesInProject(project)
	if err != nil {
		return nil, err
	}
	limit := l.skillOversizeBytes
	if limit <= 0 {
		limit = DefaultSkillOversizeBytes
	}
	var issues []Issue
	for _, n := range notes {
		if len(n.Content) <= limit {
			continue
		}
		if !noteHasType(parser.ParseFrontmatterFields(rawFrontmatter(n)), "skill") {
			continue
		}
		issues = append(issues, Issue{
			Severity: SeverityWarning,
			File:     n.Path,
			Rule:     "skill-oversize",
			Message: fmt.Sprintf(
				"skill is %d bytes and memory_get truncates notes above %d: an agent reading it gets the outline and the first chunk only",
				len(n.Content), limit),
			FixHint: "split it into an entry note plus reference notes (type:doc) under skills/<slug>/, each linked from the step that needs it",
		})
	}
	return issues, nil
}

// noteHasType reports whether a note's frontmatter declares type t, as the
// type field or as a type:<t> tag.
func noteHasType(fm map[string]any, t string) bool {
	if v, ok := fm["type"].(string); ok && v == t {
		return true
	}
	if tags, ok := fm["tags"].([]string); ok {
		for _, tg := range tags {
			if tg == "type:"+t {
				return true
			}
		}
	}
	return false
}

// DefaultHotOversizeBytes is the hot-oversize threshold when the vault does
// not configure one. hot.md is inlined into every memory_bootstrap payload
// and meant as a short cache of the current state; one past ~8 KiB has
// usually turned into a chronicle that belongs in log.md (in 2026-09, 6 of
// the 11 projects of a real vault were past it, the largest at 20 KiB).
// The bootstrap's switch to a lite hot.md stays at 16 KiB
// (autoLiteThreshold in internal/mcp): this threshold asks for grooming, it
// takes nothing out of the payload.
const DefaultHotOversizeBytes = 8 * 1024

// checkHotOversize warns when <project>/hot.md outgrows the threshold. The
// fix is grooming, not truncation: move history to log.md / plan Outcomes
// (memory_compact automates the log-shaped part).
func checkHotOversize(_ context.Context, l *Linter, project string) ([]Issue, error) {
	rel := project + "/hot.md"
	note, err := l.vault.Load(rel)
	if err != nil {
		return nil, nil // absent hot.md is scaffold territory, not an oversize issue
	}
	limit := l.hotOversizeBytes
	if limit <= 0 {
		limit = DefaultHotOversizeBytes
	}
	if int64(len(note.Content)) <= limit {
		return nil, nil
	}
	return []Issue{{
		Severity: SeverityWarning,
		File:     rel,
		Rule:     "hot-oversize",
		Message: fmt.Sprintf(
			"hot.md is %d bytes (threshold %d): it is inlined into every memory_bootstrap, so it now dominates session-start cost — groom it (move history to log.md / plan Outcomes, or memory_compact) instead of letting it grow",
			len(note.Content), limit),
	}}, nil
}

// optionalRules are known and selectable by name but excluded from the default
// run, because they are advisory and higher-noise on a dense vault. Name them
// explicitly in the `rules` argument to run them. See knownRules / selectRules.
var optionalRules = []ruleSpec{
	{name: "unlinked-mentions", defaultSeverity: SeverityInfo, fn: checkUnlinkedMentions},
}

// knownRules returns the full registry (default + optional) for name
// resolution and discovery. The default set alone is allRules.
func knownRules() []ruleSpec {
	out := make([]ruleSpec, 0, len(allRules)+len(optionalRules))
	out = append(out, allRules...)
	out = append(out, optionalRules...)
	return out
}

// rawFrontmatter returns a note's raw YAML frontmatter via the shared,
// path-aware parser primitive (parser.FrontmatterRawForPath), so the linter
// and the indexer can never disagree on whether a note has frontmatter or what
// its tags are — the inconsistency that made well-formed .html notes falsely
// flag frontmatter-missing (BUG-012).
func rawFrontmatter(n projectNote) string {
	return parser.FrontmatterRawForPath(n.Path, n.Content)
}

// notesInProject returns the notes under the given project prefix.
func (l *Linter) notesInProject(project string) ([]projectNote, error) {
	rows, err := l.index.NotesByPrefix(project)
	if err != nil {
		return nil, err
	}
	out := make([]projectNote, 0, len(rows))
	for _, n := range rows {
		note, err := l.vault.Load(n.Path)
		if err != nil {
			// Skip un-loadable files (stale index vs disk) — the lint
			// run shouldn't abort on a single corrupt file.
			continue
		}
		out = append(out, projectNote{
			Path:    n.Path,
			Title:   n.Title,
			Content: note.Content,
		})
	}
	return out, nil
}

type projectNote struct {
	Path    string
	Title   string
	Content []byte
}

// ---- broken-wikilink ----

func checkBrokenWikilink(ctx context.Context, l *Linter, project string) ([]Issue, error) {
	notes, err := l.notesInProject(project)
	if err != nil {
		return nil, err
	}
	var issues []Issue
	for _, n := range notes {
		outs, err := l.index.Outlinks(n.Path)
		if err != nil {
			return nil, err
		}
		for _, o := range outs {
			if o.TargetPath != "" || index.IsSelfLink(o.Target) || index.IsPlaceholder(o.Target) {
				continue
			}
			// The index resolves note targets only; an attachment embed like
			// ![[<hash>.webp]] always lands here unresolved. Mirror the
			// renderer's resolution (ResolveAttachmentByName) so a working
			// embed is not flagged — the false-positive mode that inflated a
			// real vault's lint report by ~138 entries.
			if ext := strings.ToLower(filepath.Ext(o.Target)); ext != "" {
				if _, isAttachExt := attach.AllowedExt[ext]; isAttachExt {
					if _, ok := l.vault.ResolveAttachmentByName(o.Target); ok {
						continue
					}
					issues = append(issues, Issue{
						Severity: SeverityWarning,
						File:     n.Path,
						Rule:     "broken-wikilink",
						Message:  fmt.Sprintf("embed target %q does not resolve to any attachment", o.Target),
						FixHint:  "restore the file under an attachments/ dir or remove the embed",
					})
					continue
				}
			}
			msg := fmt.Sprintf("wikilink target %q does not resolve to any note", o.Target)
			issues = append(issues, Issue{
				Severity: SeverityWarning,
				File:     n.Path,
				Rule:     "broken-wikilink",
				Message:  msg,
				FixHint:  "correct the target path or remove the wikilink",
			})
		}
	}
	return issues, nil
}

// ---- orphan-note ----

// orphanExcludedBase lists filenames that are index-like and legitimately
// have no incoming link. The exclusion is filename-based (not path-based) so
// it applies at any nesting level (e.g. `gosidian/skills/README.md`).
var orphanExcludedBase = map[string]struct{}{
	"README.md": {},
	"hot.md":    {},
	"log.md":    {},
	"CLAUDE.md": {},
}

// orphanExcludedDirs lists vault sub-paths whose notes are documentation
// index-like and never required to be linked. Matched as a prefix against
// the path segment after the project.
var orphanExcludedDirs = []string{
	"/docs/",
}

func isOrphanExempt(path string) bool {
	idx := strings.LastIndex(path, "/")
	base := path
	if idx >= 0 {
		base = path[idx+1:]
	}
	if _, ok := orphanExcludedBase[base]; ok {
		return true
	}
	for _, dir := range orphanExcludedDirs {
		if strings.Contains(path, dir) {
			return true
		}
	}
	return false
}

func checkOrphanNote(ctx context.Context, l *Linter, project string) ([]Issue, error) {
	notes, err := l.notesInProject(project)
	if err != nil {
		return nil, err
	}
	var issues []Issue
	for _, n := range notes {
		if isOrphanExempt(n.Path) {
			continue
		}
		bl, err := l.index.Backlinks(n.Path)
		if err != nil {
			return nil, err
		}
		outs, err := l.index.Outlinks(n.Path)
		if err != nil {
			return nil, err
		}
		if len(bl) == 0 && len(outs) == 0 {
			issues = append(issues, Issue{
				Severity: SeverityInfo,
				File:     n.Path,
				Rule:     "orphan-note",
				Message:  "note has no backlinks and no outlinks — unreachable from the vault graph",
				FixHint:  "link from a relevant note (e.g. the project README) or archive if obsolete",
			})
		}
	}
	return issues, nil
}

// ---- frontmatter-missing ----

func checkFrontmatterMissing(ctx context.Context, l *Linter, project string) ([]Issue, error) {
	notes, err := l.notesInProject(project)
	if err != nil {
		return nil, err
	}
	var issues []Issue
	for _, n := range notes {
		raw := rawFrontmatter(n)
		if strings.TrimSpace(raw) != "" {
			continue
		}
		issues = append(issues, Issue{
			Severity: SeverityError,
			File:     n.Path,
			Rule:     "frontmatter-missing",
			Message:  "note has no YAML frontmatter block",
			FixHint:  "add a --- / --- block with at least title and tags",
		})
	}
	return issues, nil
}

// ---- frontmatter-tag-unknown ----

// knownTagPrefixes are the tag namespaces allowed by the closed vocabulary.
// A tag either matches a prefix exactly (e.g. "pinned") or starts with
// "<prefix>:" and has a value from the corresponding set.
var knownBareTags = map[string]struct{}{
	"pinned": {},
}

var knownTagValues = map[string]map[string]struct{}{
	"type": {
		"memory":  {},
		"agent":   {},
		"plan":    {},
		"skill":   {},
		"doc":     {},
		"index":   {},
		"handoff": {},
		"insight": {},
		"image":   {},
		"table":   {},
	},
	"status": {
		"draft":       {},
		"in-progress": {},
		"done":        {},
		"archived":    {},
		"pending":     {},
		"snapshot":    {},
	},
}

// knownOpenNamespaces are namespaces whose value set is open by contract —
// the directives define `topic:<area>` as free-form, so any well-formed
// value is accepted. type: and status: stay closed: they are lifecycle
// vocabularies the tooling itself depends on.
var knownOpenNamespaces = map[string]struct{}{
	"topic": {},
}

// MaxProjectVocabEntries caps the per-project vocabulary declared in
// memory/conventions.md — entries beyond the cap are ignored, so a runaway
// declaration cannot void the closed vocabulary (IMP-075).
const MaxProjectVocabEntries = 64

// ProjectVocabularyNote returns the vault-relative path of the note whose
// frontmatter `tag_vocabulary:` field declares a project's extra tag
// vocabulary.
func ProjectVocabularyNote(project string) string {
	return project + "/memory/conventions.md"
}

// ValidVocabularyEntries filters raw tag_vocabulary entries down to the
// well-formed ones — bare tag, "ns:value", or "ns:*" namespace wildcard —
// trimmed and capped at MaxProjectVocabEntries. Shared by the lint rule
// and the bootstrap surfacing so both report the same effective vocabulary.
func ValidVocabularyEntries(entries []string) []string {
	if len(entries) == 0 {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		e = trimTag(e)
		if !validExtraTag(e) {
			continue
		}
		out = append(out, e)
		if len(out) == MaxProjectVocabEntries {
			break
		}
	}
	return out
}

// tagVocab is an extra vocabulary resolved for a single rule run: exact
// entries plus wildcard namespaces ("ns:*"). The zero value (nil maps) is
// a valid empty vocabulary.
type tagVocab struct {
	exact      map[string]struct{}
	namespaces map[string]struct{}
}

// newTagVocab indexes pre-validated entries into a tagVocab.
func newTagVocab(entries []string) tagVocab {
	v := tagVocab{
		exact:      make(map[string]struct{}, len(entries)),
		namespaces: make(map[string]struct{}),
	}
	for _, e := range entries {
		if ns, ok := wildcardNamespace(e); ok {
			v.namespaces[ns] = struct{}{}
			continue
		}
		v.exact[e] = struct{}{}
	}
	return v
}

// wildcardNamespace reports whether entry is a namespace wildcard
// ("ns:*") and returns the namespace.
func wildcardNamespace(entry string) (string, bool) {
	if strings.HasSuffix(entry, ":*") && len(entry) > 2 {
		return entry[:len(entry)-2], true
	}
	return "", false
}

// validTagValue reports whether a namespaced tag's value part is
// well-formed: non-empty, no whitespace, no further colon. Open and
// wildcard namespaces accept any value passing this check.
func validTagValue(v string) bool {
	return v != "" && !strings.ContainsAny(v, ": \t\n")
}

// projectVocab resolves the per-project extra vocabulary declared in
// memory/conventions.md frontmatter (`tag_vocabulary:`). Empty unless the
// linter was armed via WithProjectTagVocabulary — the declaration is
// inert otherwise. notes is the project note list the rule already
// loaded, so this costs no extra I/O.
func (l *Linter) projectVocab(project string, notes []projectNote) tagVocab {
	if !l.projectVocabEnabled {
		return tagVocab{}
	}
	target := ProjectVocabularyNote(project)
	for _, n := range notes {
		if n.Path != target {
			continue
		}
		entries := parser.FrontmatterList(rawFrontmatter(n), "tag_vocabulary")
		return newTagVocab(ValidVocabularyEntries(entries))
	}
	return tagVocab{}
}

// isKnownTag reports whether tag is part of the closed vocabulary. The
// project name itself is always considered valid (each project tags its
// own notes with its top-level folder name). When the linter has been
// extended via WithExtraAllowedTags (e.g. from
// .gosidian/config.toml [lint.frontmatter_tag_vocabulary]) or via a
// per-project vocabulary (extra), those entries are also accepted in
// addition to the built-in vocabulary.
func (l *Linter) isKnownTag(tag, project string, extra tagVocab) bool {
	if tag == project {
		return true
	}
	if _, ok := knownBareTags[tag]; ok {
		return true
	}
	if _, ok := l.extraAllowedTags[tag]; ok {
		return true
	}
	if _, ok := extra.exact[tag]; ok {
		return true
	}
	i := strings.IndexByte(tag, ':')
	if i <= 0 {
		return false
	}
	ns, val := tag[:i], tag[i+1:]
	if !validTagValue(val) {
		return false
	}
	if _, ok := knownOpenNamespaces[ns]; ok {
		return true
	}
	if _, ok := l.extraAllowedNamespaces[ns]; ok {
		return true
	}
	if _, ok := extra.namespaces[ns]; ok {
		return true
	}
	if vals, ok := knownTagValues[ns]; ok {
		if _, ok := vals[val]; ok {
			return true
		}
	}
	return false
}

// trimTag strips surrounding whitespace from a vocabulary entry. Used for
// extra_allowed entries from TOML where YAML/TOML round-trips can leave
// stray spaces.
func trimTag(s string) string {
	return strings.TrimSpace(s)
}

// validExtraTag reports whether s is a well-formed entry for the extra
// vocabulary. A valid entry is either a non-empty bare token (no ':') or
// a "<namespace>:<value>" pair where both halves are non-empty. Malformed
// entries (empty, leading/trailing colon, internal whitespace) are
// rejected so they can be skipped silently at load time without crashing
// the lint or producing surprising matches.
func validExtraTag(s string) bool {
	if s == "" {
		return false
	}
	if strings.ContainsAny(s, " \t\n") {
		return false
	}
	i := strings.IndexByte(s, ':')
	if i < 0 {
		return true
	}
	ns := s[:i]
	val := s[i+1:]
	return ns != "" && val != "" && !strings.Contains(val, ":")
}

func checkFrontmatterTagUnknown(ctx context.Context, l *Linter, project string) ([]Issue, error) {
	notes, err := l.notesInProject(project)
	if err != nil {
		return nil, err
	}
	extra := l.projectVocab(project, notes)
	fixHint := fmt.Sprintf(
		"use type:/topic:/status: namespaces, or declare the tag in %s frontmatter tag_vocabulary (exact or ns:* — needs the project's use_tag_vocabulary flag)",
		ProjectVocabularyNote(project))
	var issues []Issue
	for _, n := range notes {
		raw := rawFrontmatter(n)
		if strings.TrimSpace(raw) == "" {
			continue
		}
		fm := parser.ParseFrontmatterFields(raw)
		tags, ok := fm["tags"].([]string)
		if !ok {
			continue
		}
		for _, tag := range tags {
			if l.isKnownTag(tag, project, extra) || index.IsPlaceholder(tag) {
				continue
			}
			issues = append(issues, Issue{
				Severity: SeverityWarning,
				File:     n.Path,
				Rule:     "frontmatter-tag-unknown",
				Message:  fmt.Sprintf("tag %q is outside the closed vocabulary", tag),
				FixHint:  fixHint,
			})
		}
	}
	return issues, nil
}

// ---- status-incoherent ----

func checkStatusIncoherent(ctx context.Context, l *Linter, project string) ([]Issue, error) {
	hotPath := project + "/hot.md"
	hotBody, err := l.vault.Load(hotPath)
	var hot string
	if err == nil {
		hot = string(hotBody.Content)
	}
	// If there is no hot.md, we emit a single info issue at project level —
	// but status-incoherent is Warning, and absence of hot.md itself is a
	// separate project-health concern; skip silently here.
	if hot == "" {
		return nil, nil
	}

	// The notes hot.md links to, resolved by the index as broken-wikilink and
	// the backlinks resolve them: [[basename]], [[plans/basename]] and the
	// vault path all count. A substring match on the vault path only missed
	// the bare form the templates use (BUG-080).
	outs, err := l.index.Outlinks(hotPath)
	if err != nil {
		return nil, err
	}
	linked := make(map[string]bool, len(outs))
	for _, o := range outs {
		if o.TargetPath != "" {
			linked[o.TargetPath] = true
		}
	}

	// The in-progress plans, read as memory_query reads them: the type and
	// status fields, or the type:/status: tags of a note without the field
	// (the vault states status more often as a tag), case ignored.
	plans, _, err := l.index.Query(index.QueryOptions{
		Projects: []string{project},
		Where: []index.FieldCond{
			{Field: "type", Op: "eq", Values: []string{"plan"}},
			{Field: "status", Op: "eq", Values: []string{"in-progress"}},
		},
		Sort:  "path",
		Limit: maxInProgressPlans,
	})
	if err != nil {
		return nil, err
	}
	// A plan listed by a ```view block of hot.md counts too (IMP-127): the
	// Active plans section is often a view on the plans folder, and its rows
	// exist only once the view is computed.
	if len(views.FindBlocks(hotBody.Content)) > 0 {
		inProject := func(o index.QueryOptions) ([]index.QueryHit, int, error) {
			o.Projects = []string{project}
			return l.index.Query(o)
		}
		c := views.Context{This: views.ThisFields(hotPath, parser.ParseFrontmatterFields(parser.FrontmatterRawForPath(hotPath, hotBody.Content)))}
		rendered, _ := views.RenderNote(hotBody.Content, true, c, inProject)
		hot = string(rendered)
	}
	var issues []Issue
	for _, p := range plans {
		// A wikilink to the plan, or its path in the text.
		if linked[p.Path] || strings.Contains(hot, p.Path) || strings.Contains(hot, strings.TrimSuffix(p.Path, ".md")) {
			continue
		}
		// Hint the bare name only when it reaches this plan: another note
		// can answer it first (a title, or the same basename elsewhere).
		link := basenameNoExt(p.Path)
		if l.index.Resolve(link) != p.Path {
			link = strings.TrimSuffix(p.Path, filepath.Ext(p.Path))
		}
		issues = append(issues, Issue{
			Severity: SeverityWarning,
			File:     p.Path,
			Rule:     "status-incoherent",
			Message:  fmt.Sprintf("plan has status:in-progress but is not referenced in %s Active plans section", hotPath),
			FixHint:  fmt.Sprintf("add [[%s]] (or any wikilink that resolves to this plan) under ## Active plans in %s, or move the plan to status:draft/done", link, hotPath),
		})
	}
	return issues, nil
}

// maxInProgressPlans bounds the plans status-incoherent reads per project,
// far above any real project.
const maxInProgressPlans = 10000

// ---- unlinked-mentions (optional) ----

// minMentionLen is the shortest note title/basename considered a mention
// candidate. Short labels ("a", "ok", project acronyms) match too much prose
// and drown the signal, so they are skipped. 4 runes is a pragmatic floor.
const minMentionLen = 4

// fmBlockRe matches a leading YAML frontmatter block so it can be stripped
// before scanning prose — otherwise a note's own `title:` would self-match.
// Mirrors parser's internal frontmatter regex (kept local to avoid exporting it).
var fmBlockRe = regexp.MustCompile(`(?s)\A---\r?\n.*?\r?\n---\r?\n`)

// wikilinkBlockRe blanks out existing [[wikilinks]] so their inner text is not
// re-flagged as an unlinked mention of some other note.
var wikilinkBlockRe = regexp.MustCompile(`\[\[[^\]]*\]\]`)

// mentionCandidate is a note plus a precompiled word-boundary matcher for one
// of its labels (title or basename). Compiled once, scanned against every note.
type mentionCandidate struct {
	path  string
	label string
	re    *regexp.Regexp
}

// checkUnlinkedMentions flags notes whose prose names another note's title or
// basename without linking to it — the "unlinked mentions" of Obsidian, adapted
// to an explicit-wikilink vault. Advisory (Info) and opt-in: it is not in the
// default rule set. False-positive guards: frontmatter, fenced/inline code and
// existing wikilinks are stripped before scanning; labels under minMentionLen
// runes are ignored; a note already linking the target is skipped for that
// target; at most one issue per (source, target) pair.
func checkUnlinkedMentions(ctx context.Context, l *Linter, project string) ([]Issue, error) {
	notes, err := l.notesInProject(project)
	if err != nil {
		return nil, err
	}

	// Build mention candidates from every note's title + basename.
	var cands []mentionCandidate
	for _, n := range notes {
		seen := map[string]struct{}{}
		for _, label := range []string{n.Title, basenameNoExt(n.Path)} {
			label = strings.TrimSpace(label)
			if len([]rune(label)) < minMentionLen {
				continue
			}
			key := strings.ToLower(label)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			re, rerr := regexp.Compile(`(?i)\b` + regexp.QuoteMeta(label) + `\b`)
			if rerr != nil {
				continue
			}
			cands = append(cands, mentionCandidate{path: n.Path, label: label, re: re})
		}
	}

	var issues []Issue
	for _, n := range notes {
		// Resolve the set of note paths this note already links to.
		outs, err := l.index.Outlinks(n.Path)
		if err != nil {
			return nil, err
		}
		linked := make(map[string]struct{}, len(outs))
		for _, o := range outs {
			if o.TargetPath != "" {
				linked[o.TargetPath] = struct{}{}
			}
		}

		prose := mentionProse(n.Content)
		emitted := map[string]struct{}{}
		for _, c := range cands {
			if c.path == n.Path {
				continue // a note mentioning itself is not a missing link
			}
			if _, ok := linked[c.path]; ok {
				continue // already linked → not "unlinked"
			}
			if _, done := emitted[c.path]; done {
				continue // one issue per (source, target) pair
			}
			if !c.re.MatchString(prose) {
				continue
			}
			emitted[c.path] = struct{}{}
			issues = append(issues, Issue{
				Severity: SeverityInfo,
				File:     n.Path,
				Rule:     "unlinked-mentions",
				Message:  fmt.Sprintf("mentions %q (note %s) without a wikilink", c.label, c.path),
				FixHint:  fmt.Sprintf("consider linking the first mention as [[%s]]", strings.TrimSuffix(c.path, ".md")),
			})
		}
	}
	return issues, nil
}

// mentionProse returns the scannable prose of a note: frontmatter, fenced/inline
// code and existing wikilinks removed, so mention matching sees only free text.
func mentionProse(content []byte) string {
	src := fmBlockRe.ReplaceAllString(string(content), "")
	src = parser.StripCode(src)
	return wikilinkBlockRe.ReplaceAllString(src, " ")
}

// basenameNoExt returns the file name without directory or extension.
func basenameNoExt(path string) string {
	base := path
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		base = base[idx+1:]
	}
	if idx := strings.LastIndex(base, "."); idx > 0 {
		base = base[:idx]
	}
	return base
}
