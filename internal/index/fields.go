package index

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/gosidian/gosidian/internal/parser"
)

// Frontmatter fields (IMP-099, memory_query). Every scalar of a note's
// frontmatter becomes a row of note_fields, every element of a list another
// row under the same key, and a namespaced tag (status:done) stands in for a
// field of that name when the frontmatter has none — the vault states status
// and type as tags more often than as fields. The frontmatter is read with
// the same forgiving parser as the tags, so a note that strict YAML rejects
// (an unquoted colon in a description) still has its fields.

type fieldRow struct {
	key, value string
	num        any // float64 or nil
	date       any // ISO date/datetime text or nil
	source     string
}

var (
	fieldKeyRe  = regexp.MustCompile(`^([a-zA-Z_][\w-]*):[ \t]*(.*)$`)
	fieldNumRe  = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
	fieldDateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}([T ]\d{2}:\d{2}(:\d{2}(\.\d+)?)?(Z|[+-]\d{2}:?\d{2})?)?$`)
)

// extractFields turns a note's raw frontmatter and its tags into rows.
func extractFields(meta string, tags []string) []fieldRow {
	var out []fieldRow
	scalars := parser.ParseFrontmatterFields(meta)
	seen := map[string]bool{}
	for _, line := range strings.Split(meta, "\n") {
		m := fieldKeyRe.FindStringSubmatch(line)
		if m == nil || seen[m[1]] {
			continue // indented lines belong to the key above
		}
		key, rest := m[1], strings.TrimSpace(m[2])
		seen[key] = true
		switch {
		case key == "tags":
			// the tags table's list: frontmatter tags and inline #tags alike
			for _, t := range tags {
				out = append(out, typedField(key, t, "list"))
			}
		case rest == "" || strings.HasPrefix(rest, "["):
			// inline [a, b] or a block list; a nested mapping yields nothing
			for _, v := range parser.FrontmatterList(meta, key) {
				out = append(out, typedField(key, v, "list"))
			}
		default:
			if v, ok := scalars[key].(string); ok && v != "" {
				out = append(out, typedField(key, v, "field"))
			}
		}
	}
	for _, t := range tags {
		ns, v, ok := strings.Cut(t, ":")
		if !ok || v == "" || seen[ns] || !fieldKeyRe.MatchString(ns+":") {
			continue
		}
		out = append(out, typedField(ns, v, "tag"))
	}
	return out
}

func typedField(key, value, source string) fieldRow {
	f := fieldRow{key: key, value: value, source: source}
	if fieldNumRe.MatchString(value) {
		if n, err := strconv.ParseFloat(value, 64); err == nil {
			f.num = n
		}
	}
	if fieldDateRe.MatchString(value) {
		f.date = strings.Replace(value, " ", "T", 1)
	}
	return f
}

// Query operators accepted by FieldCond.Op.
const (
	OpEq       = "eq"
	OpNe       = "ne"
	OpIn       = "in"
	OpExists   = "exists"
	OpLt       = "lt"
	OpLte      = "lte"
	OpGt       = "gt"
	OpGte      = "gte"
	OpContains = "contains"
)

// MaxQueryConds caps the conditions of one query.
const MaxQueryConds = 16

// ErrBadQuery wraps the errors a caller can fix (unknown operator, missing
// value…), as opposed to database failures.
var ErrBadQuery = errors.New("bad query")

// FieldCond is one condition of a query. A note matches when any value of
// the field satisfies it (a list matches element by element); ne and
// exists=false match notes without the field too. Values holds one value,
// several for in, and "true"/"false" (default true) for exists. The type of
// a comparison follows the value: an ISO date compares as a date (a bare day
// covers the whole day), a number as a number, anything else as text,
// ignoring case.
type FieldCond struct {
	Field  string
	Op     string
	Values []string
}

// QueryOptions selects notes by their frontmatter. Projects/Exclude scope the
// query like SearchOptions (nil Projects = every project, empty = none).
// Sort is a field name or one of path, title, modified (the default,
// newest first); Fields lists the fields whose values each hit carries.
type QueryOptions struct {
	Projects []string
	Exclude  []string
	Where    []FieldCond
	Sort     string
	Desc     bool
	Limit    int
	Fields   []string
}

// QueryHit is one matching note, with the requested fields' values in
// frontmatter order (a namespaced tag's value when the field comes from it);
// Lists marks the fields written as lists, so a one-element list stays a list.
type QueryHit struct {
	Path    string
	Title   string
	ModTime int64
	Fields  map[string][]string
	Lists   map[string]bool
}

// Query returns the notes matching every condition, sorted and cut at
// Limit, and how many matched before the cut.
func (i *Index) Query(opts QueryOptions) ([]QueryHit, int, error) {
	if opts.Projects != nil && len(opts.Projects) == 0 {
		return nil, 0, nil
	}
	if len(opts.Where) > MaxQueryConds {
		return nil, 0, fmt.Errorf("%w: at most %d conditions", ErrBadQuery, MaxQueryConds)
	}
	where, args := scopeClause(opts.Projects, opts.Exclude)
	for _, c := range opts.Where {
		sqlc, cargs, err := condSQL(c)
		if err != nil {
			return nil, 0, err
		}
		where += " AND " + sqlc
		args = append(args, cargs...)
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	var total int
	if err := i.db.QueryRow(`SELECT COUNT(*) FROM notes n WHERE 1=1`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return nil, 0, nil
	}

	order, sortArgs := sortSQL(opts.Sort, opts.Desc)
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT n.id, n.path, n.title, n.mtime FROM notes n WHERE 1=1` + where + ` ORDER BY ` + order + ` LIMIT ?`
	all := append(append(append([]any{}, args...), sortArgs...), limit)
	rows, err := i.db.Query(q, all...)
	if err != nil {
		return nil, 0, err
	}
	var hits []QueryHit
	var ids []any
	byID := map[int64]int{}
	for rows.Next() {
		var id int64
		var h QueryHit
		if err := rows.Scan(&id, &h.Path, &h.Title, &h.ModTime); err != nil {
			rows.Close()
			return nil, 0, err
		}
		byID[id] = len(hits)
		ids = append(ids, id)
		hits = append(hits, h)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}

	if len(opts.Fields) > 0 && len(ids) > 0 {
		keys := make([]any, 0, len(opts.Fields))
		for _, k := range opts.Fields {
			keys = append(keys, k)
		}
		frows, err := i.db.Query(`SELECT note_id, key, value, source FROM note_fields WHERE note_id IN (`+placeholders(len(ids))+
			`) AND key IN (`+placeholders(len(keys))+`) ORDER BY rowid`, append(ids, keys...)...)
		if err != nil {
			return nil, 0, err
		}
		defer frows.Close()
		for frows.Next() {
			var id int64
			var k, v, src string
			if err := frows.Scan(&id, &k, &v, &src); err != nil {
				return nil, 0, err
			}
			h := &hits[byID[id]]
			if h.Fields == nil {
				h.Fields = map[string][]string{}
			}
			h.Fields[k] = append(h.Fields[k], v)
			if src == "list" {
				if h.Lists == nil {
					h.Lists = map[string]bool{}
				}
				h.Lists[k] = true
			}
		}
		if err := frows.Err(); err != nil {
			return nil, 0, err
		}
	}
	return hits, total, nil
}

