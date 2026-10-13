// Package plugins runs the nodes of external MCP servers in the pipelines
// (IMP-168, docs/plugins.md). A plugin is registered in config.toml
// ([[pipelines.plugins]]) and serves its manifest through the tool
// gosidian_manifest: its nodes, the MCP tool behind each, the permissions
// it asks for. Its nodes run only once the owner accepted that manifest
// (gosidian plugin accept), and stop when the plugin serves another one
// until it is accepted again.
package plugins

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// ManifestTool is the tool a plugin serves its manifest with.
const ManifestTool = "gosidian_manifest"

// The tools of a plugin's sources (IMP-172 phase 2), each taking the
// source's name in source.
const (
	SourceListTool    = "gosidian_source_list"
	SourceChangesTool = "gosidian_source_changes"
	SourceOpenTool    = "gosidian_source_open"
	SourceMoveTool    = "gosidian_source_move"
)

// GraphTool reads a plugin's graph (IMP-183): the graph's name in graph,
// its arguments in with.
const GraphTool = "gosidian_graph"

// sourceTools are the tools no node may name.
var sourceTools = map[string]bool{ManifestTool: true, SourceListTool: true, SourceChangesTool: true, SourceOpenTool: true, SourceMoveTool: true, GraphTool: true}

// Contract is the version of the manifest format this server reads.
const Contract = 1

// maxNodes caps the nodes of one plugin, maxSources its sources, maxTools
// the tools it offers the agents.
const (
	maxNodes   = 32
	maxSources = 32
	maxTools   = 32
	maxGraphs  = 16
)

// MaxToolName caps the name an agent sees a plugin's tool by,
// <plugin>_<tool>: a client prefixes it (Claude Code with
// mcp__gosidian__), and tool names stop at 64 characters.
const MaxToolName = 48

// The access a plugin's tool needs (IMP-182): read tools are open to every
// token of a project the plugin is exposed in, write tools to those that may
// write there.
const (
	AccessRead  = "read"
	AccessWrite = "write"
)

var (
	pluginName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	nodeName   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
)

// Manifest is what a plugin says about itself.
type Manifest struct {
	// Name is the plugin's name, the one it is registered with: its
	// nodes are <name>.<node>.
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	// Gosidian is the manifest format, Contract.
	Gosidian int        `json:"gosidian"`
	Nodes    []NodeSpec `json:"nodes"`
	// Sources are the remote archives it offers, which the owner binds to
	// a project's IN folder (IMP-172 phase 2).
	Sources []SourceSpec `json:"sources,omitempty"`
	// Tools are the plugin's tools gosidian offers the agents, as
	// <plugin>_<tool>, in the projects the owner exposes the plugin in
	// (IMP-182).
	Tools []ToolSpec `json:"tools,omitempty"`
	// Graphs are the graphs it offers the Graph window and the ```graph
	// blocks, as <plugin>/<graph>, in the projects the owner exposes it in
	// (IMP-183).
	Graphs      []GraphSpec `json:"graphs,omitempty"`
	Permissions Permissions `json:"permissions"`
}

// GraphSpec is a graph a plugin offers: nodes and edges it makes of its
// data (the chunks of an archive and their neighbours, say), read with
// GraphTool.
type GraphSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Input       json.RawMessage `json:"input_schema,omitempty"`
}

// ToolSpec is a tool of the plugin an agent may call through gosidian: the
// arguments go to the plugin's MCP tool Name as they come, but for project,
// which gosidian adds to the schema and hands the plugin in the call's _meta.
type ToolSpec struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Access is read or write: the scope a token needs to call it.
	Access string          `json:"access"`
	Input  json.RawMessage `json:"input_schema,omitempty"`
	Output json.RawMessage `json:"output_schema,omitempty"`
}

// SourceSpec is a source a plugin offers: an archive it reads, set up in
// the plugin with its credentials, which never leave it.
type SourceSpec struct {
	Name        string `json:"name"`
	Kind        string `json:"kind,omitempty"` // webdav, s3, drive…: for the reader
	Description string `json:"description,omitempty"`
	// Changes: the plugin gives the source's changes from a cursor
	// (gosidian_source_changes); without, gosidian lists it in full.
	Changes bool `json:"changes,omitempty"`
	// Move: the plugin moves files inside the source
	// (gosidian_source_move).
	Move bool `json:"move,omitempty"`
}

