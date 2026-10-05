package mcp

import (
	"bytes"
	"fmt"
	"path"
	"sort"
	"time"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/dbschema"
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
	out, hash, _ := s.renderViewsWithin(tok, rel, content, 0)
	return out, hash
}

// renderViewsWithin is renderViews with the results held to about budget
// bytes (0: no limit), as the bootstrap serves them (IMP-136); cut reports
// a view cut to its share.
func (s *Server) renderViewsWithin(tok *auth.Token, rel string, content []byte, budget int) ([]byte, string, bool) {
	return views.RenderNoteWithin(content, true, s.viewContext(tok, rel, content), s.viewQuery(tok), budget)
}

// viewContext is the context of the views of the note at rel for the
// token: this is the note, schemas and links within the token's scope.
func (s *Server) viewContext(tok *auth.Token, rel string, content []byte) views.Context {
	return views.Context{
		This:    views.ThisFields(rel, parser.ParseFrontmatterFields(parser.FrontmatterRawForPath(rel, content))),
		Today:   time.Now(),
		Schema:  s.viewSchema(tok),
		Resolve: s.viewResolve(tok),
		Load:    s.viewLoad(tok),
	}
}

// viewLoad reads a note the token may read, for the embeds of a note
// (![[note#Heading]]); false for any other.
func (s *Server) viewLoad(tok *auth.Token) func(p string) ([]byte, bool) {
	filter := buildProjectsFilter(nil, tok.ProjectList())
	return func(p string) ([]byte, bool) {
		if !tok.AllowsPath(p) || !filter.matches(p) || s.pathInHiddenProject(p) {
			return nil, false
		}
		n, err := s.vault.Load(p)
		if err != nil {
			return nil, false
		}
		return n.Content, true
	}
}

// embedsHint is the hint of a read that returns embeds (![[note#Heading]])
// without the text they include. "" when content includes none for the
// token.
func (s *Server) embedsHint(tok *auth.Token, rel string, content []byte, how string) string {
	n := views.CountEmbeds(content, s.viewContext(tok, rel, content))
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d embed(s) here (![[note#Heading]]), not included: %s to see the text they include, its views computed for this note", n, how)
}

// viewResolve resolves a link target of a view to a note the token may
// read, "" otherwise.
func (s *Server) viewResolve(tok *auth.Token) func(target string) string {
	filter := buildProjectsFilter(nil, tok.ProjectList())
	return func(target string) string {
		p := s.index.Resolve(target)
		if p == "" || !tok.AllowsPath(p) || !filter.matches(p) || s.pathInHiddenProject(p) {
			return ""
		}
		return p
	}
}

// rowSchema returns the schema of the database whose row is the note at
// rel (content: the note as stored), when it declares row views and the
// token may read it; nil otherwise.
func (s *Server) rowSchema(tok *auth.Token, rel string, content []byte) *dbschema.Schema {
	schema := s.viewSchema(tok)(path.Dir(rel))
	if schema == nil || len(schema.RowViews) == 0 || !schema.IsRow(rel, parser.FrontmatterRawForPath(rel, content)) {
		return nil
	}
	return schema
}

// withRowViews appends the row views of the note at rel to content, as an
// agent reads them (IMP-139): computed, between markers, after the body.
// The bool reports whether there were any.
func (s *Server) withRowViews(tok *auth.Token, rel string, note, content []byte) ([]byte, bool) {
	schema := s.rowSchema(tok, rel, note)
	if schema == nil {
		return content, false
	}
	rs := views.ComputeRowViews(schema.RowViews, s.viewContext(tok, rel, note), s.viewQuery(tok))
	out := bytes.TrimRight(views.StripRowViews(content), "\n")
	return append(append(out, '\n'), views.RowViewsMarkdown(schema.Path, rs)...), true
}

// rowViewsHint is the hint of a read of a row without render_views: its
// database declares row views, which only render_views computes. "" when
// it declares none.
func (s *Server) rowViewsHint(tok *auth.Token, rel string, note []byte, how string) string {
	schema := s.rowSchema(tok, rel, note)
	if schema == nil {
		return ""
	}
	return fmt.Sprintf("%d row view(s) of %s not shown (what links to this row, and other views its database declares): %s to see them after the body",
		len(schema.RowViews), schema.Path, how)
}

// viewSchema resolves the schema of the database whose rows are the notes
// of a folder, when the token may read the database note: it orders the
// columns of a board as the select's options.
func (s *Server) viewSchema(tok *auth.Token) func(folder string) *dbschema.Schema {
	filter := buildProjectsFilter(nil, tok.ProjectList())
	return func(folder string) *dbschema.Schema {
		schema, err := dbschema.Covering(s.index, s.vault, folder+"/_")
		if err != nil || schema == nil || !tok.AllowsPath(schema.Path) || !filter.matches(schema.Path) || s.pathInHiddenProject(schema.Path) {
			return nil
		}
		return schema
	}
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
	var starts []int // of the view blocks and the inline values
	for _, b := range views.FindBlocks(body) {
		starts = append(starts, b.Start)
	}
	for _, v := range views.FindValues(body) {
		starts = append(starts, v.Start)
	}
	offs := parser.HeadingOffsets(body)
	embeds := views.EmbedOffsets(body)
	if len(starts)+len(embeds) == 0 || len(offs) != len(out) {
		return out
	}
	// The last heading before a block, or an embed, owns it.
	for _, st := range starts {
		if i := sort.SearchInts(offs, st+1) - 1; i >= 0 {
			out[i].Views++
		}
	}
	for _, st := range embeds {
		if i := sort.SearchInts(offs, st+1) - 1; i >= 0 {
			out[i].Embeds++
		}
	}
	return out
}

// viewsHint is the hint of a read that returns view blocks without their
// result (BUG-087): the text alone gives the spec and no rows. how says how
// to get them. "" when content has no view blocks.
func viewsHint(content []byte, how string) string {
	n, v := len(views.FindBlocks(content)), len(views.FindValues(content))
	switch {
	case n == 0 && v == 0:
		return ""
	case v == 0:
		return fmt.Sprintf("%d ```view block(s) here, not computed: %s to see their rows (as returned, the text is the file as stored, the form an edit needs)", n, how)
	case n == 0:
		return fmt.Sprintf("%d `=count(…)` value(s) here, not computed: %s to see the numbers (as returned, the text is the file as stored, the form an edit needs)", v, how)
	}
	return fmt.Sprintf("%d ```view block(s) and %d `=count(…)` value(s) here, not computed: %s to see their rows and numbers (as returned, the text is the file as stored, the form an edit needs)", n, v, how)
}

// withRenderedViews computes the views of a bootstrap file in place, within
// budget bytes (IMP-136), and sets its ViewsETag; the bool reports a view cut
// to its share. ETag stays the file's own, which is what if_match on a
// later write must carry; known_etags compares ViewsETag instead, so a file
// whose views changed is never reported unchanged.
func (s *Server) withRenderedViews(tok *auth.Token, f bootstrapFile, budget int) (bootstrapFile, bool) {
	if !f.Present || f.Content == "" {
		return f, false
	}
	out, hash, cut := s.renderViewsWithin(tok, f.Path, []byte(f.Content), budget)
	if hash == "" {
		return f, false
	}
	f.Content = string(out)
	f.ViewsETag = f.ETag + "+v" + hash
	return f, cut
}
