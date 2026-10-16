package pipeline

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// The expressions of a pipeline are kept small on purpose (IMP-167): a
// reference to a value, {{ convert.markdown }}, {{ trigger.file }},
// {{ item.path }}, {{ images.0.text }}. Anything more is the job of a
// starlark node, so the pipeline does not grow a second language.

var exprRe = regexp.MustCompile(`\{\{\s*([^{}]*?)\s*\}\}`)

var segRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$|^[0-9]+$`)

// ref is a parsed reference: the root (a node id, trigger, run, item,
// index) and the path inside it.
type ref struct {
	root string
	path []string
}

func (r ref) String() string { return strings.Join(append([]string{r.root}, r.path...), ".") }

func parseRef(src string) (ref, error) {
	src = strings.TrimSpace(src)
	if src == "" {
		return ref{}, fmt.Errorf("an empty {{ }}")
	}
	segs := strings.Split(src, ".")
	for _, s := range segs {
		if !segRe.MatchString(s) {
			return ref{}, fmt.Errorf("{{ %s }}: %q is not a name or an index (only references like node.field.0 are allowed; use a starlark node for more)", src, s)
		}
	}
	if _, err := strconv.Atoi(segs[0]); err == nil {
		return ref{}, fmt.Errorf("{{ %s }}: it must start with a name", src)
	}
	return ref{root: segs[0], path: segs[1:]}, nil
}

// refsIn returns the references in a setting value: strings, and the
// strings nested in lists and maps, their keys too.
func refsIn(v any) ([]ref, error) {
	var out []ref
	var walk func(v any) error
	walk = func(v any) error {
		switch t := v.(type) {
		case string:
			for _, m := range exprRe.FindAllStringSubmatch(t, -1) {
				r, err := parseRef(m[1])
				if err != nil {
					return err
				}
				out = append(out, r)
			}
		case []any:
			for _, e := range t {
				if err := walk(e); err != nil {
					return err
				}
			}
		case map[string]any:
			for k, e := range t {
				if err := walk(k); err != nil {
					return err
				}
				if err := walk(e); err != nil {
					return err
				}
			}
		}
		return nil
	}
	err := walk(v)
	return out, err
}

// scope holds the values an expression may read.
type scope map[string]any

// lookup resolves a reference. A missing field is nil, not an error: an
// optional value of a node is simply absent.
func (sc scope) lookup(r ref) (any, error) {
	v, ok := sc[r.root]
	if !ok {
		return nil, fmt.Errorf("{{ %s }}: nothing is called %q here", r, r.root)
	}
	for _, seg := range r.path {
		switch t := v.(type) {
		case map[string]any:
			v = t[seg]
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil {
				return nil, fmt.Errorf("{{ %s }}: %q is a list, %q is not an index", r, r.String(), seg)
			}
			if i < 0 || i >= len(t) {
				return nil, nil
			}
			v = t[i]
		case nil:
			return nil, nil
		default:
			return nil, fmt.Errorf("{{ %s }}: %q has no field %q", r, r.root, seg)
		}
	}
	return v, nil
}

// eval resolves the expressions in a setting value. A string that is one
// expression and nothing else gives the value as it is (a list, a map, a
// number); otherwise each expression is written into the string.
func (sc scope) eval(v any) (any, error) {
	switch t := v.(type) {
	case string:
		if m := exprRe.FindStringSubmatchIndex(t); m != nil && m[0] == 0 && m[1] == len(t) {
			r, err := parseRef(t[m[2]:m[3]])
			if err != nil {
				return nil, err
			}
			return sc.lookup(r)
		}
		var firstErr error
		out := exprRe.ReplaceAllStringFunc(t, func(s string) string {
			m := exprRe.FindStringSubmatch(s)
			r, err := parseRef(m[1])
			if err == nil {
				var val any
				val, err = sc.lookup(r)
				if err == nil {
					return render(val)
				}
			}
			if firstErr == nil {
				firstErr = err
			}
			return ""
		})
		return out, firstErr
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			ev, err := sc.eval(e)
			if err != nil {
				return nil, err
			}
			out[i] = ev
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(t))
		from := map[string]string{}
		for k, e := range t {
			// A key with expressions is written as a string: a rating keyed
			// by the turn it rates, {"{{ trigger.turn }}": 7}.
			key := k
			if strings.Contains(k, "{{") {
				kv, err := sc.eval(k)
				if err != nil {
					return nil, err
				}
				key = render(kv)
			}
			if prev, ok := from[key]; ok {
				return nil, fmt.Errorf("the keys %q and %q both give %q", prev, k, key)
			}
			from[key] = k
			ev, err := sc.eval(e)
			if err != nil {
				return nil, err
			}
			out[key] = ev
		}
		return out, nil
	default:
		return v, nil
	}
}

// render is a value written into a string.
func render(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool, int, int64:
		return fmt.Sprint(t)
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

// truthy is a when condition's value: false, nil, "", 0 and an empty list
// or map are false.
func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != "" && t != "false"
	case float64:
		return t != 0
	case int:
		return t != 0
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	default:
		return true
	}
}

// decode is a JSON value as the expressions see it.
func decode(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return v
}
