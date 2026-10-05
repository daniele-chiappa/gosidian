package views

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"path"
	"regexp"
	"strings"

	"github.com/gosidian/gosidian/internal/parser"
)

// Embeds (IMP-127 iteration 3, phase 3): a line made of `![[note#Heading]]`
// or `![[note]]` alone, outside code blocks, includes that section or the
// whole note, as Obsidian does. The views and values it includes are
// computed with `this` the note that embeds them, so a view written once
// serves as a model in many notes. Only a markdown note the reader may open
// is included (an image embed stays an image, a note the reader cannot open
// stays a link), and only one level: an embed inside the included text
// becomes a link, and so does a note that embeds itself.

var embedLineRe = regexp.MustCompile(`^[ \t]*!\[\[([^\]\|]+?)(\|[^\]]*)?\]\][ \t]*$`)

// Markers of an embed for an agent: the line stays, and the included text
// follows it between these, so the agent sees both the source and the
// result, and knows not to copy the result into the file.
const (
	embedOpen  = "<!-- gosidian:embed — included from %s when the note is read; not part of the file, never copy it into the note -->"
	embedClose = "<!-- /gosidian:embed -->"
)

var persistedEmbedRe = regexp.MustCompile(`(?s)\n?<!-- gosidian:embed — .*?` + regexp.QuoteMeta(embedClose) + `\n?`)

// embedLine is an embed of a note found on line i of a body.
type embedLine struct {
	target, heading string // as written
	path            string // the note it resolves to for the reader
}

