// Package views runs the ```view blocks of a note (IMP-127). A view is a
// small YAML spec that selects notes by folder and frontmatter:
//
//	```view
//	from: proj/docs/improvements
//	where:
//	  - status in [open, in-progress]
//	  - {field: priority, op: eq, value: high}
//	sort: priority desc
//	columns: [id, title, priority]
//	```
//
// and renders them as a markdown table or list. A view is computed when the
// note is read; the file keeps only the spec, so a note whose data changes
// never needs rewriting. The caller runs the query with its own scope, so a
// reader only ever sees rows it may read.
package views

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/gosidian/gosidian/internal/index"
)

// Spec is a parsed view.
type Spec struct {
	From    []string
	Where   []index.FieldCond
	Sort    string
	Desc    bool
	Columns []string
	Limit   int
	As      string // "table" or "list"
}

// Context resolves the relative values a spec may use: this.<field>, the
// fields of the note holding the view, and today±Nd.
type Context struct {
	This  map[string][]string
	Today time.Time
}

// rawSpec is the YAML shape; where entries are a "field op value" string or
// a {field, op, value} map like memory_query's.
type rawSpec struct {
	From    any      `yaml:"from"`
	Where   []any    `yaml:"where"`
	Sort    string   `yaml:"sort"`
	Columns []string `yaml:"columns"`
	Limit   int      `yaml:"limit"`
	As      string   `yaml:"as"`
}

// Parse reads a view spec.
func Parse(src string, c Context) (*Spec, error) {
	var r rawSpec
	if err := yaml.Unmarshal([]byte(src), &r); err != nil {
		return nil, fmt.Errorf("spec is not valid YAML: %w", err)
	}
	s := &Spec{Columns: r.Columns, As: strings.TrimSpace(r.As)}
	switch f := r.From.(type) {
	case string:
		s.From = []string{f}
	case []any:
		for _, e := range f {
			s.From = append(s.From, fmt.Sprint(e))
		}
	}
	for i := range s.From {
		s.From[i] = strings.Trim(strings.TrimSpace(s.From[i]), "/")
	}
	if len(s.From) == 0 || s.From[0] == "" {
		return nil, errors.New("`from` is required: the folder (or list of folders) whose notes the view lists")
	}
	for _, w := range r.Where {
		cond, err := parseCond(w, c)
		if err != nil {
			return nil, err
		}
		s.Where = append(s.Where, cond)
	}
	if len(s.Where) > index.MaxQueryConds {
		return nil, fmt.Errorf("at most %d conditions", index.MaxQueryConds)
	}
	sortField, order, _ := strings.Cut(strings.TrimSpace(r.Sort), " ")
	s.Sort = sortField
	desc, err := index.SortDesc(sortField, strings.TrimSpace(order))
	if err != nil {
		return nil, err
	}
	s.Desc = desc
	s.Limit = index.ClampQueryLimit(r.Limit)
	if r.Limit <= 0 {
		s.Limit = index.QueryDefaultLimit
	}
	switch s.As {
	case "":
		s.As = "table"
	case "table", "list":
	default:
		return nil, fmt.Errorf("as: %q is not table or list", s.As)
	}
	if len(s.Columns) == 0 {
		s.Columns = append([]string{"title"}, withoutBuiltins(index.DefaultQueryFields(s.Where, s.Sort))...)
	}
	return s, nil
}

var condRe = regexp.MustCompile(`^\s*([A-Za-z_][\w.-]*)\s+(==|=|!=|<=|>=|<|>|in|contains|!exists|exists)(?:\s+|$)(.*?)\s*$`)

var opNames = map[string]string{"=": index.OpEq, "==": index.OpEq, "!=": "ne", "<": "lt", "<=": "lte",
	">": "gt", ">=": "gte", "in": "in", "contains": index.OpContains, "exists": "exists", "!exists": "exists"}

func parseCond(w any, c Context) (index.FieldCond, error) {
	switch t := w.(type) {
	case string:
		m := condRe.FindStringSubmatch(t)
		if m == nil {
			return index.FieldCond{}, fmt.Errorf("condition %q: want \"field op value\" (op: = != < <= > >= in contains exists !exists)", t)
		}
		cond := index.FieldCond{Field: m[1], Op: opNames[m[2]]}
		switch m[2] {
		case "exists":
			cond.Values = []string{"true"}
			return cond, nil
		case "!exists":
			cond.Values = []string{"false"}
			return cond, nil
		}
		vals, err := resolve(splitValue(m[3]), c)
		if err != nil {
			return index.FieldCond{}, fmt.Errorf("condition %q: %w", t, err)
		}
		cond.Values = vals
		return fixOp(cond), nil
	case map[string]any:
		field, _ := t["field"].(string)
		op, _ := t["op"].(string)
		if op == "" {
			op = index.OpEq
		}
		raw, err := index.CondValues(t["value"])
		if err != nil {
			return index.FieldCond{}, err
		}
		vals, err := resolve(raw, c)
		if err != nil {
			return index.FieldCond{}, err
		}
		return fixOp(index.FieldCond{Field: field, Op: op, Values: vals}), nil
	}
	return index.FieldCond{}, fmt.Errorf("condition %v: want a string or a {field, op, value} map", w)
}

