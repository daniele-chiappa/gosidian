package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// The tools of the plugins (IMP-182): a manifest lists the tools it offers
// the agents, the owner exposes the plugin in a project (gosidian plugin
// expose), and gosidian's MCP server offers them as <plugin>_<tool> to the
// tokens of that project, forwarding each call to the plugin with the
// project in the call's _meta.

// PluginTool is a tool of an accepted manifest, as the MCP server offers it.
type PluginTool struct {
	// Name is the name the agents see: <plugin>_<tool>.
	Name   string
	Plugin string
	Spec   ToolSpec
	// SHA is the accepted manifest's.
	SHA string
}

// ToolName is the name the agents see a plugin's tool by. A plugin's name
// has no _, so the first one splits it from the tool's.
func ToolName(plugin, tool string) string { return plugin + "_" + tool }

// Tools lists the tools of the accepted manifests, in the order of the
// config.
func (r *Registry) Tools() []PluginTool {
	acc, _ := r.store.all()
	var out []PluginTool
	for _, name := range r.order {
		a, ok := acc[name]
		if !ok {
			continue
		}
		for _, t := range a.Manifest.Tools {
			out = append(out, PluginTool{Name: ToolName(name, t.Name), Plugin: name, Spec: t, SHA: a.SHA})
		}
	}
	return out
}

// Expose shows a plugin's tools to the agents of a project, and its graphs
// to its views. The plugin's accepted manifest must offer some: the owner
// read them when accepting.
func (r *Registry) Expose(plugin, project, by string) (Exposed, error) {
	if _, ok := r.plugins[plugin]; !ok {
		return Exposed{}, fmt.Errorf("no plugin %q in [[pipelines.plugins]]", plugin)
	}
	acc, err := r.store.all()
	if err != nil {
		return Exposed{}, err
	}
	a, ok := acc[plugin]
	if !ok {
		return Exposed{}, fmt.Errorf("the manifest of plugin %s is not accepted yet (gosidian plugin show %s, then accept)", plugin, plugin)
	}
	if len(a.Manifest.Tools) == 0 && len(a.Manifest.Graphs) == 0 {
		return Exposed{}, fmt.Errorf("plugin %s offers no tools and no graphs", plugin)
	}
	e := Exposed{Plugin: plugin, Project: project, At: time.Now().UTC(), By: by}
	err = r.store.update(func(f *acceptedFile) error {
		for _, x := range f.Exposed {
			if x.Plugin == plugin && x.Project == project {
				return fmt.Errorf("plugin %s is exposed in %s already", plugin, project)
			}
		}
		f.Exposed = append(f.Exposed, e)
		return nil
	})
	return e, err
}

// Unexpose hides a plugin's tools from the agents of a project.
func (r *Registry) Unexpose(plugin, project string) error {
	return r.store.update(func(f *acceptedFile) error {
		for i, x := range f.Exposed {
			if x.Plugin == plugin && x.Project == project {
				f.Exposed = append(f.Exposed[:i], f.Exposed[i+1:]...)
				return nil
			}
		}
		return fmt.Errorf("plugin %s is not exposed in %s", plugin, project)
	})
}

// Exposures lists the exposed plugins, by plugin and project.
func (r *Registry) Exposures() ([]Exposed, error) {
	list, err := r.store.exposures()
	sort.Slice(list, func(i, j int) bool {
		if list[i].Plugin != list[j].Plugin {
			return list[i].Plugin < list[j].Plugin
		}
		return list[i].Project < list[j].Project
	})
	return list, err
}

// ExposedIn lists the projects a plugin is exposed in; a file that cannot
// be read exposes it nowhere.
func (r *Registry) ExposedIn(plugin string) []string {
	list, err := r.store.exposures()
	if err != nil {
		r.log.Warn("plugins: the exposed plugins", "err", err)
		return nil
	}
	var out []string
	for _, e := range list {
		if e.Plugin == plugin {
			out = append(out, e.Project)
		}
	}
	return out
}