// NodeSpec is a node a plugin offers.
type NodeSpec struct {
	Name string `json:"name"`
	// Tool is the MCP tool that runs it: its arguments are the node's
	// settings (with), its result the node's output.
	Tool        string `json:"tool"`
	Description string `json:"description,omitempty"`
	// Files are the settings that name a vault file: the plugin gets the
	// file's content ({name, mime, data} in base64) in their place.
	Files []string `json:"files,omitempty"`
	// FileRefs gives the plugin, in place of each file's content, where
	// to fetch it ({name, mime, size, sha256, url}), while the step may
	// run (IMP-172): a file of any size, the IN folder's too.
	FileRefs bool `json:"file_refs,omitempty"`
	// Input and Output are JSON Schemas of the settings and the output,
	// for the reader and the editor.
	Input  json.RawMessage `json:"input_schema,omitempty"`
	Output json.RawMessage `json:"output_schema,omitempty"`
}

// Permissions are what a plugin asks for. Files is enforced by gosidian
// (a plugin sees only the files a pipeline hands it); Network is
// declared, and enforced by whoever runs the plugin's container.
type Permissions struct {
	// Files: the plugin reads the content of the files a pipeline names.
	Files bool `json:"files"`
	// Network lists the hosts the plugin itself calls; empty: none.
	Network []string `json:"network"`
	// SourceHosts are the hosts the URLs of its sources' files may point
	// at, besides the plugin's own: gosidian reads no file from another.
	SourceHosts []string `json:"source_hosts,omitempty"`
}

// ParseManifest reads a manifest and checks it is one plugin name may
// serve.
func ParseManifest(data []byte, name string) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("the manifest is not valid JSON: %w", err)
	}
	if err := m.check(name); err != nil {
		return nil, err
	}
	return &m, nil
}

func (m *Manifest) check(name string) error {
	if m.Gosidian != Contract {
		return fmt.Errorf("the manifest is format %d, this server reads %d", m.Gosidian, Contract)
	}
	if m.Name != name {
		return fmt.Errorf("the plugin calls itself %q, it is registered as %q", m.Name, name)
	}
	if m.Version == "" {
		return errors.New("the manifest has no version")
	}
	if len(m.Nodes) > maxNodes {
		return fmt.Errorf("the manifest has %d nodes, at most %d", len(m.Nodes), maxNodes)
	}
	if len(m.Sources) > maxSources {
		return fmt.Errorf("the manifest has %d sources, at most %d", len(m.Sources), maxSources)
	}
	if len(m.Tools) > maxTools {
		return fmt.Errorf("the manifest has %d tools, at most %d", len(m.Tools), maxTools)
	}
	if len(m.Graphs) > maxGraphs {
		return fmt.Errorf("the manifest has %d graphs, at most %d", len(m.Graphs), maxGraphs)
	}
	if len(m.Nodes) == 0 && len(m.Sources) == 0 && len(m.Tools) == 0 && len(m.Graphs) == 0 {
		return errors.New("the manifest has no nodes, no sources, no tools and no graphs")
	}
	seen := map[string]bool{}
	for _, n := range m.Nodes {
		if !nodeName.MatchString(n.Name) {
			return fmt.Errorf("node %q: a name of lower-case letters, digits and _", n.Name)
		}
		if seen[n.Name] {
			return fmt.Errorf("node %q twice", n.Name)
		}
		seen[n.Name] = true
		if n.Tool == "" || sourceTools[n.Tool] {
			return fmt.Errorf("node %q: tool names the MCP tool that runs it", n.Name)
		}
		if len(n.Files) > 0 && !m.Permissions.Files {
			return fmt.Errorf("node %q takes files, and the manifest does not ask for permissions.files", n.Name)
		}
	}
	seen = map[string]bool{}
	for _, src := range m.Sources {
		if !nodeName.MatchString(src.Name) {
			return fmt.Errorf("source %q: a name of lower-case letters, digits and _", src.Name)
		}
		if seen[src.Name] {
			return fmt.Errorf("source %q twice", src.Name)
		}
		seen[src.Name] = true
	}
	if err := m.checkTools(); err != nil {
		return err
	}
	seen = map[string]bool{}
	for _, g := range m.Graphs {
		switch {
		case !nodeName.MatchString(g.Name):
			return fmt.Errorf("graph %q: a name of lower-case letters, digits and _", g.Name)
		case seen[g.Name]:
			return fmt.Errorf("graph %q twice", g.Name)
		}
		seen[g.Name] = true
		if err := checkObjectSchema(g.Input); err != nil {
			return fmt.Errorf("graph %q: %w", g.Name, err)
		}
	}
	for _, h := range m.Permissions.SourceHosts {
		if !validHost(h) {
			return fmt.Errorf("permissions.source_hosts: %q is not a host (a name or an address, with a port or not)", h)
		}
	}
	return nil
}

