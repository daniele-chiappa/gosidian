// Package dbschema reads the schema of a database note and checks the notes
// it covers (IMP-127).
//
// A database note is a note with `type: database` whose frontmatter names
// the folder of its rows (`source`) and declares their fields:
//
//	type: database
//	source: proj/docs/improvements
//	fields:
//	  id: {type: text, required: true}
//	  status: {type: select, required: true, options: [open, done]}
//	  closed: {type: date}
//
// Each row is a note directly inside the source folder, with the values in
// its own frontmatter. The schema is read with a YAML parser; the index keeps
// its own frontmatter extraction, so nothing here changes what is indexed.
package dbschema

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Field is one declared field of a database.
type Field struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Options  []string `json:"options,omitempty"`
	Required bool     `json:"required,omitempty"`
}

// Schema is a parsed database note.
type Schema struct {
	Path   string  `json:"path"`   // the database note
	Source string  `json:"source"` // folder of the rows, vault-relative
	Fields []Field `json:"fields"` // in declaration order
}

// Types are the field types a schema may declare.
var Types = []string{"text", "number", "date", "checkbox", "select", "multi-select", "url", "relation", "list"}

// implicitFields may appear on any row without being declared: every
// gosidian note carries them (lint frontmatter-missing asks for both).
var implicitFields = []string{"title", "tags"}

// ErrNotDatabase reports a note that is not a database note.
var ErrNotDatabase = errors.New("not a database note")

// Parse reads the schema of the database note at path from its raw YAML
// frontmatter. A note without `type: database` yields ErrNotDatabase; a
// database note with a malformed schema yields a descriptive error.
func Parse(notePath, frontmatter string) (*Schema, error) {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(frontmatter), &root); err != nil {
		return nil, fmt.Errorf("frontmatter is not valid YAML: %w", err)
	}
	m := mappingOf(&root)
	if m == nil || scalar(m, "type") != "database" {
		return nil, ErrNotDatabase
	}
	s := &Schema{Path: notePath, Source: strings.Trim(scalar(m, "source"), "/")}
	if s.Source == "" {
		return nil, errors.New("database note has no `source` (the folder of its rows)")
	}
	fields := valueOf(m, "fields")
	if fields == nil || fields.Kind != yaml.MappingNode {
		return nil, errors.New("`fields` must be a map of field name to {type, options, required}")
	}
	for i := 0; i+1 < len(fields.Content); i += 2 {
		name, def := fields.Content[i].Value, fields.Content[i+1]
		f := Field{Name: name}
		if def.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("field %q: want {type: …}", name)
		}
		f.Type = scalar(def, "type")
		if !slices.Contains(Types, f.Type) {
			return nil, fmt.Errorf("field %q: type %q is not one of %s", name, f.Type, strings.Join(Types, ", "))
		}
		f.Required = scalar(def, "required") == "true"
		if opts := valueOf(def, "options"); opts != nil {
			if opts.Kind != yaml.SequenceNode {
				return nil, fmt.Errorf("field %q: options must be a list", name)
			}
			for _, o := range opts.Content {
				f.Options = append(f.Options, o.Value)
			}
		}
		if (f.Type == "select" || f.Type == "multi-select") && len(f.Options) == 0 {
			return nil, fmt.Errorf("field %q: a %s needs options", name, f.Type)
		}
		s.Fields = append(s.Fields, f)
	}
	return s, nil
}

// Covers reports whether the note at rel is a row of this database: a note
// directly inside its source folder.
func (s *Schema) Covers(rel string) bool {
	return path.Dir(rel) == s.Source
}

