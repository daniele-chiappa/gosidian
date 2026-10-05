package views

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Obsidian bases (IMP-118, IMP-127 iteration 3, phase 5): a .base file is
// the YAML of views over the vault's notes that Obsidian reads. gosidian
// shows one read-only, as a note: each of its views translated into a
// ```view block, computed like any other, with what has no equivalent said
// in a warning above it rather than guessed.

// rawBase is the YAML shape of a .base file; what is not read here (a
// view's column sizes, its card images) only presents the view.
type rawBase struct {
	Filters    any                       `yaml:"filters"`
	Properties map[string]map[string]any `yaml:"properties"`
	Summaries  map[string]any            `yaml:"summaries"`
	Views      []rawBaseView             `yaml:"views"`
}

type rawBaseView struct {
	Type      string         `yaml:"type"`
	Name      string         `yaml:"name"`
	Filters   any            `yaml:"filters"`
	Order     []string       `yaml:"order"`
	Sort      []baseSortKey  `yaml:"sort"`
	GroupBy   *baseSortKey   `yaml:"groupBy"`
	Limit     int            `yaml:"limit"`
	Summaries map[string]any `yaml:"summaries"`
}

type baseSortKey struct {
	Property  string `yaml:"property"`
	Direction string `yaml:"direction"`
}

// baseSpec is the view block a view of a base becomes, in the order a
// person writes it.
type baseSpec struct {
	From    any      `yaml:"from"`
	Where   []any    `yaml:"where,omitempty"`
	Sort    string   `yaml:"sort,omitempty"`
	Columns []string `yaml:"columns,omitempty"`
	Limit   int      `yaml:"limit,omitempty"`
	As      string   `yaml:"as,omitempty"`
}

type baseCond struct {
	Field string `yaml:"field"`
	Op    string `yaml:"op"`
	Value any    `yaml:"value"`
}

// BaseView is one view of a base, translated.
type BaseView struct {
	Name     string
	Spec     string // the YAML of its view block
	Warnings []string
}

// Base is a .base file translated: its views, and what of the file as a
// whole has no equivalent.
type Base struct {
	Views    []BaseView
	Warnings []string
}

// TranslateBase reads the .base file at rel. Its notes are those of rel's
// project: a folder in file.inFolder is read inside it, as the project is
// the vault the base was written for.
func TranslateBase(rel string, src []byte) (*Base, error) {
	var raw rawBase
	if err := yaml.Unmarshal(src, &raw); err != nil {
		return nil, fmt.Errorf("not valid YAML: %w", err)
	}
	project, _, _ := strings.Cut(rel, "/")
	b := &Base{}
	for _, p := range raw.Properties {
		if _, ok := p["displayName"]; ok {
			b.Warnings = append(b.Warnings, "display names have no equivalent: columns are named by their property")
			break
		}
	}
	if len(raw.Summaries) > 0 {
		b.Warnings = append(b.Warnings, "summaries have no equivalent: they are left out")
	}
	views := raw.Views
	if len(views) == 0 {
		views = []rawBaseView{{Type: "table", Name: "Table"}}
	}
	for i, v := range views {
		bv := translateBaseView(project, raw.Filters, v)
		if bv.Name == "" {
			bv.Name = fmt.Sprintf("View %d", i+1)
		}
		b.Views = append(b.Views, bv)
	}
	return b, nil
}