// checkTools checks the tools a manifest offers the agents.
func (m *Manifest) checkTools() error {
	// A node's tool that takes files gets their content or a ticket in place
	// of a path: an agent has neither.
	withFiles := map[string]bool{}
	for _, n := range m.Nodes {
		if len(n.Files) > 0 {
			withFiles[n.Tool] = true
		}
	}
	seen := map[string]bool{}
	for _, t := range m.Tools {
		switch {
		case !nodeName.MatchString(t.Name):
			return fmt.Errorf("tool %q: a name of lower-case letters, digits and _", t.Name)
		case seen[t.Name]:
			return fmt.Errorf("tool %q twice", t.Name)
		case strings.HasPrefix(t.Name, "gosidian_"):
			return fmt.Errorf("tool %q: the gosidian_ names are gosidian's", t.Name)
		case withFiles[t.Name]:
			return fmt.Errorf("tool %q runs a node that takes files, which an agent cannot hand it", t.Name)
		case len(m.Name)+1+len(t.Name) > MaxToolName:
			return fmt.Errorf("tool %q: %s_%s is longer than %d characters", t.Name, m.Name, t.Name, MaxToolName)
		case t.Access != AccessRead && t.Access != AccessWrite:
			return fmt.Errorf("tool %q: access is read or write", t.Name)
		}
		seen[t.Name] = true
		if err := checkObjectSchema(t.Input); err != nil {
			return fmt.Errorf("tool %q: %w", t.Name, err)
		}
	}
	return nil
}

// checkObjectSchema checks the input schema of a tool or a graph: an
// object, without project, which gosidian hands the plugin in _meta.
func checkObjectSchema(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var schema struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil || schema.Type != "object" {
		return errors.New("input_schema is a JSON Schema of type object")
	}
	if _, ok := schema.Properties["project"]; ok {
		return fmt.Errorf("project is gosidian's argument, the plugin reads it in _meta (%s)", MetaProject)
	}
	return nil
}

// validHost says whether h is a host, with a port or not: no scheme, no
// path, no credentials.
func validHost(h string) bool {
	if h == "" || strings.ContainsAny(h, "/@?# ") {
		return false
	}
	u, err := url.Parse("http://" + h)
	return err == nil && u.Host == h && u.Hostname() != ""
}

// Source finds a source by name.
func (m *Manifest) Source(name string) (SourceSpec, bool) {
	for _, src := range m.Sources {
		if src.Name == name {
			return src, true
		}
	}
	return SourceSpec{}, false
}

// Graph finds a graph by name.
func (m *Manifest) Graph(name string) (GraphSpec, bool) {
	for _, g := range m.Graphs {
		if g.Name == name {
			return g, true
		}
	}
	return GraphSpec{}, false
}

// Tool finds a tool by name.
func (m *Manifest) Tool(name string) (ToolSpec, bool) {
	for _, t := range m.Tools {
		if t.Name == name {
			return t, true
		}
	}
	return ToolSpec{}, false
}

// Node finds a node by name.
func (m *Manifest) Node(name string) (NodeSpec, bool) {
	for _, n := range m.Nodes {
		if n.Name == name {
			return n, true
		}
	}
	return NodeSpec{}, false
}

// SHA identifies a manifest: the SHA-256 of its JSON as this server reads
// it, so a field it does not know changes nothing.
func (m *Manifest) SHA() string {
	c := *m
	c.Nodes = make([]NodeSpec, len(m.Nodes))
	for i, n := range m.Nodes {
		n.Input, n.Output = compact(n.Input), compact(n.Output)
		c.Nodes[i] = n
	}
	if len(m.Graphs) > 0 {
		c.Graphs = make([]GraphSpec, len(m.Graphs))
		for i, g := range m.Graphs {
			g.Input = compact(g.Input)
			c.Graphs[i] = g
		}
	}
	if len(m.Tools) > 0 {
		c.Tools = make([]ToolSpec, len(m.Tools))
		for i, t := range m.Tools {
			t.Input, t.Output = compact(t.Input), compact(t.Output)
			c.Tools[i] = t
		}
	}
	b, _ := json.Marshal(c)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func compact(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return raw
	}
	b, _ := json.Marshal(v)
	return b
}

// ValidName says whether name may name a plugin.
func ValidName(name string) bool { return pluginName.MatchString(name) }
