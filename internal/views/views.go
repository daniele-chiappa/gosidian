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
// and renders them as a markdown table, a list, a board (as: board, with
// group_by: a select or checkbox field whose values make the columns), or a
// count (as: count, optionally per value of group_by). A view is computed when the
// note is read; the file keeps only the spec, so a note whose data changes
// never needs rewriting. The caller runs the query with its own scope, so a
// reader only ever sees rows it may read.
package views

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/gosidian/gosidian/internal/dbschema"
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
	As      string // "table", "list", "board" or "count"
	// GroupBy is the field whose values make the columns of a board, or
	// the groups of a count.
	GroupBy string
	// SortOrder orders the sort field by the options of its select in the
	// schema of the database the view lists; compute sets it.
	SortOrder []string
}

// Context resolves the relative values a spec may use: this, the note
// holding the view, this.<field>, its fields, and today±Nd.
type Context struct {
	This  map[string][]string
	Today time.Time
	// Schema returns the schema of the database whose rows are the notes of
	// folder, or nil. It types the columns of a view that lists one folder
	// and orders the columns of a board. The caller resolves it within the
	// reader's scope. Nil means no schema.
	Schema func(folder string) *dbschema.Schema
	// CanWrite reports whether the reader may edit the note at path; it
	// marks the rows of Data. Nil means no row is writable.
	CanWrite func(path string) bool
	// Resolve returns the path of the note a wikilink target names, among
	// those the reader may see, or "". It resolves the links in the field
	// values of Data and the notes a condition on relations names
	// (related contains [[p/x]]). Nil leaves them unresolved: such a
	// condition then works with this only.
	Resolve func(target string) string
}

// resolveLink resolves a link target of a condition: the note holding the
// view (this), or a note the reader may see.
func (c Context) resolveLink(target string) string {
	if p := c.This["path"]; len(p) > 0 && p[0] != "" && target == p[0] {
		return p[0]
	}
	if c.Resolve != nil {
		return c.Resolve(target)
	}
	return ""
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
	GroupBy string   `yaml:"group_by"`
}

// Parse reads a view spec.
func Parse(src string, c Context) (*Spec, error) {
	var r rawSpec
	if err := yaml.Unmarshal([]byte(src), &r); err != nil {
		return nil, fmt.Errorf("spec is not valid YAML: %w", err)
	}
	s := &Spec{Columns: r.Columns, As: strings.TrimSpace(r.As), GroupBy: strings.TrimSpace(r.GroupBy)}
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
	case "table", "list", "board", "count":
	default:
		return nil, fmt.Errorf("as: %q is not table, list, board or count", s.As)
	}
	switch {
	case s.As == "board" && s.GroupBy == "":
		return nil, errors.New("as: board needs group_by: the select or checkbox field whose values make the columns")
	case s.As != "board" && s.As != "count" && s.GroupBy != "":
		return nil, errors.New("group_by works only with as: board or as: count")
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
		cond, err = linkCond(fixOp(cond), c)
		if err != nil {
			return index.FieldCond{}, fmt.Errorf("condition %q: %w", t, err)
		}
		return cond, nil
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
		return linkCond(fixOp(index.FieldCond{Field: field, Op: op, Values: vals}), c)
	}
	return index.FieldCond{}, fmt.Errorf("condition %v: want a string or a {field, op, value} map", w)
}