func translateBaseView(project string, global any, v rawBaseView) BaseView {
	bv := BaseView{Name: strings.TrimSpace(v.Name)}
	warn := func(format string, a ...any) { bv.Warnings = append(bv.Warnings, fmt.Sprintf(format, a...)) }

	var fs filterSet
	fs.project = project
	fs.add(global)
	fs.add(v.Filters)
	bv.Warnings = append(bv.Warnings, fs.warnings...)

	spec := baseSpec{}
	switch len(fs.folders) {
	case 0:
		spec.From = project
	case 1:
		spec.From = fs.folders[0]
	default:
		spec.From = fs.folders
	}
	for _, c := range fs.conds {
		spec.Where = append(spec.Where, c.yaml())
	}

	switch t := strings.TrimSpace(v.Type); t {
	case "", "table":
	case "list":
		spec.As = "list"
	default:
		warn("a %s view has no equivalent: shown as a table", t)
	}
	if v.GroupBy != nil && strings.TrimSpace(v.GroupBy.Property) != "" {
		warn("grouping by %s has no equivalent in a table: shown ungrouped", v.GroupBy.Property)
	}
	if len(v.Summaries) > 0 {
		warn("summaries have no equivalent: they are left out")
	}

	if len(v.Sort) > 0 {
		k := v.Sort[0]
		if f, ok := baseColumn(k.Property); ok {
			spec.Sort = f + " " + strings.ToLower(strings.TrimSpace(k.Direction))
			spec.Sort = strings.TrimSpace(spec.Sort)
		} else {
			warn("sorting by %s has no equivalent: the view keeps its default order", k.Property)
		}
		if len(v.Sort) > 1 {
			var rest []string
			for _, k := range v.Sort[1:] {
				rest = append(rest, k.Property)
			}
			warn("only the first sort key is kept: %s ignored", strings.Join(rest, ", "))
		}
	}

	seen := map[string]bool{}
	for _, p := range v.Order {
		f, ok := baseColumn(p)
		if !ok {
			warn("the column %s has no equivalent: it is left out", p)
			continue
		}
		if !seen[f] {
			seen[f] = true
			spec.Columns = append(spec.Columns, f)
		}
	}
	if len(spec.Columns) > 0 && !seen["title"] {
		// The title is the link to the note: a table without it leads
		// nowhere.
		spec.Columns = append([]string{"title"}, spec.Columns...)
	}
	if spec.As == "list" {
		spec.Columns = nil
	}
	if v.Limit > 0 {
		spec.Limit = v.Limit
	}

	out, err := yaml.Marshal(spec)
	if err != nil {
		warn("the view could not be written: %v", err)
	}
	bv.Spec = string(out)
	return bv
}

// baseColumn names in gosidian the property p of a base: note.x and x are
// the frontmatter field x; of the file's own properties, those with an
// equivalent. A formula has none.
func baseColumn(p string) (string, bool) {
	p = strings.TrimSpace(p)
	switch p {
	case "file.name", "file.basename":
		return "title", true
	case "file.path":
		return "path", true
	case "file.mtime":
		return "modified", true
	case "file.ctime":
		return "created_at", true
	case "file.tags":
		return "tags", true
	}
	if f, ok := strings.CutPrefix(p, "note."); ok && baseFieldRe.MatchString(f) {
		return f, true
	}
	if baseFieldRe.MatchString(p) && !strings.Contains(p, ".") {
		return p, true
	}
	return "", false
}

var baseFieldRe = regexp.MustCompile(`^[A-Za-z_][\w-]*$`)

// condition is a condition of a view, as filterSet builds it.
type condition struct {
	field  string
	op     string // eq ne lt lte gt gte in contains exists
	values []string
	link   bool // values are link targets, "this" or [[note]]
}

var opSymbols = map[string]string{"eq": "=", "ne": "!=", "lt": "<", "lte": "<=", "gt": ">", "gte": ">=", "in": "in", "contains": "contains"}

// yaml is the condition as a view's where writes it: "field op value"
// when that reads back the same, a {field, op, value} map otherwise.
func (c condition) yaml() any {
	if c.op == "exists" {
		if len(c.values) > 0 && c.values[0] == "false" {
			return c.field + " !exists"
		}
		return c.field + " exists"
	}
	if c.link {
		v := c.values[0]
		if v != "this" {
			v = "[[" + v + "]]"
		}
		return c.field + " " + opSymbols[c.op] + " " + v
	}
	plain := true
	for _, v := range c.values {
		if v == "" || strings.ContainsAny(v, `,[]"'{}#`) || strings.TrimSpace(v) != v {
			plain = false
		}
	}
	if !plain {
		var value any = c.values[0]
		if c.op == "in" {
			value = c.values
		}
		return baseCond{Field: c.field, Op: c.op, Value: value}
	}
	v := c.values[0]
	if c.op == "in" {
		v = "[" + strings.Join(c.values, ", ") + "]"
	}
	return c.field + " " + opSymbols[c.op] + " " + v
}

// negate is the condition that holds where c does not, when a view can
// say it.
func (c condition) negate() (condition, bool) {
	flip := map[string]string{"eq": "ne", "ne": "eq", "lt": "gte", "gte": "lt", "gt": "lte", "lte": "gt"}
	switch {
	case c.op == "exists":
		if len(c.values) > 0 && c.values[0] == "false" {
			c.values = []string{"true"}
		} else {
			c.values = []string{"false"}
		}
		return c, true
	case flip[c.op] != "" && len(c.values) == 1 && !c.link:
		c.op = flip[c.op]
		return c, true
	}
	return c, false
}

