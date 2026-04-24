package lint

import (
	"context"
	"fmt"
	"strings"

	"github.com/gosidian/gosidian/internal/parser"
)

// allRules is the registry of baseline rules run by default, in stable
// declaration order (so the output is deterministic).
var allRules = []ruleSpec{
	{name: "broken-wikilink", defaultSeverity: SeverityWarning, fn: checkBrokenWikilink},
	{name: "orphan-note", defaultSeverity: SeverityInfo, fn: checkOrphanNote},
	{name: "frontmatter-missing", defaultSeverity: SeverityError, fn: checkFrontmatterMissing},
	{name: "frontmatter-tag-unknown", defaultSeverity: SeverityWarning, fn: checkFrontmatterTagUnknown},
	{name: "status-incoherent", defaultSeverity: SeverityWarning, fn: checkStatusIncoherent},
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
			if o.TargetPath != "" {
				continue
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
	"README.md":  {},
	"hot.md":     {},
	"log.md":     {},
	"CLAUDE.md":  {},
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
		raw := parser.ExtractFrontmatterRaw(n.Content)
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
	},
	"topic": {
		"mcp":     {},
		"webui":   {},
		"vault":   {},
		"index":   {},
		"gitsync": {},
		"auth":    {},
		"deploy":  {},
		"meta":    {},
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

// isKnownTag reports whether tag is part of the closed vocabulary. The
// project name itself is always considered valid (each project tags its
// own notes with its top-level folder name).
func isKnownTag(tag, project string) bool {
	if tag == project {
		return true
	}
	if _, ok := knownBareTags[tag]; ok {
		return true
	}
	if i := strings.IndexByte(tag, ':'); i > 0 {
		ns := tag[:i]
		val := tag[i+1:]
		if vals, ok := knownTagValues[ns]; ok {
			if _, ok := vals[val]; ok {
				return true
			}
		}
	}
	return false
}

func checkFrontmatterTagUnknown(ctx context.Context, l *Linter, project string) ([]Issue, error) {
	notes, err := l.notesInProject(project)
	if err != nil {
		return nil, err
	}
	var issues []Issue
	for _, n := range notes {
		raw := parser.ExtractFrontmatterRaw(n.Content)
		if strings.TrimSpace(raw) == "" {
			continue
		}
		fm := parser.ParseFrontmatterFields(raw)
		tags, ok := fm["tags"].([]string)
		if !ok {
			continue
		}
		for _, tag := range tags {
			if isKnownTag(tag, project) {
				continue
			}
			issues = append(issues, Issue{
				Severity: SeverityWarning,
				File:     n.Path,
				Rule:     "frontmatter-tag-unknown",
				Message:  fmt.Sprintf("tag %q is outside the closed vocabulary", tag),
				FixHint:  "use type:/topic:/status: namespaces or document the new tag in gosidian/memory/conventions.md",
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

	notes, err := l.notesInProject(project)
	if err != nil {
		return nil, err
	}
	var issues []Issue
	for _, n := range notes {
		raw := parser.ExtractFrontmatterRaw(n.Content)
		if strings.TrimSpace(raw) == "" {
			continue
		}
		fm := parser.ParseFrontmatterFields(raw)
		isPlan := false
		if v, ok := fm["type"].(string); ok && v == "plan" {
			isPlan = true
		}
		if !isPlan {
			if tags, ok := fm["tags"].([]string); ok {
				for _, tg := range tags {
					if tg == "type:plan" {
						isPlan = true
						break
					}
				}
			}
		}
		if !isPlan {
			continue
		}
		status, _ := fm["status"].(string)
		if status != "in-progress" {
			continue
		}
		// Look for the plan path or a wikilink to it in hot.md.
		if strings.Contains(hot, n.Path) || strings.Contains(hot, strings.TrimSuffix(n.Path, ".md")) {
			continue
		}
		issues = append(issues, Issue{
			Severity: SeverityWarning,
			File:     n.Path,
			Rule:     "status-incoherent",
			Message:  fmt.Sprintf("plan has status:in-progress but is not referenced in %s Active plans section", hotPath),
			FixHint:  "add a wikilink to this plan under ## Active plans in hot.md, or move the plan to status:draft/done",
		})
	}
	return issues, nil
}
