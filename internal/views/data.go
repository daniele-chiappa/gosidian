package views

import (
	"time"

	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/parser"
)

// Data is a computed view in the form the web UI's editors use (IMP-127
// phase 5): typed columns, the notes with their field values, and for a
// board its columns in order.
type Data struct {
	As      string   `json:"as,omitempty"`
	Columns []Column `json:"columns,omitempty"`
	// Group describes the group_by field of a board, and Groups the values
	// of its columns in order ("" for the notes without one).
	Group  *Column  `json:"group,omitempty"`
	Groups []string `json:"groups,omitempty"`
	Rows   []Row    `json:"rows,omitempty"`
	Total  int      `json:"total"`
	// Database and Source name the database the view lists, when it lists
	// the rows of one.
	Database string `json:"database,omitempty"`
	Source   string `json:"source,omitempty"`
	// Error is why the view could not be computed; the rest is empty.
	Error string `json:"error,omitempty"`
}

// Column is a column of a view. Type, Options and Required come from the
// schema of the database the view lists; Type is "" for title, path and
// modified, and for a field the schema does not declare.
type Column struct {
	Name     string   `json:"name"`
	Type     string   `json:"type,omitempty"`
	Options  []string `json:"options,omitempty"`
	Required bool     `json:"required,omitempty"`
}

// Row is a note a view lists. Fields holds the fields the view reads, as
// the index reads them: a string, or a list of strings for a list field.
type Row struct {
	Path     string         `json:"path"`
	Title    string         `json:"title"`
	Modified string         `json:"modified"`
	Fields   map[string]any `json:"fields"`
	// Links holds the wikilinks of each field's values, in order: a
	// relation, say. The web UI shows them as links, as the HTML does.
	Links    map[string][]Link `json:"links,omitempty"`
	Writable bool              `json:"writable"`
}

// Link is a wikilink in a field value. Path is the note it resolves to for
// the reader, "" when it names no note the reader may see.
type Link struct {
	Text string `json:"text"`
	Path string `json:"path,omitempty"`
}

// Data returns the view in the form the web UI's editors use; c.CanWrite
// marks the rows the reader may edit.
func (r *Result) Data(c Context) Data {
	d := Data{As: r.Spec.As, Total: r.Total}
	if r.Schema != nil {
		d.Database, d.Source = r.Schema.Path, r.Schema.Source
	}
	for _, name := range r.Spec.Columns {
		d.Columns = append(d.Columns, r.column(name))
	}
	if r.Spec.As == "board" {
		g := r.column(r.Spec.GroupBy)
		d.Group = &g
		groups, _ := r.Groups()
		for _, gr := range groups {
			d.Groups = append(d.Groups, gr.Value)
		}
	}
	d.Rows = make([]Row, 0, len(r.Hits))
	for _, h := range r.Hits {
		fields := h.FieldValues()
		if fields == nil {
			fields = map[string]any{}
		}
		d.Rows = append(d.Rows, Row{
			Path:     h.Path,
			Title:    rowTitle(h),
			Modified: time.Unix(h.ModTime, 0).UTC().Format("2006-01-02"),
			Fields:   fields,
			Links:    fieldLinks(h, c),
			Writable: c.CanWrite != nil && c.CanWrite(h.Path),
		})
	}
	return d
}

// fieldLinks lists the wikilinks of each field of h, resolved with
// c.Resolve; nil when no value holds one.
func fieldLinks(h index.QueryHit, c Context) map[string][]Link {
	var out map[string][]Link
	for name, values := range h.Fields {
		for _, v := range values {
			for _, l := range parser.WikiLinks(v) {
				text := l.Alias
				if text == "" {
					text = l.Target
				}
				link := Link{Text: text}
				if c.Resolve != nil {
					link.Path = c.Resolve(l.Target)
				}
				if out == nil {
					out = map[string][]Link{}
				}
				out[name] = append(out[name], link)
			}
		}
	}
	return out
}

func (r *Result) column(name string) Column {
	col := Column{Name: name}
	if r.Schema == nil || builtin[name] && name != "title" {
		return col
	}
	if f, ok := r.Schema.Field(name); ok {
		col.Type, col.Options, col.Required = f.Type, f.Options, f.Required
	}
	return col
}

func rowTitle(h index.QueryHit) string {
	if h.Title != "" {
		return h.Title
	}
	return h.Path
}