// atom is one filter of a base translated: a folder, a condition, or
// nothing to say (file.ext == "md", always true of a note).
type atom struct {
	folder string
	cond   *condition
	always bool
}

// filterSet collects the filters of a base and of a view: they all hold
// (and), the folders make the view's from.
type filterSet struct {
	project  string
	folders  []string
	conds    []condition
	warnings []string
}

func (fs *filterSet) warn(format string, a ...any) {
	fs.warnings = append(fs.warnings, fmt.Sprintf(format, a...))
}

func (fs *filterSet) unsupported(what string) {
	fs.warn("the filter %s has no equivalent: it is ignored, so the view may list notes Obsidian leaves out", what)
}

// add adds the filters of a filters: value, a string, a list (all of them)
// or an and, or, not map.
func (fs *filterSet) add(f any) {
	switch t := f.(type) {
	case nil:
	case string:
		fs.addExpr(t)
	case []any:
		for _, e := range t {
			fs.add(e)
		}
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			v := t[k]
			items, _ := v.([]any)
			if items == nil && v != nil {
				items = []any{v}
			}
			switch k {
			case "and":
				for _, e := range items {
					fs.add(e)
				}
			case "or":
				fs.addOr(items)
			case "not":
				for _, e := range items {
					fs.addNot(e)
				}
			default:
				fs.unsupported(fmt.Sprintf("%q", k))
			}
		}
	default:
		fs.unsupported(fmt.Sprint(t))
	}
}

func (fs *filterSet) addAtom(a atom) {
	switch {
	case a.always:
	case a.folder != "":
		fs.addFolder(a.folder)
	case a.cond != nil:
		fs.conds = append(fs.conds, *a.cond)
	}
}

// addFolder narrows the view to folder: within a folder already set, the
// deeper of the two.
func (fs *filterSet) addFolder(folder string) {
	if len(fs.folders) == 0 {
		fs.folders = []string{folder}
		return
	}
	for i, f := range fs.folders {
		switch {
		case folder == f || strings.HasPrefix(f, folder+"/"):
			return
		case strings.HasPrefix(folder, f+"/"):
			fs.folders[i] = folder
			return
		}
	}
	fs.unsupported(fmt.Sprintf("file.inFolder(%q) with another folder", folder))
}

func (fs *filterSet) addExpr(expr string) {
	expr = strings.TrimSpace(expr)
	if parts := splitTop(expr, "||"); len(parts) > 1 {
		items := make([]any, len(parts))
		for i, p := range parts {
			items[i] = p
		}
		fs.addOr(items)
		return
	}
	if parts := splitTop(expr, "&&"); len(parts) > 1 {
		for _, p := range parts {
			fs.addExpr(p)
		}
		return
	}
	a, ok := fs.parseAtom(expr)
	if !ok {
		fs.unsupported(fmt.Sprintf("%q", expr))
		return
	}
	fs.addAtom(a)
}

// addNot adds the filter f negated: a condition a view can negate, or a
// warning.
func (fs *filterSet) addNot(f any) {
	expr, ok := f.(string)
	if !ok {
		fs.unsupported(fmt.Sprintf("not: %v", f))
		return
	}
	a, ok := fs.parseAtom(strings.TrimSpace(expr))
	if !ok || a.cond == nil {
		fs.unsupported(fmt.Sprintf("not: %q", expr))
		return
	}
	n, ok := a.cond.negate()
	if !ok {
		fs.unsupported(fmt.Sprintf("not: %q", expr))
		return
	}
	fs.conds = append(fs.conds, n)
}

