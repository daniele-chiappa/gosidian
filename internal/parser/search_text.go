package parser

import (
	"path"
	"regexp"
	"strings"
)

// searchLinkRe matches a wikilink or an embed, the ! included.
var searchLinkRe = regexp.MustCompile(`!?\[\[([^\]]+)\]\]`)

// SearchText is s as the full-text index reads it (IMP-120): every
// [[target#heading|alias]] and ![[…]] outside code becomes the words a
// reader sees — the alias, or else the target's file name and heading — so
// the folders and project of a link path stop matching a search ("plans",
// "services" found nearly every note that linked a page under them). A
// block reference (#^id) is dropped. Fenced and inline code stay as
// written. The links table keeps the full targets: backlinks, the graph and
// resolution do not change.
func SearchText(s string) string {
	if !strings.Contains(s, "[[") {
		return s
	}
	var out strings.Builder
	out.Grow(len(s))
	var fences Fences
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if i > 0 {
			out.WriteByte('\n')
		}
		if fences.Code(line) || !strings.Contains(line, "[[") {
			out.WriteString(line)
			continue
		}
		// Outside inline code only: the even segments between backticks.
		for j, seg := range strings.Split(line, "`") {
			if j > 0 {
				out.WriteByte('`')
			}
			if j%2 == 1 {
				out.WriteString(seg)
				continue
			}
			out.WriteString(searchLinkRe.ReplaceAllStringFunc(seg, func(m string) string {
				return linkWords(searchLinkRe.FindStringSubmatch(m)[1])
			}))
		}
	}
	return out.String()
}

// linkWords is what a reader sees of a link: its alias, or else the target's
// file name (without .md or .html) and heading.
func linkWords(inner string) string {
	target, alias := parseWikiLinkInner(inner)
	if alias != "" {
		return alias
	}
	heading := ""
	if i := strings.IndexByte(target, '#'); i >= 0 {
		target, heading = target[:i], target[i+1:]
	}
	if strings.HasPrefix(heading, "^") {
		heading = "" // a block reference names no words
	}
	heading = strings.TrimSpace(strings.ReplaceAll(heading, "#", " "))
	name := ""
	if target = strings.TrimSuffix(strings.TrimSpace(target), "/"); target != "" {
		name = path.Base(target)
		if ext := strings.ToLower(path.Ext(name)); ext == ".md" || ext == ".html" {
			name = name[:len(name)-len(ext)]
		}
	}
	return strings.TrimSpace(name + " " + heading)
}
