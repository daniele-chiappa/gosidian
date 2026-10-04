package parser

import (
	"strings"

	"go.yaml.in/yaml/v3"
)

// The single reader of a note's frontmatter (IMP-138 P3). Until v2.47 the
// index, the views and most tools read the frontmatter line by line while the
// database schemas used a YAML parser, so the same lines could be read two
// ways (BUG-089). Now a frontmatter that is valid YAML is read with yaml.Node,
// every value kept as the text written: a database's schema types it, never
// the parser, so 007 stays 007 and a date stays its text. A frontmatter that
// is not valid YAML falls back to the line reader (the line* functions), so a
// quoting slip does not hide a note; lint frontmatter-invalid-yaml reports it.

// FMKind is the shape of a top-level frontmatter value.
type FMKind int

const (
	// FMEmpty is a key with no value (`key:`), or a nested shape the
	// fallback reader cannot tell apart from one.
	FMEmpty FMKind = iota
	FMScalar
	FMList
	FMMap
)

// FMEntry is one top-level key of a frontmatter, in the order written.
type FMEntry struct {
	Key  string
	Kind FMKind
	// Text is a scalar's value as written, unquoted.
	Text string
	// Items are a list's items as text.
	Items []string
	// Sub holds a map's keys: a string, or a []string for a list.
	Sub map[string]any
	// Comment is what YAML read as a comment after an unquoted scalar:
	// `title: alert #3` is "alert" with the comment "#3". Usually a # that
	// belongs to the value and needs quotes; lint and the write notices say
	// so.
	Comment string
}

// CutByComment lists the top-level scalars that YAML cuts at a comment (see
// FMEntry.Comment), as "key: what YAML reads".
func CutByComment(raw string) []string {
	es, ok := yamlEntries(raw)
	if !ok {
		return nil
	}
	var out []string
	for _, e := range es {
		if e.Comment != "" {
			out = append(out, e.Key+": "+e.Text)
		}
	}
	return out
}

// FrontmatterLinks returns the [[wikilinks]] written in the frontmatter's
// top-level values, a scalar or the items of a list, each with the key it
// was written under (IMP-127 iteration 2): `related: "[[p/x]]"` is a link
// from the field related, as a property link is in Obsidian. Inline code
// is not a link, as in the body. Nested maps and tags are left out.
func FrontmatterLinks(raw string) []WikiLinkRef {
	var out []WikiLinkRef
	add := func(key, text string) {
		for _, l := range WikiLinks(stripCode(text)) {
			if l.Target != "" {
				l.Field = key
				out = append(out, l)
			}
		}
	}
	for _, e := range FrontmatterEntries(raw) {
		switch {
		case e.Key == "tags":
		case e.Kind == FMScalar:
			add(e.Key, e.Text)
		case e.Kind == FMList:
			for _, it := range e.Items {
				add(e.Key, it)
			}
		}
	}
	return out
}

// FrontmatterEntries reads raw frontmatter (the text between the --- lines):
// with a YAML parser when it parses, line by line otherwise. A key written
// twice counts once, the first time, as the index has always done.
func FrontmatterEntries(raw string) []FMEntry {
	if es, ok := yamlEntries(raw); ok {
		return es
	}
	return lineEntries(raw)
}

// yamlEntries reads raw with yaml.Node; ok is false when it is not valid
// YAML, or not a mapping, and the caller falls back to the line reader.
func yamlEntries(raw string) ([]FMEntry, bool) {
	if strings.TrimSpace(raw) == "" {
		return nil, true
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, false
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, false
	}
	m := doc.Content[0]
	var out []FMEntry
	seen := map[string]bool{}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], resolveAlias(m.Content[i+1])
		if k.Kind != yaml.ScalarNode || seen[k.Value] {
			continue
		}
		seen[k.Value] = true
		out = append(out, nodeEntry(k.Value, v))
	}
	return out, true
}