// addOr adds an or of filters when a view can say it: folders (from lists
// them all), or the same field equal to one of some values (in).
func (fs *filterSet) addOr(items []any) {
	var atoms []atom
	var src []string
	for _, it := range items {
		expr, ok := it.(string)
		if !ok {
			fs.unsupported(fmt.Sprintf("or: %v", items))
			return
		}
		a, ok := fs.parseAtom(strings.TrimSpace(expr))
		if !ok {
			fs.unsupported(fmt.Sprintf("or: %q", expr))
			return
		}
		atoms = append(atoms, a)
		src = append(src, expr)
	}
	if len(atoms) == 0 {
		return
	}
	folders := true
	for _, a := range atoms {
		folders = folders && a.folder != ""
	}
	if folders {
		if len(fs.folders) > 0 {
			fs.unsupported(fmt.Sprintf("or: %s with another folder", strings.Join(src, ", ")))
			return
		}
		for _, a := range atoms {
			fs.folders = append(fs.folders, a.folder)
		}
		return
	}
	in := condition{op: "in"}
	for _, a := range atoms {
		c := a.cond
		if c == nil || c.link || (c.op != "eq" && c.op != "in") || (in.field != "" && c.field != in.field) {
			fs.unsupported(fmt.Sprintf("or: %s", strings.Join(src, ", ")))
			return
		}
		in.field = c.field
		in.values = append(in.values, c.values...)
	}
	if len(in.values) == 1 {
		in.op = "eq"
	}
	fs.conds = append(fs.conds, in)
}

var (
	baseCallRe = regexp.MustCompile(`^([A-Za-z_][\w.-]*)\.(\w+)\((.*)\)$`)
	baseCmpRe  = regexp.MustCompile(`^([A-Za-z_][\w.-]*)\s*(==|!=|>=|<=|>|<)\s*(.+)$`)
	baseCmpOps = map[string]string{"==": "eq", "!=": "ne", ">": "gt", ">=": "gte", "<": "lt", "<=": "lte"}
)

// parseAtom reads one filter of a base: a function of the file
// (file.hasTag, file.inFolder, file.hasLink, file.hasProperty), a method of
// a property (contains, isEmpty), or a comparison of a property with a
// literal, negated by a leading !. What it does not read has no
// equivalent.
func (fs *filterSet) parseAtom(expr string) (atom, bool) {
	expr = strings.TrimSpace(expr)
	for len(expr) > 1 && expr[0] == '(' && expr[len(expr)-1] == ')' && closes(expr) {
		expr = strings.TrimSpace(expr[1 : len(expr)-1])
	}
	if rest, ok := strings.CutPrefix(expr, "!"); ok && !strings.HasPrefix(rest, "=") {
		a, ok := fs.parseAtom(rest)
		if !ok || a.cond == nil {
			return atom{}, false
		}
		n, ok := a.cond.negate()
		if !ok {
			return atom{}, false
		}
		return atom{cond: &n}, true
	}
	if m := baseCallRe.FindStringSubmatch(expr); m != nil {
		args, ok := literalArgs(m[3])
		if !ok {
			return atom{}, false
		}
		return fs.call(m[1], m[2], args)
	}
	if m := baseCmpRe.FindStringSubmatch(expr); m != nil {
		v, ok := literal(m[3])
		if !ok {
			return atom{}, false
		}
		op := baseCmpOps[m[2]]
		if m[1] == "file.ext" && op == "eq" && strings.EqualFold(v, "md") {
			return atom{always: true}, true
		}
		f, ok := baseColumn(m[1])
		if !ok || f == "title" || f == "modified" || f == "created_at" {
			return atom{}, false
		}
		if f == "path" && op != "eq" && op != "ne" {
			return atom{}, false
		}
		return atom{cond: &condition{field: f, op: op, values: []string{v}}}, true
	}
	return atom{}, false
}

// call reads recv.method(args).
func (fs *filterSet) call(recv, method string, args []string) (atom, bool) {
	if recv == "file" {
		switch method {
		case "inFolder":
			if len(args) != 1 || args[0] == "this" {
				return atom{}, false
			}
			return atom{folder: fs.folder(args[0])}, true
		case "hasTag":
			if len(args) == 0 {
				return atom{}, false
			}
			var tags []string
			for _, a := range args {
				if a == "this" {
					return atom{}, false
				}
				tags = append(tags, strings.TrimPrefix(a, "#"))
			}
			op := "eq"
			if len(tags) > 1 {
				op = "in"
			}
			return atom{cond: &condition{field: "tags", op: op, values: tags}}, true
		case "hasLink":
			if len(args) != 1 {
				return atom{}, false
			}
			if args[0] == "this" {
				fs.warn("file.hasLink(this) names the base itself, and gosidian does not follow links to a base: the view lists nothing")
			}
			return atom{cond: &condition{field: "links", op: "contains", values: []string{strings.TrimSuffix(args[0], ".md")}, link: true}}, true
		case "hasProperty":
			if len(args) != 1 || !baseFieldRe.MatchString(args[0]) {
				return atom{}, false
			}
			return atom{cond: &condition{field: args[0], op: "exists", values: []string{"true"}}}, true
		}
		return atom{}, false
	}
	f, ok := baseColumn(recv)
	if !ok || f == "title" || f == "path" || f == "modified" || f == "created_at" {
		return atom{}, false
	}
	switch method {
	case "contains", "containsAny":
		if len(args) != 1 || args[0] == "this" || args[0] == "" {
			return atom{}, false
		}
		if f == "tags" {
			return atom{cond: &condition{field: f, op: "eq", values: []string{strings.TrimPrefix(args[0], "#")}}}, true
		}
		return atom{cond: &condition{field: f, op: "contains", values: args}}, true
	case "isEmpty":
		if len(args) != 0 {
			return atom{}, false
		}
		return atom{cond: &condition{field: f, op: "exists", values: []string{"false"}}}, true
	}
	return atom{}, false
}

