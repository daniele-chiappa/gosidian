package index

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
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

// extractFields turns a note's raw frontmatter and its tags into rows. The
// frontmatter is read by parser.FrontmatterEntries (IMP-138): a list gives a
// row per item, a scalar one row, a nested map nothing.
func extractFields(meta string, tags []string) []fieldRow {
	var out []fieldRow
	seen := map[string]bool{}
	for _, e := range parser.FrontmatterEntries(meta) {
		if !fieldKeyRe.MatchString(e.Key + ":") {
			continue
		}
		seen[e.Key] = true
		switch {
		case e.Key == "tags":
			// the tags table's list: frontmatter tags and inline #tags alike
			for _, t := range tags {
				out = append(out, typedField(e.Key, t, "list"))
			}
		case e.Kind == parser.FMList:
			for _, v := range parser.FrontmatterList(meta, e.Key) {
				out = append(out, typedField(e.Key, v, "list"))
			}
		case e.Kind == parser.FMScalar && e.Text != "":
			out = append(out, typedField(e.Key, e.Text, "field"))
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
	// Link makes the condition a test on the note's links (IMP-127
	// iteration 2): Values are note paths, and the note matches when it
	// links to one of them from the frontmatter field Field, or from
	// anywhere for the pseudo-field links. ResolveLinkConds sets it.
	Link bool
}

// LinksField is the pseudo-field of a condition on all the links of a note,
// body and frontmatter alike: `links contains [[p/x]]` keeps the notes that
// link to p/x, its backlinks as a query.
const LinksField = "links"

// PathField is the pseudo-field of a condition on the note's own path:
// `path in this.implements_imp` keeps the notes a relation of the row points
// at (IMP-127 iteration 3). Its values are [[wikilinks]] or vault paths; it
// takes eq, ne and in.
const PathField = "path"

// LinkTarget returns the target of v when v is written as one [[wikilink]],
// alias and heading dropped.
func LinkTarget(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "[[") || !strings.HasSuffix(v, "]]") || strings.Count(v, "[[") != 1 {
		return "", false
	}
	t, _, _ := strings.Cut(strings.TrimSuffix(strings.TrimPrefix(v, "[["), "]]"), "|")
	t, _, _ = strings.Cut(strings.TrimSuffix(t, `\`), "#")
	t = strings.TrimSpace(t)
	return t, t != ""
}

// ResolveLinkConds turns the conditions on relations into link conditions:
// one whose values are written as [[wikilinks]] (related contains [[p/x]]),
// and any condition on the pseudo-field links, whose values may also be
// plain note paths. resolve maps a link target to the path of a note the
// reader may see, "" when there is none; a target that names no note is an
// error, rather than a filter that silently matches nothing. The other
// conditions are returned as they are.
func ResolveLinkConds(where []FieldCond, resolve func(target string) string) ([]FieldCond, error) {
	out := make([]FieldCond, 0, len(where))
	for _, c := range where {
		field := strings.TrimSpace(c.Field)
		if field == PathField && !c.Link {
			pc, err := pathCond(c, resolve)
			if err != nil {
				return nil, err
			}
			out = append(out, pc)
			continue
		}
		links := field == LinksField
		targets := make([]string, 0, len(c.Values))
		for _, v := range c.Values {
			if t, ok := LinkTarget(v); ok {
				targets = append(targets, t)
			} else if links {
				targets = append(targets, strings.TrimSpace(v))
			}
		}
		if c.Link || len(targets) == 0 && !links {
			out = append(out, c)
			continue
		}
		op := strings.ToLower(strings.TrimSpace(c.Op))
		if op == "" {
			op = OpEq
		}
		switch op {
		case OpEq, OpNe, OpIn, OpContains:
		default:
			return nil, fmt.Errorf("%w: %s on %q: a condition on links takes eq, ne, in or contains", ErrBadQuery, op, field)
		}
		if len(targets) != len(c.Values) {
			return nil, fmt.Errorf("%w: %s on %q mixes [[wikilinks]] with other values", ErrBadQuery, op, field)
		}
		if len(targets) == 0 {
			return nil, fmt.Errorf("%w: %s on %q needs a note: a [[wikilink]] or a path", ErrBadQuery, op, field)
		}
		paths := make([]string, len(targets))
		for k, t := range targets {
			if resolve != nil {
				paths[k] = resolve(t)
			}
			if paths[k] == "" {
				return nil, fmt.Errorf("%w: %s on %q: [[%s]] names no note", ErrBadQuery, op, field, t)
			}
		}
		out = append(out, FieldCond{Field: field, Op: op, Values: paths, Link: true})
	}
	return out, nil
}

// pathCond resolves a condition on the note's path: each value a [[wikilink]]
// resolved with resolve, or a vault path as it is. A wikilink that names no
// note is an error, as in a condition on links.
func pathCond(c FieldCond, resolve func(target string) string) (FieldCond, error) {
	op := strings.ToLower(strings.TrimSpace(c.Op))
	if op == "" {
		op = OpEq
	}
	switch op {
	case OpEq, OpNe, OpIn:
	default:
		return FieldCond{}, fmt.Errorf("%w: %s on path: a condition on the path takes eq, ne or in", ErrBadQuery, op)
	}
	if len(c.Values) == 0 {
		return FieldCond{}, fmt.Errorf("%w: %s on path needs a note: a [[wikilink]] or a path", ErrBadQuery, op)
	}
	paths := make([]string, 0, len(c.Values))
	for _, v := range c.Values {
		t, link := LinkTarget(v)
		if !link {
			paths = append(paths, strings.TrimSpace(v))
			continue
		}
		p := ""
		if resolve != nil {
			p = resolve(t)
		}
		if p == "" {
			return FieldCond{}, fmt.Errorf("%w: %s on path: [[%s]] names no note", ErrBadQuery, op, t)
		}
		paths = append(paths, p)
	}
	if op == OpEq && len(paths) > 1 {
		op = OpIn
	}
	return FieldCond{Field: PathField, Op: op, Values: paths, Link: true}, nil
}

// QueryOptions selects notes by their frontmatter. Projects/Exclude scope the
// query like SearchOptions (nil Projects = every project, empty = none).
// Sort is a field name or one of path, title, modified (the default,
// newest first), and ThenBy the keys that order the notes Sort leaves tied;
// Fields lists the fields whose values each hit carries.
type QueryOptions struct {
	Projects []string
	Exclude  []string
	// Folders, when set, keeps only notes directly inside one of these
	// vault-relative folders, the rows of a database note (IMP-127); a
	// folder written "f/**" takes the notes of every folder under it too.
	Folders []string
	// Paths, when set, keeps only these notes.
	Paths []string
	Where []FieldCond
	Sort  string
	// SortOrder, when set, orders the Sort field by the position of its
	// value in this list, the options of a select field (IMP-127): values
	// not listed come after the listed ones, notes without the field last.
	SortOrder []string
	Desc      bool
	// ThenBy are the keys after Sort (IMP-143), at most MaxSortKeys-1.
	ThenBy []SortKey
	Limit  int
	// Offset skips that many notes of the order first: a page after the
	// first, for a reader that needs every match (the automations).
	Offset int
	Fields []string
}

// SortKey is one key of a sort: a field or path, title, modified; its
// direction; and Order, the options of a select field it ranks by, like
// QueryOptions.SortOrder.
type SortKey struct {
	Field string
	Desc  bool
	Order []string
}

// MaxSortKeys bounds the keys of one sort.
const MaxSortKeys = 4

// SortKeys returns the keys of the sort, Sort first; nil for the default
// (modified, newest first).
func (o QueryOptions) SortKeys() []SortKey {
	if strings.TrimSpace(o.Sort) == "" && len(o.ThenBy) == 0 {
		return nil
	}
	return append([]SortKey{{Field: strings.TrimSpace(o.Sort), Desc: o.Desc, Order: o.SortOrder}}, o.ThenBy...)
}

// SetSortKeys sets the sort to keys, the first in Sort, Desc and SortOrder.
func (o *QueryOptions) SetSortKeys(keys []SortKey) {
	o.Sort, o.Desc, o.SortOrder, o.ThenBy = "", false, nil, nil
	if len(keys) == 0 {
		return
	}
	o.Sort, o.Desc, o.SortOrder = keys[0].Field, keys[0].Desc, keys[0].Order
	o.ThenBy = keys[1:]
}

// ParseSort reads a sort of one or more keys separated by commas, each a
// field with an optional asc or desc: "plans desc, id". order is the
// direction of the keys without one, as SortDesc resolves it. An empty sort
// is nil, the default.
func ParseSort(sort, order string) ([]SortKey, error) {
	if _, err := SortDesc("", order); err != nil {
		return nil, err
	}
	sort = strings.TrimSpace(sort)
	if sort == "" {
		return nil, nil
	}
	parts := strings.Split(sort, ",")
	if len(parts) > MaxSortKeys {
		return nil, fmt.Errorf("%w: sort: at most %d keys, got %d", ErrBadQuery, MaxSortKeys, len(parts))
	}
	keys := make([]SortKey, 0, len(parts))
	for _, part := range parts {
		words := strings.Fields(part)
		switch {
		case len(words) == 0:
			return nil, fmt.Errorf("%w: sort: %q has an empty key", ErrBadQuery, sort)
		case len(words) > 2:
			return nil, fmt.Errorf("%w: sort: %q: a key is a field, then asc or desc", ErrBadQuery, strings.TrimSpace(part))
		}
		dir := order
		if len(words) == 2 {
			dir = words[1]
		}
		desc, err := SortDesc(words[0], dir)
		if err != nil {
			return nil, fmt.Errorf("sort: %q: %w", strings.TrimSpace(part), err)
		}
		keys = append(keys, SortKey{Field: words[0], Desc: desc})
	}
	return keys, nil
}

// SortString writes keys back as a sort, each with its direction.
func SortString(keys []SortKey) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		dir := "asc"
		if k.Desc {
			dir = "desc"
		}
		parts[i] = k.Field + " " + dir
	}
	return strings.Join(parts, ", ")
}

// SortFields names the fields of a sort, builtins included, in its order.
func SortFields(sort string) []string {
	var out []string
	for _, part := range strings.Split(sort, ",") {
		if words := strings.Fields(part); len(words) > 0 {
			out = append(out, words[0])
		}
	}
	return out
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
	if len(opts.Folders) > 0 {
		ors := make([]string, 0, len(opts.Folders))
		for _, f := range opts.Folders {
			f = strings.Trim(f, "/")
			if base, ok := strings.CutSuffix(f, "/**"); ok {
				ors = append(ors, `n.path LIKE ? ESCAPE '\'`)
				args = append(args, likeUnder(base))
				continue
			}
			in := likeUnder(f)
			ors = append(ors, `(n.path LIKE ? ESCAPE '\' AND n.path NOT LIKE ? ESCAPE '\')`)
			args = append(args, in, in+"/%")
		}
		where += " AND (" + strings.Join(ors, " OR ") + ")"
	}
	if len(opts.Paths) > 0 {
		where += " AND n.path IN (?" + strings.Repeat(", ?", len(opts.Paths)-1) + ")"
		for _, p := range opts.Paths {
			args = append(args, p)
		}
	}
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

	if len(opts.ThenBy) >= MaxSortKeys {
		return nil, 0, fmt.Errorf("%w: sort: at most %d keys", ErrBadQuery, MaxSortKeys)
	}
	order, sortArgs := sortSQL(opts.SortKeys())
	limit := opts.Limit
	if limit <= 0 {
		limit = 50
	}
	q := `SELECT n.id, n.path, n.title, n.mtime FROM notes n WHERE 1=1` + where + ` ORDER BY ` + order + ` LIMIT ? OFFSET ?`
	all := append(append(append([]any{}, args...), sortArgs...), limit, max(opts.Offset, 0))
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
	if field == PathField {
		if len(c.Values) == 0 || op != OpIn && len(c.Values) > 1 {
			return "", nil, fmt.Errorf("%w: %s on path takes one note (in takes several)", ErrBadQuery, op)
		}
		args := make([]any, len(c.Values))
		for k, p := range c.Values {
			args[k] = p
		}
		s := `n.path IN (` + placeholders(len(c.Values)) + `)`
		switch op {
		case OpEq, OpIn:
			return s, args, nil
		case OpNe:
			return "NOT " + s, args, nil
		}
		return "", nil, fmt.Errorf("%w: %s on path: a condition on the path takes eq, ne or in", ErrBadQuery, op)
	}
	if c.Link {
		return linkCondSQL(field, op, c.Values)
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

// linkCondSQL renders a link condition (FieldCond.Link): the note links to
// one of paths from field, or from anywhere for the pseudo-field links. ne
// keeps the notes that link to none of them.
func linkCondSQL(field, op string, paths []string) (string, []any, error) {
	if len(paths) == 0 {
		return "", nil, fmt.Errorf("%w: %s on %q needs a note", ErrBadQuery, op, field)
	}
	if op != OpIn && len(paths) > 1 {
		return "", nil, fmt.Errorf("%w: %s on %q takes one note (in takes several)", ErrBadQuery, op, field)
	}
	s := `EXISTS (SELECT 1 FROM links l WHERE l.src_id = n.id AND l.target_path IN (` + placeholders(len(paths)) + `)`
	args := make([]any, 0, len(paths)+1)
	for _, p := range paths {
		args = append(args, p)
	}
	if field != LinksField {
		s += ` AND l.field = ?`
		args = append(args, field)
	}
	s += `)`
	switch op {
	case OpEq, OpIn, OpContains:
		return s, args, nil
	case OpNe:
		return "NOT " + s, args, nil
	}
	return "", nil, fmt.Errorf("%w: %s on %q: a condition on links takes eq, ne, in or contains", ErrBadQuery, op, field)
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

// sortSQL is the ORDER BY of a sort: modified (the default, newest first)
// when keys is empty, then each key in turn and the path last.
func sortSQL(keys []SortKey) (string, []any) {
	if len(keys) == 0 {
		return "n.mtime DESC, n.path", nil
	}
	var terms []string
	var args []any
	for _, k := range keys {
		t, a := sortTerm(k)
		terms = append(terms, t)
		args = append(args, a...)
		if strings.TrimSpace(k.Field) == "path" {
			// The path is unique: nothing after it can order a tie.
			return strings.Join(terms, ", "), args
		}
	}
	return strings.Join(append(terms, "n.path"), ", "), args
}

// sortTerm is the ORDER BY of one key: modified, path, title, or a
// frontmatter field — notes without it last, then by date, number or text.
// With Order, a field sorts by the position of its value in Order instead,
// the values not listed after the listed ones.
func sortTerm(k SortKey) (string, []any) {
	dir := "ASC"
	if k.Desc {
		dir = "DESC"
	}
	field := strings.TrimSpace(k.Field)
	switch field {
	case "", "modified":
		if field == "" {
			dir = "DESC"
		}
		return "n.mtime " + dir, nil
	case "path":
		return "n.path " + dir, nil
	case "title":
		return "n.title COLLATE NOCASE " + dir, nil
	}
	agg := "MIN"
	if k.Desc {
		agg = "MAX"
	}
	col := func(c string) string {
		return "(SELECT " + agg + "(f." + c + ") FROM note_fields f WHERE f.note_id = n.id AND f.key = ?)"
	}
	missing := "(SELECT COUNT(*) FROM note_fields f WHERE f.note_id = n.id AND f.key = ?) = 0"
	if order := k.Order; len(order) > 0 {
		// rank is the position of the value in order, len(order) when it is
		// not listed; unlisted values go after the listed ones either way.
		rank := "CASE f.value" + strings.Repeat(" WHEN ? THEN ?", len(order)) + " ELSE ? END"
		var rankArgs []any
		for i, o := range order {
			rankArgs = append(rankArgs, o, i)
		}
		rankArgs = append(rankArgs, len(order))
		unlisted := "(SELECT MIN(CASE WHEN f.value IN (" + placeholders(len(order)) + ") THEN 0 ELSE 1 END) FROM note_fields f WHERE f.note_id = n.id AND f.key = ?)"
		args := []any{field}
		for _, o := range order {
			args = append(args, o)
		}
		args = append(args, field)
		args = append(args, rankArgs...)
		args = append(args, field, field)
		return missing + ", " + unlisted + ", (SELECT " + agg + "(" + rank + ") FROM note_fields f WHERE f.note_id = n.id AND f.key = ?) " + dir + ", " +
			col("value") + " COLLATE NOCASE " + dir, args
	}
	return missing + ", " + col("date") + " " + dir + ", " + col("num") + " " + dir + ", " +
			col("value") + " COLLATE NOCASE " + dir,
		[]any{field, field, field, field}
}

// SortHits orders hits by keys as Query would, on the values they carry:
// the caller fetched every field the keys name (a rollup's computed value
// among them). Values compare as the ORDER BY of sortTerm does: by date,
// then number, then text without case; a field's lowest values count
// ascending and its highest descending, the notes without it last, and the
// path breaks the ties.
func SortHits(hits []QueryHit, keys []SortKey) {
	if len(keys) == 0 {
		keys = []SortKey{{Field: "modified", Desc: true}}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		for _, k := range keys {
			if c := compareHits(hits[i], hits[j], k); c != 0 {
				return c < 0
			}
		}
		return hits[i].Path < hits[j].Path
	})
}

// compareHits compares a and b on one key, its direction applied.
func compareHits(a, b QueryHit, k SortKey) int {
	sign := 1
	if k.Desc {
		sign = -1
	}
	switch field := strings.TrimSpace(k.Field); field {
	case "", "modified":
		if field == "" {
			sign = -1
		}
		return sign * cmpInt(a.ModTime, b.ModTime)
	case "path":
		return sign * strings.Compare(a.Path, b.Path)
	case "title":
		return sign * compareNoCase(a.Title, b.Title)
	}
	va, vb := a.Fields[k.Field], b.Fields[k.Field]
	switch {
	case len(va) == 0 && len(vb) == 0:
		return 0
	case len(va) == 0:
		return 1
	case len(vb) == 0:
		return -1
	}
	if len(k.Order) > 0 {
		// As in SQL: a note with a listed value first, then by rank.
		ra, rb := rankOf(va, k.Order, k.Desc), rankOf(vb, k.Order, k.Desc)
		la, lb := anyListed(va, k.Order), anyListed(vb, k.Order)
		if la != lb {
			if la {
				return -1
			}
			return 1
		}
		if c := sign * cmpInt(int64(ra), int64(rb)); c != 0 {
			return c
		}
		return sign * compareNoCase(aggText(va, k.Desc), aggText(vb, k.Desc))
	}
	return sign * compareSortVals(sortValsOf(va, k.Desc), sortValsOf(vb, k.Desc))
}

// anyListed says whether one of vs is in order.
func anyListed(vs, order []string) bool {
	return slices.ContainsFunc(vs, func(v string) bool { return slices.Contains(order, v) })
}

// rankOf is the rank of a note's values in order: the lowest ascending, the
// highest descending, len(order) for a value not listed.
func rankOf(vs, order []string, desc bool) int {
	best := -1
	for _, v := range vs {
		r := slices.Index(order, v)
		if r < 0 {
			r = len(order)
		}
		if best < 0 || desc && r > best || !desc && r < best {
			best = r
		}
	}
	return best
}

// sortVals is a note's values for one key as sortTerm's ORDER BY reads
// them: the lowest of each column (the highest, descending), its date, its
// number and its text, nil where no value has one. The Go sort used to read
// one value of the list, and a number by strconv.ParseFloat, which takes
// NaN, Inf and hexadecimals the index never stores as numbers, and dates
// with a space or a "T" apart (IMP-162, S4-15).
type sortVals struct {
	date *string
	num  *float64
	text string
}

func sortValsOf(vs []string, desc bool) sortVals {
	var out sortVals
	better := func(c int) bool { return desc && c > 0 || !desc && c < 0 }
	for k, v := range vs {
		f := typedField("", v, "")
		if d, ok := f.date.(string); ok && (out.date == nil || better(strings.Compare(d, *out.date))) {
			out.date = &d
		}
		if n, ok := f.num.(float64); ok && (out.num == nil || better(cmpFloat(n, *out.num))) {
			out.num = &n
		}
		if k == 0 || better(strings.Compare(v, out.text)) {
			out.text = v
		}
	}
	return out
}

// compareSortVals orders as SQLite does: NULL before any value, then the
// date, the number and the text without (ASCII) case.
func compareSortVals(a, b sortVals) int {
	if c := cmpNullable(a.date, b.date, strings.Compare); c != 0 {
		return c
	}
	if c := cmpNullable(a.num, b.num, cmpFloat); c != 0 {
		return c
	}
	return compareNoCase(a.text, b.text)
}

func cmpNullable[T any](a, b *T, cmp func(T, T) int) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	}
	return cmp(*a, *b)
}