// linkCond turns a condition on relations into a test on links (IMP-127
// iteration 2): `related contains this`, `links contains this`,
// `related = [[p/x]]`. See index.ResolveLinkConds.
func linkCond(cond index.FieldCond, c Context) (index.FieldCond, error) {
	out, err := index.ResolveLinkConds([]index.FieldCond{cond}, c.resolveLink)
	if err != nil {
		return index.FieldCond{}, errors.New(strings.TrimPrefix(err.Error(), index.ErrBadQuery.Error()+": "))
	}
	return out[0], nil
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
	if _, link := index.LinkTarget(v); link && !strings.HasPrefix(v, "[[[") {
		return []string{v} // a [[wikilink]], not a list
	}
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

// resolve expands this (as a link to the note holding the view),
// this.<field> and today±Nd.
func resolve(vals []string, c Context) ([]string, error) {
	var out []string
	for _, v := range vals {
		if v == "this" {
			p := c.This["path"]
			if len(p) == 0 || p[0] == "" {
				return nil, errors.New("this: the view is not in a saved note")
			}
			out = append(out, "[["+p[0]+"]]")
			continue
		}
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
	// Schema is the database the view lists, when it lists the rows of one
	// (Context.Schema), or nil.
	Schema *dbschema.Schema
}

// countRowsCap bounds the rows a count per group reads: the groups of a
// count over more rows than this come from the first ones (the total stays
// exact).
const countRowsCap = 10000

// Run computes a view.
func Run(s *Spec, q QueryFunc) (*Result, error) {
	fields := withoutBuiltins(s.Columns)
	if s.GroupBy != "" && !slices.Contains(fields, s.GroupBy) {
		fields = append(fields, s.GroupBy)
	}
	limit := s.Limit
	if s.As == "count" {
		// A count needs the total, and the group_by values of every row.
		fields, limit = nil, 1
		if s.GroupBy != "" {
			fields, limit = []string{s.GroupBy}, countRowsCap
		}
	}
	hits, total, err := q(index.QueryOptions{
		Folders: s.From, Where: s.Where, Sort: s.Sort, SortOrder: s.SortOrder, Desc: s.Desc, Limit: limit,
		Fields: fields,
	})
	if err != nil {
		return nil, err
	}
	return &Result{Spec: s, Hits: hits, Total: total}, nil
}

// compute parses and computes a view in context c: with the schema of the
// database it lists, and a board checked against it.
func compute(spec string, c Context, q QueryFunc) (*Result, error) {
	s, err := Parse(spec, c)
	if err != nil {
		return nil, err
	}
	var schema *dbschema.Schema
	if c.Schema != nil && len(s.From) == 1 {
		schema = c.Schema(s.From[0])
	}
	if schema != nil {
		s.SortOrder = schema.OptionOrder(s.Sort)
		// The rows of the database only (rows: {type: plan}): a view of the
		// folder leaves out its index and other notes, and a new row made
		// from it starts with those values.
		for _, kv := range schema.RowConds() {
			s.Where = append(s.Where, index.FieldCond{Field: kv[0], Op: index.OpEq, Values: []string{kv[1]}})
		}
	}
	r, err := Run(s, q)
	if err != nil {
		return nil, err
	}
	r.Schema = schema
	if s.As == "board" {
		if _, err := r.Groups(); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// Group is a column of a board: the notes whose group_by field has Value,
// "" for the notes without one.
type Group struct {
	Value string
	Hits  []index.QueryHit
}

// Groups splits the notes of a board by their group_by field. The columns
// follow the options of a select field in the schema (false then true for a
// checkbox), then any other value found, alphabetically; the column of the
// notes without a value comes last, and only when it holds some.
func (r *Result) Groups() ([]Group, error) {
	field := r.Spec.GroupBy
	var order []string
	if r.Schema != nil {
		if f, ok := r.Schema.Field(field); ok {
			switch f.Type {
			case "select":
				order = f.Options
			case "checkbox":
				order = []string{"false", "true"}
			default:
				return nil, fmt.Errorf("group_by: %q is a %s field; a board groups by a select or checkbox field", field, f.Type)
			}
		}
	}
	byValue := map[string][]index.QueryHit{}
	var extra []string
	for _, h := range r.Hits {
		v := ""
		if vs := h.Fields[field]; len(vs) > 0 {
			v = vs[0]
		}
		if _, seen := byValue[v]; !seen && v != "" && !slices.Contains(order, v) {
			extra = append(extra, v)
		}
		byValue[v] = append(byValue[v], h)
	}
	sort.Strings(extra)
	var out []Group
	for _, v := range append(append(slices.Clone(order), extra...), "") {
		if v == "" && len(byValue[""]) == 0 {
			continue
		}
		out = append(out, Group{Value: v, Hits: byValue[v]})
	}
	return out, nil
}

// Count is the number of notes of a count view with a group_by value, ""
// for the notes without one.
type Count struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// Counts splits the notes of a count by their group_by field: a note counts
// once for each of its values (a list field), the groups in the order of the
// field's options in the schema, then the other values alphabetically, then
// the notes without a value. Groups with no notes are left out.
func (r *Result) Counts() []Count {
	field := r.Spec.GroupBy
	if field == "" {
		return nil
	}
	n := map[string]int{}
	for _, h := range r.Hits {
		vs := h.Fields[field]
		if len(vs) == 0 {
			n[""]++
			continue
		}
		seen := map[string]bool{}
		for _, v := range vs {
			if !seen[v] {
				seen[v] = true
				n[v]++
			}
		}
	}
	var order []string
	if r.Schema != nil {
		order = r.Schema.OptionOrder(field)
		if f, ok := r.Schema.Field(field); ok && f.Type == "checkbox" {
			order = []string{"false", "true"}
		}
	}
	var extra []string
	for v := range n {
		if v != "" && !slices.Contains(order, v) {
			extra = append(extra, v)
		}
	}
	sort.Strings(extra)
	var out []Count
	for _, v := range append(append(slices.Clone(order), extra...), "") {
		if n[v] > 0 {
			out = append(out, Count{Value: v, Count: n[v]})
			delete(n, v) // an option listed twice counts once
		}
	}
	return out
}

// countText is a count for agents: "**12** notes", then the groups.
func (r *Result) countText() string {
	unit := "notes"
	if r.Total == 1 {
		unit = "note"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "**%d** %s", r.Total, unit)
	if cs := r.Counts(); len(cs) > 0 {
		b.WriteString(" · " + r.Spec.GroupBy + ":")
		for i, c := range cs {
			if i > 0 {
				b.WriteString(" ·")
			}
			name := c.Value
			if name == "" {
				name = "(no value)"
			}
			fmt.Fprintf(&b, " %s %d", tableSafe(name), c.Count)
		}
	}
	return b.String()
}

// Markdown renders the result as a table or a list. Note links are
// [[path\|title]], which the renderer turns into links before it reads the
// table, so the same text serves agents and the web UI.
func (r *Result) Markdown() string {
	var b strings.Builder
	if r.Spec.As == "count" {
		return r.countText() + "\n"
	}
	if len(r.Hits) == 0 {
		return "_No matching notes._\n"
	}
	cols := r.Spec.Columns
	listItem := func(h index.QueryHit) {
		b.WriteString("- " + link(h))
		for _, c := range cols {
			if c == "title" || c == r.Spec.GroupBy {
				continue
			}
			if v := cell(h, c); v != "" {
				b.WriteString(" · " + c + ": " + v)
			}
		}
		b.WriteString("\n")
	}
	switch r.Spec.As {
	case "list":
		for _, h := range r.Hits {
			listItem(h)
		}
	case "board":
		// Agents read a board as a list per column; empty columns are left out.
		groups, _ := r.Groups()
		for i, g := range groups {
			if len(g.Hits) == 0 {
				continue
			}
			if i > 0 && b.Len() > 0 {
				b.WriteString("\n")
			}
			name := g.Value
			if name == "" {
				name = "(no value)"
			}
			fmt.Fprintf(&b, "**%s: %s** (%d)\n\n", r.Spec.GroupBy, tableSafe(name), len(g.Hits))
			for _, h := range g.Hits {
				listItem(h)
			}
		}
	default:
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

// MarkdownWithin renders the result as Markdown in about budget bytes: the
// rows that fit, then a line with how many notes are left out and the
// memory_query that returns them all (IMP-136). budget <= 0 means no limit.
func (r *Result) MarkdownWithin(budget int) string {
	full := r.Markdown()
	if budget <= 0 || len(full) <= budget {
		return full
	}
	tail := func(shown int) string {
		return fmt.Sprintf("\n_Showing %d of %d notes, cut to fit: all of them with %s._\n", shown, r.Total, r.QueryCall())
	}
	limit := budget - len(tail(len(r.Hits)))
	table := r.Spec.As != "list" && r.Spec.As != "board"
	var b strings.Builder
	shown := 0
	for i, l := range strings.SplitAfter(full, "\n") {
		if strings.HasPrefix(l, "_Showing ") {
			continue
		}
		row := strings.HasPrefix(l, "- ")
		if table {
			row = i >= 2 && strings.HasPrefix(l, "| ")
		}
		if b.Len()+len(l) > limit && (row || shown > 0) {
			break
		}
		if row {
			shown++
		}
		b.WriteString(l)
	}
	return strings.TrimRight(b.String(), "\n") + "\n" + tail(shown)
}

// QueryCall is the memory_query call that returns all the notes of the
// view, with the fields it shows.
func (r *Result) QueryCall() string {
	type cond struct {
		Field string `json:"field"`
		Op    string `json:"op"`
		Value any    `json:"value"`
	}
	type call struct {
		Project string   `json:"project"`
		From    any      `json:"from"`
		Where   []cond   `json:"where,omitempty"`
		Sort    string   `json:"sort,omitempty"`
		Order   string   `json:"order,omitempty"`
		Fields  []string `json:"fields,omitempty"`
		Limit   int      `json:"limit"`
	}
	c := call{Project: strings.SplitN(r.Spec.From[0], "/", 2)[0], From: r.Spec.From[0], Sort: r.Spec.Sort}
	if len(r.Spec.From) > 1 {
		c.From = r.Spec.From
	}
	for _, w := range r.Spec.Where {
		if w.Link {
			links := make([]string, len(w.Values))
			for i, p := range w.Values {
				links[i] = linkTo(p)
			}
			w.Values = links
		}
		var v any = w.Values
		switch {
		case w.Op == index.OpExists && len(w.Values) == 1:
			v = w.Values[0] == "true"
		case w.Op != index.OpIn && len(w.Values) == 1:
			v = w.Values[0]
		}
		c.Where = append(c.Where, cond{Field: w.Field, Op: w.Op, Value: v})
	}
	if c.Sort != "" {
		c.Order = "asc"
		if r.Spec.Desc {
			c.Order = "desc"
		}
	}
	c.Fields = withoutBuiltins(r.Spec.Columns)
	if r.Spec.GroupBy != "" && !slices.Contains(c.Fields, r.Spec.GroupBy) {
		c.Fields = append(c.Fields, r.Spec.GroupBy)
	}
	c.Limit = min(max(r.Total, 1), index.QueryMaxLimit)
	b, _ := json.Marshal(c)
	return "memory_query(" + string(b) + ")"
}

// linkTo is the [[wikilink]] that names the note at p: its path, without
// .md (a .html note keeps its extension, which tells it from a .md namesake).
func linkTo(p string) string {
	return "[[" + strings.TrimSuffix(p, ".md") + "]]"
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
