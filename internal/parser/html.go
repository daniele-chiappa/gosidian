package parser

import (
	"html"
	"regexp"
	"strings"
)

// HTML single-file note extraction.
//
// gosidian treats a `.html` file (HTML + inline CSS + inline JS) as a
// first-class note when the html-notes feature is enabled (see ADR-011). To
// participate in the graph and search it must expose the same signals a
// markdown note does — title, tags, wiki-links, and a plain-text body for FTS —
// which ExtractHTML derives without a full HTML parser (pure stdlib, regex +
// html.UnescapeString, no new dependency, CGO-free).

var (
	// htmlFrontmatterRe matches a leading HTML comment wrapping a standard
	// `--- YAML ---` frontmatter block. Capture group 1 is the inner YAML,
	// fed to the same machinery markdown notes use. Example:
	//
	//   <!--
	//   ---
	//   title: Dashboard
	//   tags: [proj, type:doc]
	//   ---
	//   -->
	//   <html>…</html>
	htmlFrontmatterRe = regexp.MustCompile(`(?s)\A\s*<!--\s*\r?\n---\r?\n(.*?)\r?\n---\s*\r?\n?\s*-->`)
	htmlScriptRe      = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script\s*>`)
	htmlStyleRe       = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style\s*>`)
	htmlCommentRe     = regexp.MustCompile(`(?s)<!--.*?-->`)
	htmlTagRe         = regexp.MustCompile(`(?s)<[^>]+>`)
)

// ExtractHTML parses a single-file HTML note and returns its wiki-links, tags,
// frontmatter title, and a plain-text projection for full-text search.
//
// Metadata convention (keeps the markdown parser reusable):
//   - frontmatter: a leading HTML comment wrapping a `--- YAML ---` block.
//   - tags: ONLY from that frontmatter. A bare '#' is far too common in HTML/CSS
//     (hex colors, fragment hrefs) to scan the body for `#tags`.
//   - wiki-links: `[[target]]` in the body, after <script>/<style>/comments are
//     stripped, so links inside code or markup attributes are not picked up.
//   - text: tag-stripped, entity-decoded visible text, used as the FTS body.
func ExtractHTML(body []byte) (links []WikiLinkRef, tags []string, title, text string) {
	src := string(body)

	// 1. Frontmatter (title + tags). The convention is a leading HTML comment
	//    wrapping a `--- YAML ---` block (ADR-011, invisible when rendered). We
	//    are also tolerant of a bare markdown-style `--- YAML ---` at the very
	//    top (some authors/agents write it that way): it is parsed the same and
	//    the renderer strips it from the view so it never shows.
	rest := src
	if m := htmlFrontmatterRe.FindStringSubmatch(src); m != nil {
		raw := m[1]
		title = frontmatterTitle(raw)
		tags = extractFrontmatterTags(raw)
		rest = src[len(m[0]):]
	} else if m := frontmatterRe.FindStringSubmatch(src); m != nil {
		raw := m[1]
		title = frontmatterTitle(raw)
		tags = extractFrontmatterTags(raw)
		rest = src[len(m[0]):]
	}

	// 2. Strip executable/style content + comments before scanning. Order
	//    matters: remove <script>/<style> spans first so a comment inside them
	//    can't desync the comment stripper.
	stripped := htmlScriptRe.ReplaceAllString(rest, " ")
	stripped = htmlStyleRe.ReplaceAllString(stripped, " ")
	stripped = htmlCommentRe.ReplaceAllString(stripped, " ")

	// 3. Wiki-links from the remaining body, de-duplicated on (target, alias).
	seen := map[string]struct{}{}
	for _, m := range wikiLinkRe.FindAllStringSubmatch(stripped, -1) {
		target, alias := parseWikiLinkInner(m[1])
		if target == "" {
			continue
		}
		key := strings.ToLower(target) + "\x00" + strings.ToLower(alias)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		links = append(links, WikiLinkRef{Target: target, Alias: alias})
	}

	// 4. FTS text: drop remaining tags, decode entities, collapse whitespace.
	plain := htmlTagRe.ReplaceAllString(stripped, " ")
	text = strings.Join(strings.Fields(html.UnescapeString(plain)), " ")
	return links, tags, title, text
}

// ExtractHTMLFrontmatterRaw returns the inner YAML of an HTML note's leading
// comment-wrapped frontmatter block (the convention ExtractHTML parses), or ""
// when absent. It lets callers that only need the frontmatter — e.g. the linter
// deciding whether a note has one — reuse the same detection as the indexer
// without running the full HTML extraction. The returned string mirrors what
// ExtractFrontmatterRaw yields for markdown notes, so it feeds the same
// ParseFrontmatterFields machinery.
func ExtractHTMLFrontmatterRaw(body []byte) string {
	if m := htmlFrontmatterRe.FindStringSubmatch(string(body)); m != nil {
		return m[1]
	}
	// Tolerate a bare markdown-style `--- YAML ---` block at the top of an
	// .html note (parsed identically; the renderer strips it from the view).
	if m := frontmatterRe.FindStringSubmatch(string(body)); m != nil {
		return m[1]
	}
	return ""
}