// condSQL renders one condition as an EXISTS test on alias n.
func condSQL(c FieldCond) (string, []any, error) {
	field := strings.TrimSpace(c.Field)
	if field == "" {
		return "", nil, fmt.Errorf("%w: every condition needs a field", ErrBadQuery)
	}
	op := strings.ToLower(strings.TrimSpace(c.Op))
	if op == "" {
		op = OpEq
	}
	has := `EXISTS (SELECT 1 FROM note_fields f WHERE f.note_id = n.id AND f.key = ?`
	one := func() (string, error) {
		if len(c.Values) != 1 {
			return "", fmt.Errorf("%w: %s on %q takes one value", ErrBadQuery, op, field)
		}
		return c.Values[0], nil
	}
	switch op {
	case OpExists:
		want := true
		if len(c.Values) > 0 {
			switch strings.ToLower(strings.TrimSpace(c.Values[0])) {
			case "", "true":
			case "false":
				want = false
			default:
				return "", nil, fmt.Errorf("%w: exists takes true or false", ErrBadQuery)
			}
		}
		if !want {
			return "NOT " + has + ")", []any{field}, nil
		}
		return has + ")", []any{field}, nil
	case OpEq, OpNe, OpIn:
		if len(c.Values) == 0 {
			return "", nil, fmt.Errorf("%w: %s on %q needs a value", ErrBadQuery, op, field)
		}
		if op != OpIn && len(c.Values) > 1 {
			return "", nil, fmt.Errorf("%w: %s on %q takes one value (in takes several)", ErrBadQuery, op, field)
		}
		parts := make([]string, 0, len(c.Values))
		args := []any{field}
		for _, v := range c.Values {
			expr, arg := valueCmp("=", v)
			parts = append(parts, expr)
			args = append(args, arg)
		}
		s := has + " AND (" + strings.Join(parts, " OR ") + "))"
		if op == OpNe {
			s = "NOT " + s
		}
		return s, args, nil
	case OpLt, OpLte, OpGt, OpGte:
		v, err := one()
		if err != nil {
			return "", nil, err
		}
		sym := map[string]string{OpLt: "<", OpLte: "<=", OpGt: ">", OpGte: ">="}[op]
		expr, arg := valueCmp(sym, v)
		return has + " AND " + expr + ")", []any{field, arg}, nil
	case OpContains:
		v, err := one()
		if err != nil {
			return "", nil, err
		}
		if strings.TrimSpace(v) == "" {
			return "", nil, fmt.Errorf("%w: contains on %q needs a non-empty value", ErrBadQuery, field)
		}
		return has + " AND instr(lower(f.value), lower(?)) > 0)", []any{field, v}, nil
	}
	return "", nil, fmt.Errorf("%w: unknown operator %q (eq, ne, in, exists, lt, lte, gt, gte, contains)", ErrBadQuery, c.Op)
}

