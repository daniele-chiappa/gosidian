package views

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/gosidian/gosidian/internal/dbschema"
	"github.com/gosidian/gosidian/internal/index"
)

// Rollups (IMP-127 iteration 3, phase 2): fields a database declares as
// `type: rollup`, computed for each row when a view, a query or the row
// itself is read, and never written. A rollup is a view spec in which `this`
// is the row — `related contains this` selects the notes whose related
// field points at it, `path in this.implements_imp` those the row's own
// relation points at — counted, or summed, or reduced to its least or
// greatest value of one field.

// MaxRollupSortRows bounds the rows a view or a query sorted by a rollup
// reads: every one of them is computed before the sort.
const MaxRollupSortRows = 500

var thisFieldRe = regexp.MustCompile(`\bthis\.([A-Za-z_][\w.-]*)`)

// RollupFields returns the rollup fields of schema named in names, once each,
// in the order of names. Nil when the schema is nil or declares none.
func RollupFields(schema *dbschema.Schema, names ...string) []dbschema.Field {
	if schema == nil {
		return nil
	}
	var out []dbschema.Field
	for _, n := range names {
		if f, ok := schema.Field(n); ok && f.Rollup != nil && !slices.ContainsFunc(out, func(g dbschema.Field) bool { return g.Name == n }) {
			out = append(out, f)
		}
	}
	return out
}

// ThisFieldsOf lists the fields of the row the rollups read (this.<field>),
// which the query of the rows must fetch.
func ThisFieldsOf(fs []dbschema.Field) []string {
	var out []string
	for _, f := range fs {
		for _, m := range thisFieldRe.FindAllStringSubmatch(f.Rollup.Spec, -1) {
			if !slices.Contains(out, m[1]) {
				out = append(out, m[1])
			}
		}
	}
	return out
}

// RowThis is the this of a rollup computed for the note h: its path, name and
// the fields the query read.
func RowThis(h index.QueryHit) map[string][]string {
	out := map[string][]string{"path": {h.Path}, "name": {strings.TrimSuffix(path.Base(h.Path), path.Ext(h.Path))}}
	for k, v := range h.Fields {
		out[k] = v
	}
	return out
}

// ComputeRollups sets the value of each rollup of fs on each hit, as the only
// value of the field (removed when there is none: a min of no notes). The
// rows' fields that the rollups read must be in the hits (ThisFieldsOf).
func ComputeRollups(hits []index.QueryHit, fs []dbschema.Field, c Context, q QueryFunc) error {
	// One schema lookup per folder for the whole table, where it ran for
	// every row and rollup: in MCP each one loads every database note of
	// the project (BUG-114, S4-8).
	if c.Schema != nil {
		lookup, seen := c.Schema, map[string]*dbschema.Schema{}
		c.Schema = func(folder string) *dbschema.Schema {
			if sc, ok := seen[folder]; ok {
				return sc
			}
			sc := lookup(folder)
			seen[folder] = sc
			return sc
		}
	}
	for i := range hits {
		h := &hits[i]
		rc := c
		rc.This = RowThis(*h)
		for _, f := range fs {
			v, err := RollupValue(f, rc, q)
			if err != nil {
				return fmt.Errorf("rollup %s: %w", f.Name, err)
			}
			if h.Fields == nil {
				h.Fields = map[string][]string{}
			}
			if v == "" {
				delete(h.Fields, f.Name)
				continue
			}
			h.Fields[f.Name] = []string{v}
		}
	}
	return nil
}

