package parser

import (
	"math"
	"regexp"

	"go.yaml.in/yaml/v3"
)

// TypedFrontmatter returns a frontmatter's top-level values with the types
// YAML gives them (IMP-198), for the pipelines, which compute on them: a
// bool, an int, a float, a string, a list as []any and a map as
// map[string]any, nested as written. ParseFrontmatterFields, which the
// index and the tools read, keeps every value as its text and leaves the
// maps out; a pipeline that wrote `revisioni: {V1: 1792237963}` must read
// it back as it wrote it.
//
// It reads with yaml.Node, as FrontmatterEntries does, and keeps what the
// vault means by a few values:
//   - title is its text (`title: 2026` is "2026"), tags the note's tags
//     (FrontmatterList: a list, `#` and quotes trimmed);
//   - an unquoted [[wikilink]], which YAML reads as a list holding a list,
//     is the link as written ("[[target]]"), at any depth;
//   - a date stays its text (2026-10-17), and so does a number written with
//     a leading zero (007, 00184: a code, which YAML would read as octal or
//     as a float), and .inf or .nan, which JSON cannot carry;
//   - a key without a value (`key:`) is left out at the top level, a null
//     inside a list or a map is nil.
//
// A frontmatter that is not valid YAML falls back to ParseFrontmatterFields'
// line reader, its values text. Empty input gives an empty map.
func TypedFrontmatter(raw string) map[string]any {
	m, ok := yamlMapping(raw)
	if !ok {
		return lineParseFrontmatterFields(raw)
	}
	out := map[string]any{}
	if m == nil {
		return out
	}
	seen := map[string]bool{}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], resolveAlias(m.Content[i+1])
		if k.Kind != yaml.ScalarNode || seen[k.Value] || v == nil {
			continue
		}
		seen[k.Value] = true
		switch {
		case k.Value == "tags":
			if tags := listOf(nodeEntry(k.Value, v)); len(tags) > 0 {
				items := make([]any, len(tags))
				for j, t := range tags {
					items[j] = t
				}
				out["tags"] = items
			}
		case k.Value == "title" && v.Kind == yaml.ScalarNode && v.ShortTag() != "!!null":
			out["title"] = v.Value
		default:
			if val, ok := typedValue(v); ok {
				out[k.Value] = val
			}
		}
	}
	return out
}

// typedValue is a YAML node as a Go value of its type; ok is false for a
// null.
func typedValue(n *yaml.Node) (any, bool) {
	n = resolveAlias(n)
	if n == nil {
		return nil, false
	}
	switch n.Kind {
	case yaml.ScalarNode:
		return typedScalar(n)
	case yaml.SequenceNode:
		if link, ok := wikilinkNode(n); ok {
			return link, true
		}
		out := make([]any, 0, len(n.Content))
		for _, it := range n.Content {
			v, _ := typedValue(it)
			out = append(out, v)
		}
		return out, true
	case yaml.MappingNode:
		out := make(map[string]any, len(n.Content)/2)
		seen := map[string]bool{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := resolveAlias(n.Content[i])
			if k == nil || k.Kind != yaml.ScalarNode || seen[k.Value] {
				continue
			}
			seen[k.Value] = true
			out[k.Value], _ = typedValue(n.Content[i+1])
		}
		return out, true
	}
	return nil, false
}

// leadingZeroRe matches a number written with a leading zero: 007, -0089.
var leadingZeroRe = regexp.MustCompile(`^[-+]?0[0-9_]`)

// typedScalar is a scalar as YAML types it: a bool, an int, a float, or its
// text (a string, a date, anything with another tag).
func typedScalar(n *yaml.Node) (any, bool) {
	switch n.ShortTag() {
	case "!!null":
		return nil, false
	case "!!bool", "!!int", "!!float":
		if leadingZeroRe.MatchString(n.Value) {
			return n.Value, true
		}
		var v any
		if err := n.Decode(&v); err != nil {
			return n.Value, true
		}
		if f, ok := v.(float64); ok && (math.IsInf(f, 0) || math.IsNaN(f)) {
			return n.Value, true
		}
		return v, true
	}
	return n.Value, true
}
