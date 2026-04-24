package parser

import (
	"regexp"
	"strings"
)

// wiki-link: [[target]] or [[target|alias]]. Captures the entire inner
// content as a single group; target/alias splitting happens in
// parseWikiLinkInner so we can honor the \| escape sequence used inside
// markdown tables (where a literal | would otherwise terminate a cell).
var wikiLinkRe = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

// extractFrontmatterTags reads the YAML-ish `tags:` field from a
// frontmatter block and returns the values. Accepts both inline list and
// indented list syntax:
//
//   tags: [foo, bar]
//   tags: foo, bar
//   tags:
//     - foo
//     - bar
//
// Each value is trimmed and any leading '#' is stripped. Quotes (single or
// double) around values are removed. We avoid a real YAML dependency since
// only this one field matters and the spec is forgiving.
func extractFrontmatterTags(fm string) []string {
	var out []string
	lines := strings.Split(fm, "\n")
	for i := 0; i < len(lines); i++ {
		m := frontTagsKeyRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		rest := strings.TrimSpace(m[1])
		if rest != "" {
			// Inline form. Drop optional brackets, then split on comma.
			rest = strings.TrimPrefix(rest, "[")
			rest = strings.TrimSuffix(rest, "]")
			for _, tok := range strings.Split(rest, ",") {
				if t := normalizeTag(tok); t != "" {
					out = append(out, t)
				}
			}
		} else {
			// Block form: subsequent indented `- value` lines.
			for j := i + 1; j < len(lines); j++ {
				next := strings.TrimSpace(lines[j])
				if !strings.HasPrefix(next, "-") {
					break
				}
				if t := normalizeTag(strings.TrimPrefix(next, "-")); t != "" {
					out = append(out, t)
				}
			}
		}
		break
	}
	return out
}

func normalizeTag(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"'`)
	s = strings.TrimPrefix(s, "#")
	return strings.TrimSpace(s)
}

// parseWikiLinkInner takes the raw content between [[ and ]] and returns
// (target, alias). The \| sequence is recognized as a markdown-table escape
// for the pipe and treated identically to a regular | alias separator —
// matching Obsidian's behavior for wikilinks placed inside table cells.
func parseWikiLinkInner(content string) (target, alias string) {
	content = strings.ReplaceAll(content, `\|`, "|")
	parts := strings.SplitN(content, "|", 2)
	target = strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		alias = strings.TrimSpace(parts[1])
	}
	return target, alias
}

// #tag: letters, digits, underscore, dash, slash. Not starting with digit.
// Must be preceded by start-of-line or whitespace.
var tagRe = regexp.MustCompile(`(^|[\s>(])#([\p{L}_][\p{L}\p{N}_\-/]*)`)

// frontmatter: lines between --- / --- at top
var frontmatterRe = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---\r?\n`)
var frontTitleRe = regexp.MustCompile(`(?m)^title:\s*["']?(.*?)["']?\s*$`)
var frontTagsKeyRe = regexp.MustCompile(`(?m)^tags:\s*(.*)$`)
var frontScalarRe = regexp.MustCompile(`(?m)^([a-zA-Z_][\w-]*):\s*(.*)$`)

// ExtractFrontmatterRaw returns the YAML block between --- / --- markers at
// the top of body, with the enclosing markers stripped. Returns "" if no
// frontmatter is present.
func ExtractFrontmatterRaw(body []byte) string {
	m := frontmatterRe.FindStringSubmatch(string(body))
	if m == nil {
		return ""
	}
	return m[1]
}

// ParseFrontmatterFields extracts the common scalar fields and `tags` from a
// raw frontmatter block (the string returned by ExtractFrontmatterRaw).
//
// Supported fields: title, description, type, status, updated, created,
// tags (array). Any other top-level scalar key is also captured as a string.
// Returns an empty map for empty input. This is a best-effort parser — it
// does not depend on a real YAML library and handles the frontmatter style
// used throughout gosidian.
func ParseFrontmatterFields(raw string) map[string]any {
	out := map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return out
	}

	// Tags first (supports inline and block forms — delegates to the existing
	// extractFrontmatterTags which already handles both).
	if tags := extractFrontmatterTags(raw); len(tags) > 0 {
		out["tags"] = tags
	}

	// Scalar fields: capture every `key: value` pair where value is non-empty.
	// Skip the `tags` key (already handled above and value may span lines).
	// Skip keys that start with a dash (list markers) handled elsewhere.
	lines := strings.Split(raw, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		// Skip indented list continuations.
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}
		m := frontScalarRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key := m[1]
		if key == "tags" {
			continue
		}
		val := strings.TrimSpace(m[2])
		// A bare "key:" with no value may indicate a block list (e.g.
		// related_commits:\n  - abc) or an empty field — skip both.
		if val == "" {
			continue
		}
		// Strip optional surrounding quotes.
		val = strings.Trim(val, `"'`)
		out[key] = val
	}
	return out
}

// Heading is one entry in a note's table of contents.
type Heading struct {
	Level int    // 1..6
	Text  string // human text without the leading #s
	ID    string // anchor id matching goldmark's auto-heading-id output
}