// valueCmp compares f's value with v by the type v reads as: an ISO date (a
// bare day compares with the day of a datetime), a number, or text ignoring
// case.
func valueCmp(sym, v string) (string, any) {
	v = strings.TrimSpace(v)
	switch {
	case fieldDateRe.MatchString(v):
		d := strings.Replace(v, " ", "T", 1)
		if len(d) == 10 {
			return "substr(f.date, 1, 10) " + sym + " ?", d
		}
		return "f.date " + sym + " ?", d
	case fieldNumRe.MatchString(v):
		n, _ := strconv.ParseFloat(v, 64)
		return "f.num " + sym + " ?", n
	}
	return "f.value " + sym + " ? COLLATE NOCASE", v
}

// sortSQL is the ORDER BY for a sort field: modified (default, newest
// first), path, title, or a frontmatter field — notes without it last, then
// by date, number or text.
func sortSQL(field string, desc bool) (string, []any) {
	dir := "ASC"
	if desc {
		dir = "DESC"
	}
	switch field = strings.TrimSpace(field); field {
	case "":
		return "n.mtime DESC, n.path", nil
	case "modified":
		return "n.mtime " + dir + ", n.path", nil
	case "path":
		return "n.path " + dir, nil
	case "title":
		return "n.title COLLATE NOCASE " + dir + ", n.path", nil
	}
	agg := "MIN"
	if desc {
		agg = "MAX"
	}
	col := func(c string) string {
		return "(SELECT " + agg + "(f." + c + ") FROM note_fields f WHERE f.note_id = n.id AND f.key = ?)"
	}
	missing := "(SELECT COUNT(*) FROM note_fields f WHERE f.note_id = n.id AND f.key = ?) = 0"
	return missing + ", " + col("date") + " " + dir + ", " + col("num") + " " + dir + ", " +
			col("value") + " COLLATE NOCASE " + dir + ", n.path",
		[]any{field, field, field, field}
}

// Request helpers shared by the MCP tool and the REST endpoint, so both read
// a query the same way.

// Query limits of one request.
const (
	QueryDefaultLimit = 50
	QueryMaxLimit     = 500
)

// CondValues turns a JSON value into the strings a condition compares: a
// string, number or boolean, or a list of them (for in).
func CondValues(v any) ([]string, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			s, err := scalarString(e)
			if err != nil {
				return nil, err
			}
			out = append(out, s)
		}
		return out, nil
	default:
		s, err := scalarString(x)
		if err != nil {
			return nil, err
		}
		return []string{s}, nil
	}
}

func scalarString(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), nil
	case bool:
		return strconv.FormatBool(x), nil
	}
	return "", fmt.Errorf("%w: a value is a string, number or boolean, got %T", ErrBadQuery, v)
}

// SortDesc resolves the order of a request: desc unless asked otherwise,
// asc by default for path and title.
func SortDesc(sort, order string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(order)) {
	case "":
		s := strings.TrimSpace(sort)
		return s != "path" && s != "title", nil
	case "asc":
		return false, nil
	case "desc":
		return true, nil
	}
	return false, fmt.Errorf("%w: order is asc or desc", ErrBadQuery)
}

// ClampQueryLimit applies the default and the maximum.
func ClampQueryLimit(n int) int {
	if n <= 0 || n > QueryMaxLimit {
		return QueryDefaultLimit
	}
	return n
}

// DefaultQueryFields returns the fields named by the conditions and the
// sort, so a caller sees the values it filtered on without asking.
func DefaultQueryFields(where []FieldCond, sort string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(f string) {
		f = strings.TrimSpace(f)
		if f == "" || seen[f] {
			return
		}
		seen[f] = true
		out = append(out, f)
	}
	for _, c := range where {
		add(c.Field)
	}
	switch strings.TrimSpace(sort) {
	case "", "path", "title", "modified":
	default:
		add(sort)
	}
	return out
}

// FieldValues is the hit's fields as a response carries them: a single
// value as a string, a list (or several tag values) as a list.
func (h QueryHit) FieldValues() map[string]any {
	if len(h.Fields) == 0 {
		return nil
	}
	out := make(map[string]any, len(h.Fields))
	for k, vs := range h.Fields {
		if len(vs) == 1 && !h.Lists[k] {
			out[k] = vs[0]
		} else {
			out[k] = vs
		}
	}
	return out
}
