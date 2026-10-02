package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gosidian/gosidian/internal/dbschema"
	"github.com/gosidian/gosidian/internal/parser"
)

// noteSchemaProblems leaves a note on the current tool call when the note
// just written breaks a database schema (IMP-127): a row with an undeclared
// field, a missing required one or a value of the wrong type, or a database
// note whose own schema no longer parses. It warns and does not refuse: the
// write has already happened, and an agent must not be stuck on a schema it
// may not be allowed to change. The lint rule database-field-invalid keeps
// listing the row until it is fixed.
func (s *Server) noteSchemaProblems(ctx context.Context, rel string, content []byte) {
	fm := parser.FrontmatterRawForPath(rel, content)
	if _, err := dbschema.Parse(rel, fm); err != nil && !errors.Is(err, dbschema.ErrNotDatabase) {
		addCallNote(ctx, fmt.Sprintf("%s is a database note but its schema does not parse (%v): its rows go unchecked until it does.", rel, err))
		return
	}
	schema, err := dbschema.Covering(s.index, s.vault, rel)
	if err != nil || schema == nil {
		return
	}
	probs := schema.Validate(rel, fm)
	if len(probs) == 0 {
		return
	}
	msgs := make([]string, len(probs))
	for i, p := range probs {
		msgs[i] = p.Message
	}
	addCallNote(ctx, fmt.Sprintf(
		"%s is a row of the database %s and breaks its schema: %s. Fix the frontmatter with memory_edit, using only the declared fields (%s); commit hashes and details go in the body.",
		rel, schema.Path, strings.Join(msgs, "; "), strings.Join(schema.FieldNames(), ", ")))
}

// bootstrapDatabase is one entry of memory_bootstrap's `databases` list.
type bootstrapDatabase struct {
	Path   string           `json:"path"`
	Source string           `json:"source"`
	Rows   int              `json:"rows"`
	Fields []dbschema.Field `json:"fields"`
}

// bootstrapDatabases lists the database notes of a project with their
// schema and row count; nil when there are none or the lookup fails.
func (s *Server) bootstrapDatabases(project string) []bootstrapDatabase {
	schemas, _, err := dbschema.ForProject(s.index, s.vault, project)
	if err != nil || len(schemas) == 0 {
		return nil
	}
	out := make([]bootstrapDatabase, 0, len(schemas))
	for _, sc := range schemas {
		rows := 0
		if notes, err := s.index.NotesByPrefix(sc.Source); err == nil {
			for _, n := range notes {
				if sc.Covers(n.Path) {
					rows++
				}
			}
		}
		out = append(out, bootstrapDatabase{Path: sc.Path, Source: sc.Source, Rows: rows, Fields: sc.Fields})
	}
	return out
}