// Field returns the declared field with the given name.
func (s *Schema) Field(name string) (Field, bool) {
	for _, f := range s.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// FieldNames lists the declared fields, in declaration order.
func (s *Schema) FieldNames() []string {
	out := make([]string, len(s.Fields))
	for i, f := range s.Fields {
		out[i] = f.Name
	}
	return out
}

// Problem is one way a row does not match its schema.
type Problem struct {
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

var isoDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}([T ]\d{2}:\d{2}(:\d{2}(\.\d+)?)?(Z|[+-]\d{2}:?\d{2})?)?$`)

// Validate checks a row's raw YAML frontmatter against the schema. A field
// named `id` must also match the note's file name, the convention that keeps
// a row's links short and stable.
func (s *Schema) Validate(rel, frontmatter string) []Problem {
	var values map[string]any
	if err := yaml.Unmarshal([]byte(frontmatter), &values); err != nil {
		return []Problem{{Message: "frontmatter is not valid YAML: " + err.Error()}}
	}
	var out []Problem
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		f, declared := s.Field(k)
		if !declared {
			if !slices.Contains(implicitFields, k) {
				out = append(out, Problem{Field: k, Message: fmt.Sprintf(
					"field %q is not in the schema of %s (fields: %s)", k, s.Path, strings.Join(s.FieldNames(), ", "))})
			}
			continue
		}
		if msg := checkValue(f, values[k]); msg != "" {
			out = append(out, Problem{Field: k, Message: msg})
		}
	}
	for _, f := range s.Fields {
		if v, ok := values[f.Name]; f.Required && (!ok || v == nil || v == "") {
			out = append(out, Problem{Field: f.Name, Message: fmt.Sprintf("required field %q is missing", f.Name)})
		}
	}
	if _, ok := s.Field("id"); ok {
		base := strings.TrimSuffix(path.Base(rel), path.Ext(rel))
		if id, ok := values["id"]; ok && fmt.Sprint(id) != base {
			out = append(out, Problem{Field: "id", Message: fmt.Sprintf("id %q does not match the file name %q", fmt.Sprint(id), base)})
		}
	}
	return out
}

// checkValue returns why v is not a valid value of f, or "".
func checkValue(f Field, v any) string {
	if v == nil {
		return ""
	}
	switch f.Type {
	case "number":
		switch t := v.(type) {
		case int, int64, float64:
			return ""
		case string:
			if _, err := strconv.ParseFloat(t, 64); err == nil {
				return ""
			}
		}
		return fmt.Sprintf("%s: %v is not a number", f.Name, v)
	case "date":
		if _, ok := v.(time.Time); ok {
			return ""
		}
		if s, ok := v.(string); ok && isoDate.MatchString(s) {
			return ""
		}
		return fmt.Sprintf("%s: %v is not an ISO date (YYYY-MM-DD)", f.Name, v)
	case "checkbox":
		if _, ok := v.(bool); ok {
			return ""
		}
		return fmt.Sprintf("%s: %v is not true or false", f.Name, v)
	case "select":
		if s := fmt.Sprint(v); !slices.Contains(f.Options, s) {
			return fmt.Sprintf("%s: %q is not one of %s", f.Name, s, strings.Join(f.Options, ", "))
		}
	case "multi-select":
		list, ok := v.([]any)
		if !ok {
			return fmt.Sprintf("%s: want a list of %s", f.Name, strings.Join(f.Options, ", "))
		}
		for _, e := range list {
			if s := fmt.Sprint(e); !slices.Contains(f.Options, s) {
				return fmt.Sprintf("%s: %q is not one of %s", f.Name, s, strings.Join(f.Options, ", "))
			}
		}
	case "url":
		u, err := url.Parse(fmt.Sprint(v))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Sprintf("%s: %v is not an http(s) URL", f.Name, v)
		}
	case "relation":
		for _, e := range asList(v) {
			if s := fmt.Sprint(e); !strings.HasPrefix(s, "[[") || !strings.HasSuffix(s, "]]") {
				return fmt.Sprintf("%s: %q is not a [[wikilink]]", f.Name, s)
			}
		}
	case "list":
		if _, ok := v.([]any); !ok {
			return fmt.Sprintf("%s: want a list", f.Name)
		}
	}
	return ""
}

func asList(v any) []any {
	if l, ok := v.([]any); ok {
		return l
	}
	return []any{v}
}

// mappingOf returns the top-level mapping of a YAML document, or nil.
func mappingOf(doc *yaml.Node) *yaml.Node {
	if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return nil
	}
	return doc
}

func valueOf(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func scalar(m *yaml.Node, key string) string {
	if v := valueOf(m, key); v != nil && v.Kind == yaml.ScalarNode {
		return v.Value
	}
	return ""
}
