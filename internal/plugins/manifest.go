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
	"regexp"
)

// ManifestTool is the tool a plugin serves its manifest with.
const ManifestTool = "gosidian_manifest"

// Contract is the version of the manifest format this server reads.
const Contract = 1

// maxNodes caps the nodes of one plugin.
const maxNodes = 32

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
	Gosidian    int         `json:"gosidian"`
	Nodes       []NodeSpec  `json:"nodes"`
	Permissions Permissions `json:"permissions"`
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
	if len(m.Nodes) == 0 || len(m.Nodes) > maxNodes {
		return fmt.Errorf("the manifest has %d nodes, 1 to %d", len(m.Nodes), maxNodes)
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
		if n.Tool == "" || n.Tool == ManifestTool {
			return fmt.Errorf("node %q: tool names the MCP tool that runs it", n.Name)
		}
		if len(n.Files) > 0 && !m.Permissions.Files {
			return fmt.Errorf("node %q takes files, and the manifest does not ask for permissions.files", n.Name)
		}
	}
	return nil
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