// findEmbeds returns, for each line of lines, the embed it holds or nil: a
// line of its own, outside fences, naming a markdown note c resolves (to
// another note than self).
func findEmbeds(lines []string, self string, c Context) []*embedLine {
	out := make([]*embedLine, len(lines))
	var fence string
	for i, line := range lines {
		if m := fenceRe.FindStringSubmatch(line); m != nil {
			switch {
			case fence == "":
				fence = m[1]
			case m[2] == "" && strings.HasPrefix(m[1], fence[:1]) && len(m[1]) >= len(fence):
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		m := embedLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		target, heading, _ := strings.Cut(strings.TrimSpace(m[1]), "#")
		target = strings.TrimSpace(target)
		if target == "" || c.Resolve == nil {
			continue
		}
		p := c.Resolve(target)
		if p == "" || !strings.EqualFold(path.Ext(p), ".md") || p == self {
			continue
		}
		out[i] = &embedLine{target: target, heading: strings.TrimSpace(heading), path: p}
	}
	return out
}

// ExpandEmbeds includes the embeds of body (see above) for the note c.This
// describes, loading the notes with c.Load. For an agent the embed line stays
// and the included text follows it between gosidian:embed markers; for the web
// UI the included text sits between a head that links to its origin and a
// closing rule, two siblings rather than a wrapper, so the views it includes
// stay at the top level where the editors find them. The second value hashes
// what was included ("" when nothing was).
func ExpandEmbeds(body []byte, agent bool, c Context) ([]byte, string) {
	if c.Load == nil || !bytes.Contains(body, []byte("![[")) {
		return body, ""
	}
	body = persistedEmbedRe.ReplaceAll(body, []byte("\n"))
	self := ""
	if p := c.This["path"]; len(p) > 0 {
		self = p[0]
	}
	lines := strings.Split(string(body), "\n")
	embeds := findEmbeds(lines, self, c)
	h := sha256.New()
	n := 0
	var out strings.Builder
	for i, line := range lines {
		e := embeds[i]
		if e == nil {
			out.WriteString(line)
			if i < len(lines)-1 {
				out.WriteByte('\n')
			}
			continue
		}
		n++
		text, origin := e.include(c)
		h.Write([]byte(e.path + "#" + e.heading + "\n" + text))
		if agent {
			out.WriteString(line + "\n" + fmt.Sprintf(embedOpen, origin) + "\n\n" + text + "\n" + embedClose + "\n")
			continue
		}
		ref := strings.TrimSuffix(origin, "]]")[2:]
		label := path.Base(strings.TrimSuffix(e.path, ".md"))
		if e.heading != "" {
			label += " › " + e.heading
		}
		fmt.Fprintf(&out, "<div class=\"gosidian-embed-start\" data-embed=\"%s\">[[%s|%s]]</div>\n\n%s\n\n<div class=\"gosidian-embed-end\"></div>\n",
			html.EscapeString(ref), ref, strings.ReplaceAll(label, "]", ""), text)
	}
	if n == 0 {
		return body, ""
	}
	return []byte(out.String()), hex.EncodeToString(h.Sum(nil))[:16]
}

// include returns the text an embed includes, and the wikilink to its origin
// ([[path#Heading]]). The section is found as memory_get_section finds it
// (its text, or an ID at its start); a heading that names none includes a
// warning instead. The embeds inside the included text become links: one
// level only.
func (e *embedLine) include(c Context) (text, origin string) {
	target := strings.TrimSuffix(e.path, ".md")
	origin = "[[" + target + "]]"
	raw, ok := c.Load(e.path)
	if !ok {
		return "> ⚠️ embed: " + origin + " cannot be read\n", origin
	}
	body := parser.BodyAfterFrontmatter(raw)
	if e.heading != "" {
		resolved, _ := parser.ResolveHeading(raw, e.heading)
		origin = "[[" + target + "#" + e.heading + "]]"
		if resolved == "" {
			return fmt.Sprintf("> ⚠️ embed: %s has no heading %q\n", "[["+target+"]]", e.heading), origin
		}
		body = parser.ExtractSection(raw, resolved)
	}
	body = strings.Trim(body, "\n")
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if m := embedLineRe.FindStringSubmatch(l); m != nil && !isImageTarget(m[1]) {
			lines[i] = strings.Replace(l, "![[", "[[", 1)
		}
	}
	return strings.Join(lines, "\n"), origin
}

// isImageTarget reports a target with the extension of a file that is not a
// note: an image embed, which the renderer shows as an image.
func isImageTarget(target string) bool {
	t, _, _ := strings.Cut(target, "#")
	switch strings.ToLower(path.Ext(strings.TrimSpace(t))) {
	case "", ".md", ".html":
		return false
	}
	return true
}

// EmbedOffsets returns the byte offsets of the lines of body that may
// embed a note: ![[target]] alone on its line, outside a fence, whose
// target is no image. Nothing is resolved, so it needs no reader: an
// outline uses it to say which sections include another note's text.
func EmbedOffsets(body []byte) []int {
	if !bytes.Contains(body, []byte("![[")) {
		return nil
	}
	var out []int
	var fence string
	off := 0
	for _, line := range strings.SplitAfter(string(body), "\n") {
		start := off
		off += len(line)
		line = strings.TrimRight(line, "\r\n")
		if m := fenceRe.FindStringSubmatch(line); m != nil {
			switch {
			case fence == "":
				fence = m[1]
			case m[2] == "" && strings.HasPrefix(m[1], fence[:1]) && len(m[1]) >= len(fence):
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		if m := embedLineRe.FindStringSubmatch(line); m != nil && !isImageTarget(m[1]) {
			out = append(out, start)
		}
	}
	return out
}

// CountEmbeds counts the embeds of body that ExpandEmbeds would include for
// the reader c describes: what a read without render_views leaves out.
func CountEmbeds(body []byte, c Context) int {
	if c.Load == nil || !bytes.Contains(body, []byte("![[")) {
		return 0
	}
	self := ""
	if p := c.This["path"]; len(p) > 0 {
		self = p[0]
	}
	n := 0
	for _, e := range findEmbeds(strings.Split(string(body), "\n"), self, c) {
		if e != nil {
			n++
		}
	}
	return n
}