// ExtractHeadings returns the list of ATX-style headings (#, ##, …) in the
// given markdown body, in document order. The id is computed using the same
// rules as goldmark's WithAutoHeadingID so anchor links match the rendered
// HTML: lowercase, non-alphanumerics → '-', collapsed dashes.
func ExtractHeadings(body []byte) []Heading {
	src := string(body)
	if m := frontmatterRe.FindStringSubmatch(src); m != nil {
		src = src[len(m[0]):]
	}
	src = stripCode(src)

	var out []Heading
	for _, line := range strings.Split(src, "\n") {
		i := 0
		for i < len(line) && line[i] == '#' && i < 6 {
			i++
		}
		if i == 0 || i >= len(line) || line[i] != ' ' {
			continue
		}
		text := strings.TrimSpace(line[i+1:])
		if text == "" {
			continue
		}
		out = append(out, Heading{
			Level: i,
			Text:  text,
			ID:    headingID(text),
		})
	}
	return out
}

// ExtractSection returns the slice of body that belongs to the given heading
// — that is, from the heading line itself up to (but not including) the next
// heading at the same level or a higher (smaller-number) level. Match is
// case-insensitive and trims surrounding whitespace. Returns "" when the
// heading is not found. Frontmatter is stripped before scanning.
func ExtractSection(body []byte, heading string) string {
	src := string(body)
	if m := frontmatterRe.FindStringSubmatch(src); m != nil {
		src = src[len(m[0]):]
	}
	heading = strings.TrimSpace(heading)
	if heading == "" {
		return ""
	}
	headingLower := strings.ToLower(heading)

	lines := strings.Split(src, "\n")
	startIdx := -1
	startLevel := 0
	for i, line := range lines {
		level, text := parseHeadingLine(line)
		if level == 0 {
			continue
		}
		if strings.ToLower(text) == headingLower {
			startIdx = i
			startLevel = level
			break
		}
	}
	if startIdx < 0 {
		return ""
	}
	endIdx := len(lines)
	for j := startIdx + 1; j < len(lines); j++ {
		level, _ := parseHeadingLine(lines[j])
		if level > 0 && level <= startLevel {
			endIdx = j
			break
		}
	}
	return strings.Join(lines[startIdx:endIdx], "\n")
}

// parseHeadingLine returns the heading level (1-6) and trimmed text for an
// ATX heading line, or 0 when the line is not a heading.
func parseHeadingLine(line string) (int, string) {
	i := 0
	for i < len(line) && line[i] == '#' && i < 6 {
		i++
	}
	if i == 0 || i >= len(line) || line[i] != ' ' {
		return 0, ""
	}
	return i, strings.TrimSpace(line[i+1:])
}

// headingID mirrors goldmark's default auto-id slug.
func headingID(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := b.String()
	for len(out) > 0 && out[len(out)-1] == '-' {
		out = out[:len(out)-1]
	}
	return out
}

// Extract parses a markdown body (raw bytes) and returns wiki-links, tags, and
// an optional frontmatter-provided title.
//
// We intentionally use regex instead of goldmark AST walking for extraction,
// because it sidesteps code-span/fence edge cases at a small cost in accuracy.
// Code fences are stripped before scanning.
func Extract(body []byte) (links []WikiLinkRef, tags []string, title string) {
	src := string(body)

	var fmTags []string
	if m := frontmatterRe.FindStringSubmatch(src); m != nil {
		if tm := frontTitleRe.FindStringSubmatch(m[1]); tm != nil {
			title = strings.TrimSpace(tm[1])
		}
		fmTags = extractFrontmatterTags(m[1])
		src = src[len(m[0]):]
	}

	// strip fenced code blocks ``` ... ``` and inline `...`
	stripped := stripCode(src)

	for _, m := range wikiLinkRe.FindAllStringSubmatch(stripped, -1) {
		target, alias := parseWikiLinkInner(m[1])
		if target == "" {
			continue
		}
		links = append(links, WikiLinkRef{Target: target, Alias: alias})
	}

	seen := map[string]struct{}{}
	// Frontmatter tags first so the source-of-truth ordering (file → body)
	// is preserved when both forms are present.
	for _, t := range fmTags {
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		tags = append(tags, t)
	}
	for _, m := range tagRe.FindAllStringSubmatch(stripped, -1) {
		t := m[2]
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		tags = append(tags, t)
	}
	return links, tags, title
}

func stripCode(s string) string {
	var out strings.Builder
	out.Grow(len(s))
	inFence := false
	lines := strings.Split(s, "\n")
	for _, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			inFence = !inFence
			out.WriteByte('\n')
			continue
		}
		if inFence {
			out.WriteByte('\n')
			continue
		}
		// inline code: replace `...` with spaces of equal length
		line = replaceInlineCode(line)
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String()
}

func replaceInlineCode(line string) string {
	var b strings.Builder
	b.Grow(len(line))
	in := false
	for _, r := range line {
		if r == '`' {
			in = !in
			b.WriteByte(' ')
			continue
		}
		if in {
			b.WriteByte(' ')
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}