// folder is a folder of the base's vault inside its project.
func (fs *filterSet) folder(f string) string {
	f = strings.Trim(strings.TrimSpace(f), "/")
	switch {
	case f == "" || f == fs.project:
		return fs.project
	case strings.HasPrefix(f, fs.project+"/"):
		return f
	}
	return fs.project + "/" + f
}

// literalArgs reads the arguments of a call: string literals, or this
// (this, this.file: the note the base is read as).
func literalArgs(s string) ([]string, bool) {
	var out []string
	for _, p := range splitTop(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if p == "this" || p == "this.file" {
			out = append(out, "this")
			continue
		}
		v, ok := literal(p)
		if !ok {
			return nil, false
		}
		out = append(out, v)
	}
	return out, true
}

// literal reads a quoted string, a number or a boolean.
func literal(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		if s[0] == '"' {
			if u, err := strconv.Unquote(s); err == nil {
				return u, true
			}
		}
		inner := s[1 : len(s)-1]
		if strings.ContainsRune(inner, rune(s[0])) {
			return "", false
		}
		return inner, true
	}
	if s == "true" || s == "false" {
		return s, true
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return s, true
	}
	return "", false
}

// splitTop splits s on sep where sep is outside quotes and parentheses.
func splitTop(s, sep string) []string {
	var out []string
	depth, start := 0, 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '(':
			depth++
		case c == ')':
			depth--
		case depth == 0 && strings.HasPrefix(s[i:], sep):
			out = append(out, s[start:i])
			start = i + len(sep)
			i += len(sep) - 1
		}
	}
	return append(out, s[start:])
}

// closes reports whether the parenthesis s opens with is the one it ends
// with: (a) && (b) is not wrapped.
func closes(s string) bool {
	depth := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '(':
			depth++
		case c == ')':
			depth--
			if depth == 0 && i < len(s)-1 {
				return false
			}
		}
	}
	return true
}

// BaseMarkdown is the note gosidian shows for the .base file at rel: its
// name, a line that says what it is, then each of its views under its
// name, with its warnings and its view block. A file that does not parse
// shows why, with its text.
func BaseMarkdown(rel string, src []byte) []byte {
	name := strings.TrimSuffix(path.Base(rel), path.Ext(rel))
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", name)
	fmt.Fprintf(&b, "> Obsidian base `%s`, read-only: each of its views is shown as a gosidian view.\n\n", rel)
	base, err := TranslateBase(rel, src)
	if err != nil {
		fmt.Fprintf(&b, "⚠️ base: %s\n\n````yaml\n%s\n````\n", strings.ReplaceAll(err.Error(), "\n", " "), strings.TrimRight(string(src), "\n"))
		return []byte(b.String())
	}
	for _, w := range base.Warnings {
		fmt.Fprintf(&b, "- ⚠️ %s\n", w)
	}
	if len(base.Warnings) > 0 {
		b.WriteString("\n")
	}
	for _, v := range base.Views {
		fmt.Fprintf(&b, "## %s\n\n", strings.ReplaceAll(v.Name, "\n", " "))
		for _, w := range v.Warnings {
			fmt.Fprintf(&b, "- ⚠️ %s\n", w)
		}
		if len(v.Warnings) > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "```view\n%s```\n\n", v.Spec)
	}
	return []byte(strings.TrimRight(b.String(), "\n") + "\n")
}