// fixOp turns an equality on a value that resolved to several (this.tags)
// into a membership test, the only reading that makes sense.
func fixOp(c index.FieldCond) index.FieldCond {
	if c.Op == index.OpEq && len(c.Values) > 1 {
		c.Op = "in"
	}
	return c
}

func splitValue(v string) []string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
		var out []string
		for _, p := range strings.Split(v[1:len(v)-1], ",") {
			if p = unquote(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	return []string{unquote(v)}
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}

var todayRe = regexp.MustCompile(`^today(?:([+-])(\d+)d)?$`)

// resolve expands this.<field> and today±Nd.
func resolve(vals []string, c Context) ([]string, error) {
	var out []string
	for _, v := range vals {
		if f, ok := strings.CutPrefix(v, "this."); ok {
			got := c.This[f]
			if len(got) == 0 {
				return nil, fmt.Errorf("this.%s: this note has no %q field", f, f)
			}
			out = append(out, got...)
			continue
		}
		if m := todayRe.FindStringSubmatch(v); m != nil {
			d := c.Today
			if d.IsZero() {
				d = time.Now()
			}
			if m[2] != "" {
				n, _ := strconv.Atoi(m[2])
				if m[1] == "-" {
					n = -n
				}
				d = d.AddDate(0, 0, n)
			}
			out = append(out, d.Format("2006-01-02"))
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

// builtin columns come from the note itself, not its frontmatter.
var builtin = map[string]bool{"title": true, "path": true, "modified": true}

func withoutBuiltins(fields []string) []string {
	var out []string
	for _, f := range fields {
		if !builtin[f] {
			out = append(out, f)
		}
	}
	return out
}

// QueryFunc runs a query with the caller's scope and returns only the hits
// the reader may see, with total lowered for those it dropped.
type QueryFunc func(index.QueryOptions) ([]index.QueryHit, int, error)

// Result is a computed view.
type Result struct {
	Spec  *Spec
	Hits  []index.QueryHit
	Total int
}

// Run computes a view.
func Run(s *Spec, q QueryFunc) (*Result, error) {
	hits, total, err := q(index.QueryOptions{
		Folders: s.From, Where: s.Where, Sort: s.Sort, Desc: s.Desc, Limit: s.Limit,
		Fields: withoutBuiltins(s.Columns),
	})
	if err != nil {
		return nil, err
	}
	return &Result{Spec: s, Hits: hits, Total: total}, nil
}

// Markdown renders the result as a table or a list. Note links are
// [[path\|title]], which the renderer turns into links before it reads the
// table, so the same text serves agents and the web UI.
func (r *Result) Markdown() string {
	var b strings.Builder
	if len(r.Hits) == 0 {
		return "_No matching notes._\n"
	}
	cols := r.Spec.Columns
	if r.Spec.As == "list" {
		for _, h := range r.Hits {
			b.WriteString("- " + link(h))
			for _, c := range cols {
				if c == "title" {
					continue
				}
				if v := cell(h, c); v != "" {
					b.WriteString(" · " + c + ": " + v)
				}
			}
			b.WriteString("\n")
		}
	} else {
		b.WriteString("| " + strings.Join(cols, " | ") + " |\n")
		b.WriteString("|" + strings.Repeat("---|", len(cols)) + "\n")
		for _, h := range r.Hits {
			row := make([]string, len(cols))
			for i, c := range cols {
				if c == "title" {
					row[i] = link(h)
				} else {
					row[i] = cell(h, c)
				}
			}
			b.WriteString("| " + strings.Join(row, " | ") + " |\n")
		}
	}
	if r.Total > len(r.Hits) {
		fmt.Fprintf(&b, "\n_Showing %d of %d notes._\n", len(r.Hits), r.Total)
	}
	return b.String()
}

func link(h index.QueryHit) string {
	target := strings.TrimSuffix(h.Path, path.Ext(h.Path))
	title := h.Title
	if title == "" {
		title = path.Base(target)
	}
	// A backtick in the alias opens an inline code span that swallows the
	// wikilink, which then shows as raw text: titles keep their words only.
	title = strings.ReplaceAll(title, "`", "")
	return "[[" + target + `\|` + tableSafe(title) + "]]"
}

func cell(h index.QueryHit, c string) string {
	switch c {
	case "path":
		return tableSafe(h.Path)
	case "modified":
		return time.Unix(h.ModTime, 0).UTC().Format("2006-01-02")
	}
	return tableSafe(strings.Join(h.Fields[c], ", "))
}

// tableSafe keeps a value on one table row: pipes escaped, newlines folded.
func tableSafe(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, `\|`, "|")
	return strings.ReplaceAll(s, "|", `\|`)
}
