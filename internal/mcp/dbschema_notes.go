package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gosidian/gosidian/internal/dbschema"
	"github.com/gosidian/gosidian/internal/frontmatter"
	"github.com/gosidian/gosidian/internal/index"
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
	if strings.TrimSpace(fm) != "" {
		if err := frontmatter.YAMLError(fm); err != nil {
			// IMP-138: the index copes, line by line, but the schema reader
			// and the readers to come do not; usually an unquoted ": " or
			// [[wikilink]] in a value.
			addCallNote(ctx, fmt.Sprintf(
				"%s: the frontmatter is not valid YAML (%s). Quote a value that holds \": \", a [[wikilink]] or a {{placeholder}}, e.g. title: \"Plan: one\", related: \"[[p/note]]\".",
				rel, err))
			// The schema checks below read YAML too: they would only repeat
			// it, and call any such note "a database note".
			return
		}
		if cut := parser.CutByComment(fm); len(cut) > 0 {
			addCallNote(ctx, fmt.Sprintf(
				"%s: YAML reads a ' #' in a value as a comment and cuts it (%s). Quote the value when the # belongs to it, e.g. title: \"Alert #3\".",
				rel, strings.Join(cut, "; ")))
		}
	}
	if _, err := dbschema.Parse(rel, fm); err != nil && !errors.Is(err, dbschema.ErrNotDatabase) {
		addCallNote(ctx, fmt.Sprintf("%s is a database note but its schema does not parse (%v): its rows go unchecked until it does.", rel, err))
		return
	}
	schema, err := dbschema.Covering(s.index, s.vault, rel)
	if err != nil || schema == nil || !schema.IsRow(rel, fm) {
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
	Path   string `json:"path"`
	Source string `json:"source"`
	// Template is the note a new row starts from, when the database names
	// one: the same model the web UI's new rows use.
	Template string           `json:"template,omitempty"`
	Rows     int              `json:"rows"`
	Fields   []dbschema.Field `json:"fields"`
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
		if len(sc.Rows) > 0 {
			// Only the notes with the values of rows: ask the index.
			if _, total, err := s.index.Query(index.QueryOptions{Folders: []string{sc.Source}, Where: rowConds(sc), Limit: 1}); err == nil {
				rows = total
			}
		} else if notes, err := s.index.NotesByPrefix(sc.Source); err == nil {
			for _, n := range notes {
				if sc.Covers(n.Path) {
					rows++
				}
			}
		}
		out = append(out, bootstrapDatabase{Path: sc.Path, Source: sc.Source, Template: sc.Template, Rows: rows, Fields: sc.Fields})
	}
	return out
}

// rowConds are the rows of a database (dbschema.Schema.Rows) as query
// conditions.
func rowConds(sc *dbschema.Schema) []index.FieldCond {
	var out []index.FieldCond
	for _, kv := range sc.RowConds() {
		out = append(out, index.FieldCond{Field: kv[0], Op: index.OpEq, Values: []string{kv[1]}})
	}
	return out
}
