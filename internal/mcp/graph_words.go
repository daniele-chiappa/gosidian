package mcp

import (
	"encoding/json"
	"fmt"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/graphspec"
	"github.com/gosidian/gosidian/internal/linkgraph"
)

// graphWordsTop is how many of a graph's most linked notes memory_get names.
const graphWordsTop = 10

// graphWords is a ```graph block of a note as an agent reads it (IMP-224,
// F-32): its line, its source, and the graph in words, or why it does not
// read.
type graphWords struct {
	Line     int                 `json:"line"`
	Source   string              `json:"source,omitempty"`
	Text     string              `json:"text,omitempty"`
	Problems []graphspec.Problem `json:"problems,omitempty"`
}

// graphSummaries says the ```graph blocks of a note's body; nil without
// any. The links are counted among the notes tok reads; a plugin's graph
// is said by its view, without asking the plugin.
func (s *Server) graphSummaries(tok *auth.Token, body []byte) []graphWords {
	blocks := graphspec.Find(body)
	if len(blocks) == 0 {
		return nil
	}
	out := make([]graphWords, 0, len(blocks))
	for _, b := range blocks {
		spec, problems := graphspec.Parse(b.Src)
		w := graphWords{Line: b.Line, Source: spec.Source, Problems: problems}
		switch {
		case len(problems) > 0:
		case spec.Source != graphspec.SourceLinks:
			w.Text = pluginGraphWords(spec)
		case s.index == nil:
		default:
			o := linkgraph.Options{
				Project: spec.Project, Tag: spec.Tag, Focus: spec.Focus, Depth: spec.Depth,
				MinDegree: spec.MinDegree, Limit: spec.Limit,
				Visible: func(p string) bool { return tok.AllowsPath(p) && !s.pathInHiddenProject(p) },
			}
			if g, err := linkgraph.Build(s.index, o); err != nil {
				w.Text = "The links could not be read: " + err.Error()
			} else {
				w.Text = linkgraph.Text(o, g, graphWordsTop)
			}
		}
		out = append(out, w)
	}
	return out
}

// pluginGraphWords says a plugin's graph by its view: memory_get does not
// ask the plugin, which draws it in the web UI's Graph window.
func pluginGraphWords(spec graphspec.Spec) string {
	plugin, graph, _ := spec.Plugin()
	text := fmt.Sprintf("The graph %s of the plugin %s", graph, plugin)
	if spec.Project != "" {
		text += " for project " + spec.Project
	}
	if len(spec.With) > 0 {
		if b, err := json.Marshal(spec.With); err == nil {
			text += ", with " + string(b)
		}
	}
	return text + ": the web UI draws it; the plugin is not asked here."
}