// aggText is MIN (MAX, descending) of the values, as SQLite compares text.
func aggText(vs []string, desc bool) string {
	best := vs[0]
	for _, v := range vs[1:] {
		if c := strings.Compare(v, best); desc && c > 0 || !desc && c < 0 {
			best = v
		}
	}
	return best
}

// compareNoCase is COLLATE NOCASE: only the ASCII letters fold.
func compareNoCase(a, b string) int {
	return strings.Compare(foldASCII(a), foldASCII(b))
}

func foldASCII(s string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	}, s)
}

func cmpFloat(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
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
	// A view's YAML gives int for value: 4, where JSON gives float64: it
	// was refused, and value: 4.5 was not (BUG-114, S4-6).
	case int:
		return strconv.Itoa(x), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case uint64:
		return strconv.FormatUint(x, 10), nil
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

// ClampQueryLimit applies the default to a limit that is not positive and
// the maximum to a larger one.
func ClampQueryLimit(n int) int {
	if n <= 0 {
		return QueryDefaultLimit
	}
	return min(n, QueryMaxLimit)
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
		if c.Link && strings.TrimSpace(c.Field) == LinksField {
			continue // a pseudo-field: no values to show
		}
		add(c.Field)
	}
	for _, f := range SortFields(sort) {
		switch f {
		case "path", "title", "modified":
		default:
			add(f)
		}
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