// CallTool calls a tool of a plugin's accepted manifest for an agent, with
// meta as the call's _meta, within the plugin's timeout when it has one.
// The result is the plugin's, an error result included.
func (r *Registry) CallTool(ctx context.Context, plugin, tool string, args, meta map[string]any) (*mcp.CallToolResult, error) {
	c, ok := r.plugins[plugin]
	if !ok {
		return nil, fmt.Errorf("no plugin %q in [[pipelines.plugins]]", plugin)
	}
	acc, err := r.store.all()
	if err != nil {
		return nil, err
	}
	a, ok := acc[plugin]
	if !ok {
		return nil, fmt.Errorf("the manifest of plugin %s is not accepted", plugin)
	}
	if _, ok := a.Manifest.Tool(tool); !ok {
		return nil, fmt.Errorf("plugin %s offers no tool %q", plugin, tool)
	}
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	sess, err := r.session(ctx, c, a)
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	res, err := callToolMeta(ctx, sess, tool, args, meta)
	if err != nil {
		return nil, fmt.Errorf("plugin %s, %s: %w", plugin, tool, err)
	}
	return res, nil
}

// maxGraphResult caps the JSON of a plugin's graph.
const maxGraphResult = 8 << 20

// PluginGraph is a graph of an accepted manifest.
type PluginGraph struct {
	// Source is how a view names it: <plugin>/<graph>.
	Source string
	Plugin string
	Spec   GraphSpec
}

// Graphs lists the graphs of the accepted manifests, in the order of the
// config.
func (r *Registry) Graphs() []PluginGraph {
	acc, _ := r.store.all()
	var out []PluginGraph
	for _, name := range r.order {
		a, ok := acc[name]
		if !ok {
			continue
		}
		for _, g := range a.Manifest.Graphs {
			out = append(out, PluginGraph{Source: name + "/" + g.Name, Plugin: name, Spec: g})
		}
	}
	return out
}

// RefusedError is a call the plugin answered with an error of its own: it
// refused the arguments (a collection missing, a number out of range), or
// its store failed. It is no failure to reach the plugin, and its message
// is the plugin's.
type RefusedError struct{ Msg string }

func (e *RefusedError) Error() string { return e.Msg }

// Graph reads a plugin's graph: GraphTool with the graph's name and its
// arguments, meta as the call's _meta. The result is the plugin's JSON; a
// graph the plugin refuses is a *RefusedError.
func (r *Registry) Graph(ctx context.Context, plugin, graph string, args, meta map[string]any) (json.RawMessage, error) {
	c, ok := r.plugins[plugin]
	if !ok {
		return nil, fmt.Errorf("no plugin %q in [[pipelines.plugins]]", plugin)
	}
	acc, err := r.store.all()
	if err != nil {
		return nil, err
	}
	a, ok := acc[plugin]
	if !ok {
		return nil, fmt.Errorf("the manifest of plugin %s is not accepted", plugin)
	}
	if _, ok := a.Manifest.Graph(graph); !ok {
		return nil, fmt.Errorf("plugin %s offers no graph %q", plugin, graph)
	}
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	sess, err := r.session(ctx, c, a)
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	if args == nil {
		args = map[string]any{}
	}
	res, err := callToolMeta(ctx, sess, GraphTool, map[string]any{"graph": graph, "with": args}, meta)
	if err != nil {
		return nil, fmt.Errorf("plugin %s, graph %s: %w", plugin, graph, err)
	}
	if res.IsError {
		return nil, &RefusedError{Msg: fmt.Sprintf("plugin %s, graph %s: %s", plugin, graph, strings.TrimSpace(resultText(res)))}
	}
	raw := ResultJSON(res)
	if len(raw) > maxGraphResult {
		return nil, fmt.Errorf("plugin %s gave the graph %s in %d bytes, more than %d", plugin, graph, len(raw), maxGraphResult)
	}
	return raw, nil
}
