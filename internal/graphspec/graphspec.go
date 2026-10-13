// Package graphspec reads the ```graph blocks (IMP-183): a saved view of
// the graph, with its source (the vault's wikilinks, or a graph a plugin
// offers), its filters, its colour and its mode. The web UI draws the block
// in the note and opens it in the Graph window; the server checks it when
// a note is written and in the lint (graph-invalid), as it does diagrams.
package graphspec

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/gosidian/gosidian/internal/parser"
)

// Lang is the fence of the block; NoteType the type of a note that is a
// saved view.
const (
	Lang     = "graph"
	NoteType = "graph"
)

// SourceLinks is the vault's graph of wikilinks, the default source.
const SourceLinks = "links"

// Spec is a saved view of the graph.
type Spec struct {
	// Source is links or <plugin>/<graph>.
	Source string `json:"source"`
	// With are the arguments of a plugin's graph.
	With map[string]any `json:"with,omitempty"`
	// Project, Tag, Focus, Depth and MinDegree filter the links, as the
	// Graph window does; Project is also the project a plugin's graph is
	// asked for (default: the note's).
	Project   string `json:"project,omitempty"`
	Tag       string `json:"tag,omitempty"`
	Focus     string `json:"focus,omitempty"`
	Depth     int    `json:"depth,omitempty"`
	MinDegree int    `json:"min_degree,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	// Color names what groups the nodes: a field of the plugin's nodes, or
	// folder and project for the links.
	Color string `json:"color,omitempty"`
	// Mode is 2d (default) or 3d.
	Mode string `json:"mode,omitempty"`
}

// Plugin splits a plugin's source into the plugin and its graph.
func (s Spec) Plugin() (plugin, graph string, ok bool) {
	p, g, found := strings.Cut(s.Source, "/")
	return p, g, found && pluginName.MatchString(p) && graphName.MatchString(g)
}

// Problem is what keeps a block from reading as a view; Line counts from
// the block's first line.
type Problem struct {
	Line int    `json:"line,omitempty"`
	Msg  string `json:"message"`
}

func (p Problem) String() string {
	if p.Line > 0 {
		return "line " + strconv.Itoa(p.Line) + ": " + p.Msg
	}
	return p.Msg
}

// Block is a ```graph block of a note, at its line.
type Block struct {
	Line int
	Src  string
}

// Find returns the graph blocks of a note's body.
func Find(body []byte) []Block {
	fenced := parser.FindFenced(body, Lang)
	out := make([]Block, 0, len(fenced))
	for _, b := range fenced {
		out = append(out, Block{Line: strings.Count(string(body[:b.Start]), "\n") + 1, Src: b.Spec})
	}
	return out
}

var (
	pluginName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	graphName  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
)

// The fields of the links only, and the limits of the numbers.
var linksOnly = map[string]bool{"tag": true, "focus": true, "depth": true, "min_degree": true}

const (
	maxDepth = 6
	maxLimit = 5000
)

// Parse reads a block. A block that does not read still gives what it
// could, with its problems.
func Parse(src string) (Spec, []Problem) {
	var s Spec
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(src), &root); err != nil {
		return s, []Problem{{Msg: "the YAML does not parse: " + err.Error()}}
	}
	if len(root.Content) == 0 {
		return Spec{Source: SourceLinks}, nil
	}
	m := root.Content[0]
	if m.Kind != yaml.MappingNode {
		return s, []Problem{{Line: m.Line, Msg: "a graph is a map of fields: source, with, project, color, mode…"}}
	}
	var problems []Problem
	bad := func(n *yaml.Node, format string, args ...any) {
		problems = append(problems, Problem{Line: n.Line, Msg: fmt.Sprintf(format, args...)})
	}
	seen := map[string]*yaml.Node{}
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i], m.Content[i+1]
		seen[k.Value] = k
		switch k.Value {
		case "source", "project", "tag", "focus", "color", "mode":
			if v.Kind != yaml.ScalarNode {
				bad(v, "%s is a string", k.Value)
				continue
			}
			val := strings.TrimSpace(v.Value)
			switch k.Value {
			case "source":
				s.Source = val
			case "project":
				s.Project = val
			case "tag":
				s.Tag = val
			case "focus":
				s.Focus = val
			case "color":
				s.Color = val
			case "mode":
				s.Mode = strings.ToLower(val)
			}
		case "depth", "min_degree", "limit":
			n, err := strconv.Atoi(v.Value)
			if v.Kind != yaml.ScalarNode || err != nil || n < 0 {
				bad(v, "%s is a whole number, 0 or more", k.Value)
				continue
			}
			switch k.Value {
			case "depth":
				if n > maxDepth {
					bad(v, "depth is at most %d", maxDepth)
				}
				s.Depth = n
			case "min_degree":
				s.MinDegree = n
			case "limit":
				if n > maxLimit {
					bad(v, "limit is at most %d", maxLimit)
				}
				s.Limit = n
			}
		case "with":
			var w map[string]any
			if v.Kind != yaml.MappingNode || v.Decode(&w) != nil {
				bad(v, "with is a map of the source's arguments")
				continue
			}
			s.With = w
		default:
			bad(k, "%q is no field of a graph (source, with, project, tag, focus, depth, min_degree, limit, color, mode)", k.Value)
		}
	}
	if s.Source == "" {
		s.Source = SourceLinks
	}
	if s.Source != SourceLinks {
		if _, _, ok := s.Plugin(); !ok {
			problems = append(problems, Problem{Line: lineOf(seen["source"]), Msg: fmt.Sprintf("source %q: links, or <plugin>/<graph> (lower-case names)", s.Source)})
		}
		for f := range linksOnly {
			if n, ok := seen[f]; ok {
				bad(n, "%s filters the links: a plugin's graph takes its arguments in with", f)
			}
		}
	} else if n, ok := seen["with"]; ok {
		bad(n, "with is for a plugin's graph: the links take project, tag, focus, depth, min_degree")
	}
	switch s.Mode {
	case "", "2d", "3d":
	default:
		problems = append(problems, Problem{Line: lineOf(seen["mode"]), Msg: fmt.Sprintf("mode %q: 2d or 3d", s.Mode)})
	}
	return s, problems
}

func lineOf(n *yaml.Node) int {
	if n == nil {
		return 0
	}
	return n.Line
}
