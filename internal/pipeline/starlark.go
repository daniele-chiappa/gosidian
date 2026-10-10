package pipeline

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"

	starjson "go.starlark.net/lib/json"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

// The starlark node runs a small script for what the expressions do not
// cover: mapping fields, filtering a list, building a value. Starlark is
// deterministic and has no access to files, network or time unless the
// host gives it (ADR-035): here it gets only its input and the run's
// values, and json for convenience.
//
//   - id: pick
//     use: starlark
//     with:
//       input: "{{ convert.images }}"
//       code: |
//         output = [i for i in input if i["size"] > 1000]

func runStarlark(ctx context.Context, env *Env, with map[string]any) (any, error) {
	code, err := required(with, "code")
	if err != nil {
		return nil, err
	}
	in, err := toStar(with["input"])
	if err != nil {
		return nil, fmt.Errorf("with.input: %w", err)
	}
	all, err := toStar(map[string]any(env.Scope))
	if err != nil {
		return nil, fmt.Errorf("the run's values: %w", err)
	}
	all.Freeze()
	in.Freeze()
	thread := &starlark.Thread{
		Name: env.Run.ID + "/" + env.Node.ID,
		Load: func(*starlark.Thread, string) (starlark.StringDict, error) {
			return nil, errors.New("load is not available in a pipeline")
		},
		Print: func(_ *starlark.Thread, msg string) { env.Log.Info("starlark print", "msg", msg) },
	}
	thread.SetMaxExecutionSteps(env.Engine.cfg.StarlarkSteps)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			thread.Cancel("the node's time is up")
		case <-done:
		}
	}()
	predeclared := starlark.StringDict{"input": in, "ctx": all, "json": starjson.Module}
	// A node's code is a short script, not a module: loops and ifs at the
	// top level, a name set again (md = md.replace(…) in a loop), while
	// and set are allowed. The step cap bounds it all the same.
	opts := &syntax.FileOptions{TopLevelControl: true, GlobalReassign: true, While: true, Set: true}
	globals, err := starlark.ExecFileOptions(opts, thread, env.Node.ID+".star", code, predeclared)
	if err != nil {
		var ee *starlark.EvalError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("starlark: %s", ee.Backtrace())
		}
		return nil, fmt.Errorf("starlark: %w", err)
	}
	out, ok := globals["output"]
	if !ok {
		return nil, errors.New("starlark: the script sets no output (output = …)")
	}
	return fromStar(out)
}

// toStar is a JSON value as a Starlark value.
func toStar(v any) (starlark.Value, error) {
	switch t := v.(type) {
	case nil:
		return starlark.None, nil
	case bool:
		return starlark.Bool(t), nil
	case float64:
		if t == math.Trunc(t) && math.Abs(t) < 1<<53 {
			return starlark.MakeInt64(int64(t)), nil
		}
		return starlark.Float(t), nil
	case int:
		return starlark.MakeInt(t), nil
	case int64:
		return starlark.MakeInt64(t), nil
	case string:
		return starlark.String(t), nil
	case []any:
		out := make([]starlark.Value, len(t))
		for i, e := range t {
			sv, err := toStar(e)
			if err != nil {
				return nil, err
			}
			out[i] = sv
		}
		return starlark.NewList(out), nil
	case map[string]any:
		d := starlark.NewDict(len(t))
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sv, err := toStar(t[k])
			if err != nil {
				return nil, err
			}
			if err := d.SetKey(starlark.String(k), sv); err != nil {
				return nil, err
			}
		}
		return d, nil
	default:
		return nil, fmt.Errorf("a value of type %T", v)
	}
}

// fromStar is a Starlark value as a JSON value.
func fromStar(v starlark.Value) (any, error) {
	switch t := v.(type) {
	case starlark.NoneType:
		return nil, nil
	case starlark.Bool:
		return bool(t), nil
	case starlark.Int:
		if i, ok := t.Int64(); ok {
			return float64(i), nil
		}
		return nil, errors.New("starlark: an integer too large for JSON")
	case starlark.Float:
		return float64(t), nil
	case starlark.String:
		return string(t), nil
	case *starlark.List:
		out := make([]any, t.Len())
		for i := 0; i < t.Len(); i++ {
			e, err := fromStar(t.Index(i))
			if err != nil {
				return nil, err
			}
			out[i] = e
		}
		return out, nil
	case starlark.Tuple:
		out := make([]any, len(t))
		for i, e := range t {
			x, err := fromStar(e)
			if err != nil {
				return nil, err
			}
			out[i] = x
		}
		return out, nil
	case *starlark.Dict:
		out := map[string]any{}
		for _, item := range t.Items() {
			k, ok := item[0].(starlark.String)
			if !ok {
				return nil, fmt.Errorf("starlark: a dict key of type %s, JSON wants strings", item[0].Type())
			}
			x, err := fromStar(item[1])
			if err != nil {
				return nil, err
			}
			out[string(k)] = x
		}
		return out, nil
	default:
		return nil, fmt.Errorf("starlark: a value of type %s cannot be an output", v.Type())
	}
}
