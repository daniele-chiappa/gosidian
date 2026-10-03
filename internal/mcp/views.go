package mcp

import (
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
