package parser

import (
	"bytes"
	stdhtml "html"
	"net/url"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
)

// Resolver maps a wiki-link target to a vault-relative path. Returns empty
// string when the target cannot be resolved.
type Resolver interface {
	Resolve(target string) string
}

type ResolverFunc func(string) string

func (f ResolverFunc) Resolve(t string) string { return f(t) }

// Renderer renders markdown to HTML, converting [[wiki-links]] via the given
// resolver. Tags `#tag` are rendered as links to /tags/{tag}. Standard Markdown
// (GFM) is rendered via goldmark.
type Renderer struct {
	md goldmark.Markdown
}

func NewRenderer() *Renderer {
	md := goldmark.New(
		goldmark.WithExtensions(
			extension.GFM,
			extension.Footnote,
			extension.Typographer,
			highlighting.NewHighlighting(
				highlighting.WithStyle("github-dark"),
				highlighting.WithFormatOptions(),
			),
		),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(gmhtml.WithUnsafe()),
	)
	return &Renderer{md: md}
}

// Render converts the body to HTML after pre-processing wiki-links and tags.
// Wiki-links and tags are rewritten before goldmark sees them, which is
// simpler than writing a goldmark extension and good enough for our needs.
// The body may contain frontmatter; it's stripped.
func (r *Renderer) Render(body []byte, resolver Resolver) (string, error) {
	src := string(body)
	if m := frontmatterRe.FindStringSubmatch(src); m != nil {
		src = src[len(m[0]):]
	}

	// Rewrite Obsidian-style callouts (> [!warning] Title ...) into HTML
	// wrappers BEFORE the wiki-link / tag pass so the callout body is still
	// plain markdown when those rewrites run.
	src = preprocessCallouts(src)

	// Replace wiki-links and tags outside code regions.
	src = processOutsideCode(src, func(segment string) string {
		segment = wikiLinkRe.ReplaceAllStringFunc(segment, func(match string) string {
			sub := wikiLinkRe.FindStringSubmatch(match)
			target, alias := parseWikiLinkInner(sub[1])

			// Optional #heading suffix → block/heading reference.
			heading := ""
			if i := strings.Index(target, "#"); i >= 0 {
				heading = target[i+1:]
				target = strings.TrimSpace(target[:i])
			}

			text := alias
			if text == "" {
				if heading != "" && target == "" {
					text = "#" + heading
				} else if heading != "" {
					text = target + " › " + heading
				} else {
					text = target
				}
			}
			resolved := ""
			if target != "" && resolver != nil {
				resolved = resolver.Resolve(target)
			}
			class := "wikilink"
			var href string
			extraAttr := ""
			if target == "" && heading != "" {
				// In-note jump: just an anchor on the current page.
				href = "#" + headingID(heading)
			} else if resolved == "" {
				class += " unresolved"
				href = "/notes/new?title=" + url.QueryEscape(target)
			} else {
				href = "/notes/" + urlEscapePath(resolved)
				if heading != "" {
					href += "#" + headingID(heading)
				}
				// Emit the resolved vault path as a data attribute so the
				// client-side wikilink-preview script can fetch an excerpt.
				extraAttr = ` data-preview-path="` + stdhtml.EscapeString(resolved) + `"`
			}
			return `<a class="` + class + `" href="` + href + `"` + extraAttr + `>` + stdhtml.EscapeString(text) + `</a>`
		})
		segment = tagRe.ReplaceAllStringFunc(segment, func(match string) string {
			m := tagRe.FindStringSubmatch(match)
			prefix := m[1]
			tag := m[2]
			return prefix + `<a class="tag" href="/tags/` + url.PathEscape(tag) + `">#` + stdhtml.EscapeString(tag) + `</a>`
		})
		return segment
	})

	var buf bytes.Buffer
	if err := r.md.Convert([]byte(src), &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// processOutsideCode walks the source and applies fn to slices outside of
// fenced and inline code regions.
func processOutsideCode(src string, fn func(string) string) string {
	var out strings.Builder
	lines := strings.Split(src, "\n")
	inFence := false
	for i, line := range lines {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") || strings.HasPrefix(trim, "~~~") {
			inFence = !inFence
			out.WriteString(line)
			if i < len(lines)-1 {
				out.WriteByte('\n')
			}
			continue
		}
		if inFence {
			out.WriteString(line)
			if i < len(lines)-1 {
				out.WriteByte('\n')
			}
			continue
		}
		// Split line on backticks, transforming only even (non-code) parts.
		parts := strings.Split(line, "`")
		for j, p := range parts {
			if j%2 == 0 {
				out.WriteString(fn(p))
			} else {
				out.WriteByte('`')
				out.WriteString(p)
				out.WriteByte('`')
			}
		}
		if i < len(lines)-1 {
			out.WriteByte('\n')
		}
	}
	return out.String()
}

var calloutFirstRe = regexp.MustCompile(`^> \[!(\w+)\][ \t]*(.*)$`)

// preprocessCallouts rewrites Obsidian-style callout blockquotes into a
// <div class="callout callout-<kind>"> wrapper with the body re-emitted as
// plain markdown (so goldmark still renders links, bold, code inside it).
//
// Input:
//
//	> [!warning] Attenzione
//	> first line **bold**
//	> second line
//
// Output:
//
//	<div class="callout callout-warning">
//	<div class="callout-title">⚠ Attenzione</div>
//
//	first line **bold**
//	second line
//
//	</div>
//
// The blank lines around the inner body are required so goldmark re-enters
// markdown mode inside the HTML block.
func preprocessCallouts(src string) string {
	lines := strings.Split(src, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		m := calloutFirstRe.FindStringSubmatch(line)
		if m == nil {
			out = append(out, line)
			continue
		}
		kind := strings.ToLower(m[1])
		title := strings.TrimSpace(m[2])
		if title == "" {
			title = calloutDefaultTitle(kind)
		}

		// Collect consecutive blockquote lines after the marker line.
		j := i + 1
		var bodyLines []string
		for j < len(lines) {
			l := lines[j]
			if strings.HasPrefix(l, "> ") {
				bodyLines = append(bodyLines, strings.TrimPrefix(l, "> "))
				j++
				continue
			}
			if l == ">" {
				bodyLines = append(bodyLines, "")
				j++
				continue
			}
			break
		}

		out = append(out, `<div class="callout callout-`+kind+`">`)
		out = append(out, `<div class="callout-title">`+calloutIcon(kind)+` `+stdhtml.EscapeString(title)+`</div>`)
		out = append(out, "")
		out = append(out, bodyLines...)
		out = append(out, "")
		out = append(out, `</div>`)
		i = j - 1
	}
	return strings.Join(out, "\n")
}

func calloutIcon(kind string) string {
	switch kind {
	case "note", "info":
		return "ℹ"
	case "tip", "hint":
		return "💡"
	case "warning", "caution":
		return "⚠"
	case "danger", "error", "bug":
		return "⛔"
	case "success", "done":
		return "✓"
	case "important", "star":
		return "★"
	case "plan":
		return "📋"
	case "adr":
		return "🧭"
	case "skill":
		return "🛠"
	case "quote":
		return "❝"
	case "example":
		return "✎"
	default:
		return "ℹ"
	}
}

func calloutDefaultTitle(kind string) string {
	if kind == "" {
		return ""
	}
	return strings.ToUpper(kind[:1]) + kind[1:]
}

func urlEscapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}
