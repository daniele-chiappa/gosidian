package mcp

import (
	"fmt"
	"sort"
	"time"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/views"
)

// viewQuery runs the query of a view with the token's scope, the same one
// memory_query applies: a view never shows a row its reader could not query
// directly (IMP-127).
func (s *Server) viewQuery(tok *auth.Token) views.QueryFunc {
	return func(o index.QueryOptions) ([]index.QueryHit, int, error) {
		filter := buildProjectsFilter(nil, tok.ProjectList())
		o.Exclude = append(o.Exclude, s.hiddenProjects()...)
		if filter.active {
			o.Projects = append([]string{}, filter.allowed...)
		}
		hits, total, err := s.index.Query(o)
		if err != nil {
			return nil, 0, err
		}
		out := hits[:0]
		for _, h := range hits {
			if !tok.AllowsPath(h.Path) || !filter.matches(h.Path) || s.pathInHiddenProject(h.Path) {
				total--
				continue
			}
			out = append(out, h)
		}
		return out, total, nil
	}
}

// renderViews expands the view blocks of a note for an agent: each block
// stays and its computed result follows it, so the agent sees both what a
// section shows and what produces it. The second value hashes the results
// ("" when the note has no views).
func (s *Server) renderViews(tok *auth.Token, rel string, content []byte) ([]byte, string) {
	c := views.Context{
		This:  views.ThisFields(rel, parser.ParseFrontmatterFields(parser.FrontmatterRawForPath(rel, content))),
		Today: time.Now(),
	}
	return views.RenderNote(content, true, c, s.viewQuery(tok))
}

// outline lists the headings of body for an outline response, each with the
// view blocks of its own lines, so an agent that reads a note by sections
// knows which ones need render_views (BUG-087).
func outline(body []byte) []outlineHeading {
	hs := parser.ExtractHeadings(body)
	out := make([]outlineHeading, len(hs))
	for i, h := range hs {
		out[i] = outlineHeading{Level: h.Level, Text: h.Text, ID: h.ID}
	}
	blocks := views.FindBlocks(body)
	offs := parser.HeadingOffsets(body)
	if len(blocks) == 0 || len(offs) != len(out) {
		return out
	}
	for _, b := range blocks {
		// The last heading before the block owns it.
		if i := sort.SearchInts(offs, b.Start+1) - 1; i >= 0 {
			out[i].Views++
		}
	}
	return out
}

// viewsHint is the hint of a read that returns view blocks without their
// result (BUG-087): the text alone gives the spec and no rows. how says how
// to get them. "" when content has no view blocks.
func viewsHint(content []byte, how string) string {
	n := len(views.FindBlocks(content))
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d ```view block(s) here, not computed: %s to see their rows (as returned, the text is the file as stored, the form an edit needs)", n, how)
}

// withRenderedViews computes the views of a bootstrap file in place and sets
// its ViewsETag. ETag stays the file's own, which is what if_match on a
// later write must carry; known_etags compares ViewsETag instead, so a file
// whose views changed is never reported unchanged.
func (s *Server) withRenderedViews(tok *auth.Token, f bootstrapFile) bootstrapFile {
	if !f.Present || f.Content == "" {
		return f
	}
	out, hash := s.renderViews(tok, f.Path, []byte(f.Content))
	if hash == "" {
		return f
	}
	f.Content = string(out)
	f.ViewsETag = f.ETag + "+v" + hash
	return f
}
