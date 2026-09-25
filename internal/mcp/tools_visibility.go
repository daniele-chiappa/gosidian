package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
)

// projectHidden reports whether the named project has HiddenFromMCP=true in
// the projects flag store. Returns false when the store is unwired (legacy
// behaviour) or the project has no entry (default zero-Flags).
func (s *Server) projectHidden(name string) bool {
	if s == nil || s.projects == nil || name == "" {
		return false
	}
	return s.projects.Get(name).HiddenFromMCP
}

// rejectIfHidden returns an MCP error tool-result when name refers to a
// project explicitly named by the caller and that project is marked
// HiddenFromMCP. Used by tools that accept a project= argument so the agent
// gets a clear signal ("hidden by config") instead of an empty result. nil
// return means the call may proceed.
func (s *Server) rejectIfHidden(name string) *mcp.CallToolResult {
	if !s.projectHidden(name) {
		return nil
	}
	return mcp.NewToolResultErrorf("project %q is hidden from MCP by config", name)
}

// hiddenProjects lists the projects marked HiddenFromMCP, for filters that
// run inside a query (memory_search) rather than on its output.
func (s *Server) hiddenProjects() []string {
	if s == nil || s.projects == nil {
		return nil
	}
	var out []string
	for _, e := range s.projects.All() {
		if e.HiddenFromMCP {
			out = append(out, e.Name)
		}
	}
	return out
}

// pathInHiddenProject returns true when the path belongs to a project marked
// HiddenFromMCP. Used to filter list-style tool outputs without a project
// argument. Reuses the package-level topLevelProject helper.
func (s *Server) pathInHiddenProject(path string) bool {
	return s.projectHidden(topLevelProject(path))
}

// projectVisibility returns who may read the project (public | internal |
// private), or "" when no projects store is wired.
func (s *Server) projectVisibility(name string) string {
	if s == nil || s.projects == nil || name == "" {
		return ""
	}
	return s.projects.Visibility(name)
}