// RollupValue computes one rollup for the row c.This describes, with q, the
// reader's query: only the notes the reader may see count. A count is a
// number, 0 included; sum, min and max read the field Of of the notes, and
// give "" when no note has a value. The rows of a database the spec lists
// are its rows only (rows: {type: plan}), as in a view.
func RollupValue(f dbschema.Field, c Context, q QueryFunc) (string, error) {
	r := f.Rollup
	// A row's relation may name a note that is gone, or one the reader may
	// not see: that link matches no note, where it failed the whole view,
	// every row with it (BUG-114, S4-1).
	rc := c
	rc.Resolve = func(target string) string {
		if c.Resolve != nil {
			if p := c.Resolve(target); p != "" {
				return p
			}
		}
		return noNote
	}
	s, err := Parse(r.Spec, rc)
	if err != nil {
		var missing noThisField
		if errors.As(err, &missing) {
			// The row has no such relation: nothing to count.
			if r.Calc == "count" {
				return "0", nil
			}
			return "", nil
		}
		return "", err
	}
	if c.Schema != nil && len(s.From) == 1 {
		if schema := c.Schema(schemaFolder(s.From[0])); schema != nil {
			for _, kv := range schema.RowConds() {
				s.Where = append(s.Where, index.FieldCond{Field: kv[0], Op: index.OpEq, Values: []string{kv[1]}})
			}
		}
	}
	opts := index.QueryOptions{Folders: s.From, Where: s.Where, Limit: 1}
	if r.Calc != "count" {
		opts.Limit, opts.Fields = countRowsCap, []string{r.Of}
	}
	hits, total, err := q(opts)
	if err != nil {
		return "", err
	}
	if r.Calc == "count" {
		return strconv.Itoa(total), nil
	}
	var values []string
	for _, h := range hits {
		values = append(values, h.Fields[r.Of]...)
	}
	return aggregate(r.Calc, values), nil
}

// noNote is a path no note has, for a link that names none.
const noNote = "\x00no note"

// aggregate reduces the values of a sum, min or max: numbers when every value
// is one, otherwise text, which orders ISO dates too. A sum of anything but
// numbers counts only the numbers.
func aggregate(calc string, values []string) string {
	nums := make([]float64, 0, len(values))
	for _, v := range values {
		if n, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			nums = append(nums, n)
		}
	}
	if calc == "sum" {
		total := 0.0
		for _, n := range nums {
			total += n
		}
		return formatNumber(total)
	}
	if len(values) == 0 {
		return ""
	}
	if len(nums) == len(values) {
		v := slices.Min(nums)
		if calc == "max" {
			v = slices.Max(nums)
		}
		return formatNumber(v)
	}
	if calc == "max" {
		return slices.Max(values)
	}
	return slices.Min(values)
}

func formatNumber(n float64) string {
	return strconv.FormatFloat(n, 'f', -1, 64)
}

// sortFields names the fields of keys, in their order.
func sortFields(keys []index.SortKey) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = k.Field
	}
	return out
}

// rollupKey is the first key of keys that is a rollup of schema, or "".
func rollupKey(schema *dbschema.Schema, keys []index.SortKey) string {
	for _, k := range keys {
		if len(RollupFields(schema, k.Field)) > 0 {
			return k.Field
		}
	}
	return ""
}

// RunWithRollups runs s like Run, then computes the rollups of schema it
// shows or sorts by. Sorted by a rollup, it reads every row (up to
// MaxRollupSortRows), computes and sorts them, and keeps the first s.Limit.
// A condition or a group_by on a rollup is an error: the value exists only
// once computed.
func RunWithRollups(s *Spec, schema *dbschema.Schema, c Context, q QueryFunc) (*Result, error) {
	for _, w := range s.Where {
		if len(RollupFields(schema, w.Field)) > 0 {
			return nil, fmt.Errorf("where: %q is a rollup, computed when the rows are read: filter on the fields it counts, or sort by it", w.Field)
		}
	}
	if len(RollupFields(schema, s.GroupBy)) > 0 {
		return nil, fmt.Errorf("group_by: %q is a rollup, computed when the rows are read; a board or a count groups by a field of the rows", s.GroupBy)
	}
	keys := s.SortKeys()
	names := append(slices.Clone(s.Columns), sortFields(keys)...)
	fs := RollupFields(schema, names...)
	if len(fs) == 0 || s.As == "count" {
		return Run(s, q)
	}
	s.Read = append(s.Read, ThisFieldsOf(fs)...)
	byRollup := rollupKey(schema, keys)
	run := s
	if byRollup != "" {
		// Every key is sorted here, on the values the rows carry: the
		// fields of the other keys are read too.
		cp := *s
		cp.Sort, cp.SortOrder, cp.ThenBy, cp.Limit = "", nil, nil, MaxRollupSortRows
		cp.Read = append(slices.Clone(s.Read), withoutBuiltins(sortFields(keys))...)
		run = &cp
	}
	r, err := Run(run, q)
	if err != nil {
		return nil, err
	}
	if byRollup != "" && r.Total > MaxRollupSortRows {
		return nil, fmt.Errorf("sort: %q is a rollup, and a sort by a rollup reads at most %d rows; this view selects %d: narrow it with where", byRollup, MaxRollupSortRows, r.Total)
	}
	if err := ComputeRollups(r.Hits, fs, c, q); err != nil {
		return nil, err
	}
	if byRollup != "" {
		index.SortHits(r.Hits, keys)
		if len(r.Hits) > s.Limit {
			r.Hits = r.Hits[:s.Limit]
		}
	}
	r.Spec = s
	return r, nil
}