func resolveAlias(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

func nodeEntry(key string, v *yaml.Node) FMEntry {
	e := FMEntry{Key: key}
	switch v.Kind {
	case yaml.ScalarNode:
		if v.Value == "" && v.Tag == "!!null" {
			return e // `key:`: no value; `key: ""` is an empty string
		}
		e.Kind, e.Text = FMScalar, v.Value
		if v.Style == 0 {
			e.Comment = v.LineComment
		}
	case yaml.SequenceNode:
		// An unquoted [[wikilink]] is, in YAML, a list holding a list: read
		// it as the link its author wrote.
		if link, ok := wikilinkNode(v); ok {
			e.Kind, e.Text = FMScalar, link
			return e
		}
		e.Kind, e.Items = FMList, nodeItems(v)
	case yaml.MappingNode:
		e.Kind, e.Sub = FMMap, map[string]any{}
		for i := 0; i+1 < len(v.Content); i += 2 {
			sk, sv := v.Content[i], resolveAlias(v.Content[i+1])
			if sk.Kind != yaml.ScalarNode {
				continue
			}
			switch sv.Kind {
			case yaml.ScalarNode:
				e.Sub[sk.Value] = sv.Value
			case yaml.SequenceNode:
				e.Sub[sk.Value] = nodeItems(sv)
			}
		}
	}
	return e
}

// nodeItems lists a sequence's items as text: scalars, and unquoted
// [[wikilinks]] (in YAML a list holding a list of one scalar) as the link.
func nodeItems(seq *yaml.Node) []string {
	var out []string
	for _, it := range seq.Content {
		it = resolveAlias(it)
		switch it.Kind {
		case yaml.ScalarNode:
			if it.Value != "" {
				out = append(out, it.Value)
			}
		case yaml.SequenceNode:
			if link, ok := wikilinkNode(it); ok {
				out = append(out, link)
			}
		}
	}
	return out
}

// wikilinkNode reports a value written as an unquoted [[target]]: a flow
// list holding one flow list of one scalar.
func wikilinkNode(v *yaml.Node) (string, bool) {
	if v.Style&yaml.FlowStyle == 0 || len(v.Content) != 1 {
		return "", false
	}
	inner := resolveAlias(v.Content[0])
	if inner.Kind != yaml.SequenceNode || len(inner.Content) != 1 {
		return "", false
	}
	s := resolveAlias(inner.Content[0])
	if s.Kind != yaml.ScalarNode {
		return "", false
	}
	return "[[" + s.Value + "]]", true
}

// lineEntries is the line reader's FrontmatterEntries, as the index read a
// frontmatter before IMP-138: a key whose line holds nothing or starts with
// [ is a list (a nested map yields an empty one), any other a scalar.
func lineEntries(raw string) []FMEntry {
	scalars := lineParseFrontmatterFields(raw)
	var out []FMEntry
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		m := frontScalarRe.FindStringSubmatch(line)
		if m == nil || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") || seen[m[1]] {
			continue
		}
		key, rest := m[1], strings.TrimSpace(m[2])
		seen[key] = true
		e := FMEntry{Key: key}
		switch {
		case key == "tags":
			if items := lineFrontmatterTags(raw); len(items) > 0 {
				e.Kind, e.Items = FMList, items
			}
		case rest == "" || strings.HasPrefix(rest, "["):
			if items := lineFrontmatterList(raw, key); len(items) > 0 {
				e.Kind, e.Items = FMList, items
			} else if sub := lineExtractFrontmatterBlock(raw, key); sub != nil {
				e.Kind, e.Sub = FMMap, sub
			}
		default:
			if s, ok := scalars[key].(string); ok && s != "" {
				e.Kind, e.Text = FMScalar, s
			}
		}
		out = append(out, e)
	}
	return out
}

func entry(es []FMEntry, key string) (FMEntry, bool) {
	for _, e := range es {
		if e.Key == key {
			return e, true
		}
	}
	return FMEntry{}, false
}

// listOf reads e as a list, the way the line reader always did: a list's
// items, or a scalar split on commas (tags: a, b), each trimmed of quotes
// and a leading '#'.
func listOf(e FMEntry) []string {
	var raw []string
	switch e.Kind {
	case FMList:
		raw = e.Items
	case FMScalar:
		raw = strings.Split(e.Text, ",")
	}
	var out []string
	for _, it := range raw {
		if v := normalizeTag(it); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// ParseFrontmatterFields returns the frontmatter's top-level values: each
// scalar as its text, `tags` as a list, and any other list as a []string.
// Keys without a value, and nested maps, are left out. Returns an empty map
// for empty input.
func ParseFrontmatterFields(raw string) map[string]any {
	es, ok := yamlEntries(raw)
	if !ok {
		return lineParseFrontmatterFields(raw)
	}
	out := map[string]any{}
	for _, e := range es {
		switch {
		case e.Key == "tags":
			if tags := listOf(e); len(tags) > 0 {
				out["tags"] = tags
			}
		case e.Kind == FMScalar:
			out[e.Key] = e.Text
		case e.Kind == FMList && len(e.Items) > 0:
			out[e.Key] = e.Items
		}
	}
	return out
}

// FrontmatterList returns the values of a list-valued key: its items, or a
// scalar split on commas, each trimmed of quotes and a leading '#'; nil when
// the key is absent or has none. Used for tags, implements_imp and the like.
func FrontmatterList(fm, key string) []string {
	es, ok := yamlEntries(fm)
	if !ok {
		return lineFrontmatterList(fm, key)
	}
	if e, ok := entry(es, key); ok {
		return listOf(e)
	}
	return nil
}

// extractFrontmatterTags returns the frontmatter's tags (FrontmatterList of
// tags).
func extractFrontmatterTags(fm string) []string {
	es, ok := yamlEntries(fm)
	if !ok {
		return lineFrontmatterTags(fm)
	}
	if e, ok := entry(es, "tags"); ok {
		return listOf(e)
	}
	return nil
}

// ExtractFrontmatterBlock returns the sub-keys of a nested map under key: a
// scalar as a string, a list as a []string. Nil when the key is absent or not
// a map.
func ExtractFrontmatterBlock(raw, key string) map[string]any {
	es, ok := yamlEntries(raw)
	if !ok {
		return lineExtractFrontmatterBlock(raw, key)
	}
	e, ok := entry(es, key)
	if !ok || e.Kind != FMMap || len(e.Sub) == 0 {
		return nil
	}
	out := map[string]any{}
	for k, v := range e.Sub {
		if items, isList := v.([]string); isList {
			var norm []string
			for _, it := range items {
				if n := normalizeTag(it); n != "" {
					norm = append(norm, n)
				}
			}
			out[k] = norm
		} else {
			out[k] = v
		}
	}
	return out
}

// HasFrontmatterKey reports whether raw frontmatter carries key as a
// top-level entry, whatever its shape (block, inline scalar, or empty).
func HasFrontmatterKey(raw, key string) bool {
	es, ok := yamlEntries(raw)
	if !ok {
		return lineHasFrontmatterKey(raw, key)
	}
	_, found := entry(es, key)
	return found
}

// frontmatterTitle returns the title of raw frontmatter, or "" when there is
// none.
func frontmatterTitle(raw string) string {
	es, ok := yamlEntries(raw)
	if !ok {
		return lineFrontmatterTitle(raw)
	}
	if e, ok := entry(es, "title"); ok && e.Kind == FMScalar {
		return e.Text
	}
	return ""
}
