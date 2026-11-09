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
	maxApps    = 8
	maxAltURLs = 4
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
	pluginName   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	nodeName     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	contractName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}/[1-9][0-9]{0,3}$`)
	noteTypeName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
)

// ContractAgentRun is the contract of the agents' nodes (IMP-193): a
// prompt in, text or JSON out, with the usage, the cost and the limits
// every executor reads the same way (docs/plugins.md, Agent nodes).
const ContractAgentRun = "agent-run/1"

// ContractCommandRun is the contract of the command nodes (IMP-194): a
// script or a command on a target, with files and a repository at a ref;
// its exit code, the end of its output and its files back
// (docs/plugins.md, Command nodes).
const ContractCommandRun = "command-run/1"

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
	Graphs []GraphSpec `json:"graphs,omitempty"`
	// Apps are the pages it offers for a window of gosidian's own, framed
	// from the plugin's address (IMP-226).
	Apps        []AppSpec   `json:"apps,omitempty"`
	Permissions Permissions `json:"permissions"`
	// MaxConcurrent is how many calls at once the plugin bears (a model on
	// one GPU: 1), until the server's config says otherwise (IMP-196); 0:
	// no limit.
	MaxConcurrent int `json:"max_concurrent,omitempty"`
}

// AppSpec is a page a plugin offers for a window of gosidian's own
// (IMP-226): the editor of the notes of a type, say. gosidian frames URL,
// with the sandbox it chooses for every app, and talks to the page with
// postMessage under the contract Bridge; the page reaches the vault only
// through that window, with the rights of whoever is logged in.
type AppSpec struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	// URL is the page's address, which the browser loads: absolute, http
	// or https, without credentials, query or fragment.
	URL string `json:"url"`
	// AltURLs are the same page at other addresses, each like URL, for a
	// browser that reaches gosidian another way (the LAN and a tunnel, say):
	// the window opens the one on the host gosidian was opened at, else URL.
	AltURLs []string `json:"alt_urls,omitempty"`
	// NoteType is the type of the notes it opens; one app a type.
	NoteType string `json:"note_type"`
	// Bridge is the contract of its messages, such as layout/2.
	Bridge string `json:"bridge"`
	// Engine is what the page is built on, for the reader.
	Engine string `json:"engine,omitempty"`
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
	// Files are the arguments that name a file of the call's project (a
	// vault path, or a list of them): gosidian hands the plugin, in their
	// place, where to fetch each file (FileRefs, required), and stores the
	// files a write tool gives back as attachments (IMP-184).
	Files    []string `json:"files,omitempty"`
	FileRefs bool     `json:"file_refs,omitempty"`
	// Internal is a tool only gosidian's code calls, for a tool of its own
	// that checks what the call touches: no agent is offered it, wherever
	// the plugin is exposed (AppTool).
	Internal bool `json:"internal,omitempty"`
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
	// Contract names the common contract the node keeps, such as
	// agent-run/1, the agents' (docs/plugins.md, IMP-193): whoever picks
	// an executor knows its settings and its output.
	Contract string `json:"contract,omitempty"`
	// MCP: the node's setting mcp (read or write) gets, for the call, an
	// MCP token of gosidian for the run's project, within
	// permissions.mcp.
	MCP bool `json:"mcp,omitempty"`
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
	// MCP lists the access (read, write) its nodes may ask gosidian's MCP
	// for, with a token that lasts the call: accepting the manifest grants
	// it.
	MCP []string `json:"mcp,omitempty"`
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
	if len(m.Apps) > maxApps {
		return fmt.Errorf("the manifest has %d apps, at most %d", len(m.Apps), maxApps)
	}
	if len(m.Nodes) == 0 && len(m.Sources) == 0 && len(m.Tools) == 0 && len(m.Graphs) == 0 && len(m.Apps) == 0 {
		return errors.New("the manifest has no nodes, no sources, no tools, no graphs and no apps")
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
		if n.Contract != "" && !contractName.MatchString(n.Contract) {
			return fmt.Errorf("node %q: contract %q is a name and a version, such as agent-run/1", n.Name, n.Contract)
		}
		if n.MCP && len(m.Permissions.MCP) == 0 {
			return fmt.Errorf("node %q asks for gosidian's MCP, and the manifest does not ask for permissions.mcp", n.Name)
		}
	}
	for _, a := range m.Permissions.MCP {
		if a != AccessRead && a != AccessWrite {
			return fmt.Errorf("permissions.mcp: %q is neither read nor write", a)
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
	if err := m.checkApps(); err != nil {
		return err
	}
	for _, h := range m.Permissions.SourceHosts {
		if !validHost(h) {
			return fmt.Errorf("permissions.source_hosts: %q is not a host (a name or an address, with a port or not)", h)
		}
	}
	return nil
}

// checkApps checks the apps of a manifest: their names, addresses, note
// types and contracts.
func (m *Manifest) checkApps() error {
	names, types := map[string]bool{}, map[string]bool{}
	for _, a := range m.Apps {
		switch {
		case !nodeName.MatchString(a.Name):
			return fmt.Errorf("app %q: a name of lower-case letters, digits and _", a.Name)
		case names[a.Name]:
			return fmt.Errorf("app %q twice", a.Name)
		case strings.TrimSpace(a.Title) == "":
			return fmt.Errorf("app %q has no title", a.Name)
		case !noteTypeName.MatchString(a.NoteType):
			return fmt.Errorf("app %q: note_type %q is the type of the notes it opens, lower-case letters, digits, _ and -", a.Name, a.NoteType)
		case types[a.NoteType]:
			return fmt.Errorf("app %q: another app opens the notes type: %s", a.Name, a.NoteType)
		case !contractName.MatchString(a.Bridge):
			return fmt.Errorf("app %q: bridge %q is a name and a version, such as layout/1", a.Name, a.Bridge)
		}
		if len(a.AltURLs) > maxAltURLs {
			return fmt.Errorf("app %q has %d other addresses, at most %d", a.Name, len(a.AltURLs), maxAltURLs)
		}
		for _, raw := range a.urls() {
			if _, err := appOrigin(raw); err != nil {
				return fmt.Errorf("app %q: %w", a.Name, err)
			}
		}
		names[a.Name], types[a.NoteType] = true, true
	}
	return nil
}

// urls are the addresses of the app's page, the main one first.
func (a AppSpec) urls() []string {
	return append([]string{a.URL}, a.AltURLs...)
}

// appOrigin is the origin of an app's address, which the SPA's CSP admits
// in frame-src: the address absolute, http or https, without credentials,
// query or fragment, its host a name or an address.
func appOrigin(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
		u.RawQuery != "" || u.Fragment != "" || strings.Contains(raw, "#") || !validHost(u.Host) || strings.ContainsAny(raw, " ;,'\"") {
		return "", fmt.Errorf("url %q: the page's address, https://layout.example.com/app/", raw)
	}
	return u.Scheme + "://" + u.Host, nil
}

// checkTools checks the tools a manifest offers the agents.
func (m *Manifest) checkTools() error {
	// A node's tool that takes files gets their content or a ticket in place
	// of a path: an agent has neither, unless the tool says which of its
	// arguments name files, which gosidian then hands by reference.
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
		case withFiles[t.Name] && len(t.Files) == 0:
			return fmt.Errorf("tool %q runs a node that takes files, which an agent cannot hand it: list them in the tool's files", t.Name)
		case t.Internal && len(t.Files) > 0:
			return fmt.Errorf("tool %q is internal: gosidian gives it what it needs, and it takes no agent's files", t.Name)
		case len(t.Files) > 0 && !t.FileRefs:
			return fmt.Errorf("tool %q takes files: an agent's files go by reference (file_refs: true)", t.Name)
		case len(t.Files) > 0 && !m.Permissions.Files:
			return fmt.Errorf("tool %q takes files, and the manifest does not ask for permissions.files", t.Name)
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

// App finds an app by name.
func (m *Manifest) App(name string) (AppSpec, bool) {
	for _, a := range m.Apps {
		if a.Name == name {
			return a, true
		}
	}
	return AppSpec{}, false
}

// AppOrigins are the origins of the manifest's apps, for the SPA's
// frame-src.
func (m *Manifest) AppOrigins() []string {
	var out []string
	for _, a := range m.Apps {
		for _, raw := range a.urls() {
			if o, err := appOrigin(raw); err == nil {
				out = append(out, o)
			}
		}
	}
	return out
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