// RollupQuery runs a query over the rows of schema (opts.Folders its source)
// with the rollups it names in Fields or Sort: main runs the query of the
// rows, q the queries of the rollups, both with the reader's scope. Sorted
// by a rollup, it reads every row up to MaxRollupSortRows, then sorts and
// cuts at opts.Limit. The fields of the rows the rollups read but the query
// did not ask for are left out of the hits. A condition on a rollup is an
// index.ErrBadQuery.
func RollupQuery(opts index.QueryOptions, schema *dbschema.Schema, c Context, main, q QueryFunc) ([]index.QueryHit, int, error) {
	keys := opts.SortKeys()
	fs := RollupFields(schema, append(slices.Clone(opts.Fields), sortFields(keys)...)...)
	for _, w := range opts.Where {
		if len(RollupFields(schema, w.Field)) > 0 {
			return nil, 0, fmt.Errorf("%w: %q is a rollup, computed when the rows are read: filter on the fields it counts, or sort by it", index.ErrBadQuery, w.Field)
		}
	}
	if len(fs) == 0 {
		return main(opts)
	}
	run := opts
	var extra []string
	read := func(f string) {
		if !slices.Contains(run.Fields, f) {
			run.Fields = append(slices.Clone(run.Fields), f)
			extra = append(extra, f)
		}
	}
	for _, f := range ThisFieldsOf(fs) {
		read(f)
	}
	byRollup := rollupKey(schema, keys)
	if byRollup != "" {
		// Every key is sorted here, on the values the rows carry.
		for _, f := range withoutBuiltins(sortFields(keys)) {
			read(f)
		}
		run.SetSortKeys(nil)
		run.Limit = MaxRollupSortRows
	}
	hits, total, err := main(run)
	if err != nil {
		return nil, 0, err
	}
	if byRollup != "" && total > MaxRollupSortRows {
		return nil, 0, fmt.Errorf("%w: sort: %q is a rollup, and a sort by a rollup reads at most %d rows; this query selects %d: narrow it with where", index.ErrBadQuery, byRollup, MaxRollupSortRows, total)
	}
	if err := ComputeRollups(hits, fs, c, q); err != nil {
		return nil, 0, err
	}
	if byRollup != "" {
		index.SortHits(hits, keys)
		if opts.Limit > 0 && len(hits) > opts.Limit {
			hits = hits[:opts.Limit]
		}
	}
	for i := range hits {
		for _, f := range extra {
			delete(hits[i].Fields, f)
		}
	}
	return hits, total, nil
}

// RollupsAcross refuses a query over several folders that names a rollup
// of one of their databases in where, fields or sort: a rollup is computed
// over the rows of one database, and the query would give no value, or no
// row, without saying why (BUG-096).
func RollupsAcross(folders []string, schemaOf func(string) *dbschema.Schema, opts index.QueryOptions) error {
	if len(folders) < 2 || schemaOf == nil {
		return nil
	}
	names := append(slices.Clone(opts.Fields), sortFields(opts.SortKeys())...)
	for _, w := range opts.Where {
		names = append(names, w.Field)
	}
	for _, folder := range folders {
		for _, f := range RollupFields(schemaOf(folder), names...) {
			return fmt.Errorf("%w: %q is a rollup of the database of %s, computed over the rows of one database: query that folder alone (from: [%s])", index.ErrBadQuery, f.Name, folder, folder)
		}
	}
	return nil
}

// NumberRollups gives the rollups among values, as FieldValues returns
// them, as numbers rather than text.
func NumberRollups(values map[string]any, schema *dbschema.Schema) map[string]any {
	if schema == nil {
		return values
	}
	for k, v := range values {
		s, ok := v.(string)
		if !ok || len(RollupFields(schema, k)) == 0 {
			continue
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			values[k] = n
		} else if f, err := strconv.ParseFloat(s, 64); err == nil {
			values[k] = f
		}
	}
	return values
}
