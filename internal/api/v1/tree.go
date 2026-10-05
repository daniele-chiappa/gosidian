package v1

import (
	"net/http"
	"sort"
	"strings"

	"github.com/gosidian/gosidian/internal/vault"
)

// apiTreeNode is the JSON projection of the sidebar tree shipped to
// the SPA. Renamed from the server.treeNode shape to keep the wire
// contract independent: HTMX templates and Vue components have
// different needs (e.g. the SPA carries `kind` for component lookup
// while the HTML side rendered icon names directly), and freezing the
// JSON keys here lets us evolve the HTML side without breaking the
// SPA contract.
type apiTreeNode struct {
	Name          string         `json:"name"`
	Path          string         `json:"path"`
	IsDir         bool           `json:"is_dir"`
	IsProjectRoot bool           `json:"is_project_root,omitempty"`
	Kind          string         `json:"kind"`
	NoteCount     int            `json:"note_count,omitempty"`
	InProgress    bool           `json:"in_progress,omitempty"`
	HiddenFromMCP bool           `json:"hidden_from_mcp,omitempty"`
	SkipGitSync   bool           `json:"skip_git_sync,omitempty"`
	Children      []*apiTreeNode `json:"children,omitempty"`
}

// handleTree returns the sidebar tree as JSON. Filters: ?project=X
// scopes to a top-level dir; the response is the corresponding
// subtree (still rooted on the project node so the SPA can render
// breadcrumbs cleanly).
//
// HiddenFromMCP is NOT filtered here — it's an MCP-listing concern,
// orthogonal to web visibility; the flag is surfaced on the node so the
// SPA can render an icon hint. Guest-role principals, however, see only
// notes in projects flagged Public (authz.CanAccessProject) — that filter
// is applied while collecting paths below.
func (r *Router) handleTree(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	if r.deps.Index == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "index not configured")
		return
	}
	project := strings.TrimSpace(req.URL.Query().Get("project"))

	// Source of truth: the index lists every note path. Building from
	// the index avoids a filesystem walk and respects deletions
	// already reflected there.
	rows, err := r.deps.Index.AllNotes()
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	p := principalFromContext(req)
	paths := make([]string, 0, len(rows))
	for _, n := range rows {
		if project != "" && !strings.HasPrefix(n.Path, project+"/") && n.Path != project {
			continue
		}
		// Per-role visibility: guests drop to public-only; owner/member keep all.
		if !r.canSee(p, n.Path) {
			continue
		}
		paths = append(paths, n.Path)
	}
	// Obsidian bases and canvases are no notes, so the index does not hold
	// them: they are read off the vault, read-only (IMP-118, IMP-144).
	if r.deps.Vault != nil {
		bases, _ := r.deps.Vault.ListBases(project)
		canvases, _ := r.deps.Vault.ListCanvases(project)
		for _, b := range append(bases, canvases...) {
			if r.canSee(p, b) {
				paths = append(paths, b)
			}
		}
	}

	// In-progress flag: notes tagged status:in-progress get a badge in
	// the UI. One round-trip to the tag index covers everything visible.
	inProgress := map[string]bool{}
	if rows, err := r.deps.Index.NotesByTag("status:in-progress"); err == nil {
		for _, n := range rows {
			inProgress[n.Path] = true
		}
	}

	root := buildAPITree(paths, inProgress)
	r.annotateProjectFlags(root)
	computeAPINoteCount(root)
	WriteJSON(w, http.StatusOK, map[string]any{"root": root})
}

// buildAPITree assembles the tree from a flat list of slash-separated
// paths. Mirrors the server-side buildTree but returns the
// SPA-specific shape with explicit JSON tags.
func buildAPITree(paths []string, inProgress map[string]bool) *apiTreeNode {
	root := &apiTreeNode{Name: "", Path: "", IsDir: true, Kind: "folder"}
	for _, p := range paths {
		parts := strings.Split(p, "/")
		cur := root
		for i, part := range parts {
			isLast := i == len(parts)-1
			var child *apiTreeNode
			for _, c := range cur.Children {
				if c.Name == part && c.IsDir == !isLast {
					child = c
					break
				}
			}
			if child == nil {
				child = &apiTreeNode{
					Name:          part,
					Path:          strings.Join(parts[:i+1], "/"),
					IsDir:         !isLast,
					IsProjectRoot: !isLast && i == 0,
				}
				if child.IsDir {
					child.Kind = "folder"
				} else {
					child.Kind = classifyAPIKind(child.Path)
					if inProgress[child.Path] {
						child.InProgress = true
					}
				}
				cur.Children = append(cur.Children, child)
			}
			cur = child
		}
	}
	sortAPITree(root)
	return root
}

// classifyAPIKind mirrors the server's classifyNoteKind so the SPA
// receives the same kind values the HTML view used. Extracted into
// its own function because importing internal/server from internal/api
// would fight the package layering (api is a peer of server, not a
// consumer).
func classifyAPIKind(path string) string {
	if vault.IsBaseFile(path) {
		return "base"
	}
	if vault.IsCanvasFile(path) {
		return "canvas"
	}
	lower := strings.ToLower(path)
	base := path
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	baseLower := strings.ToLower(base)
	if baseLower == "readme.md" || baseLower == "hot.md" || baseLower == "log.md" {
		return "index"
	}
	parts := strings.Split(lower, "/")
	for _, seg := range parts[:len(parts)-1] {
		switch seg {
		case "plans":
			return "plan"
		case "skills":
			return "skill"
		case "memory":
			return "memory"
		case "agents":
			return "agent"
		case "docs":
			return "doc"
		}
	}
	return "note"
}

func sortAPITree(n *apiTreeNode) {
	sort.Slice(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		return a.Name < b.Name
	})
	for _, c := range n.Children {
		sortAPITree(c)
	}
}

func computeAPINoteCount(n *apiTreeNode) int {
	if !n.IsDir {
		return 1
	}
	total := 0
	for _, c := range n.Children {
		total += computeAPINoteCount(c)
	}
	n.NoteCount = total
	return total
}

// annotateProjectFlags sets HiddenFromMCP/SkipGitSync on the
// project-root nodes from the per-project flag store. The flags don't
// recurse into children — they're a project-level setting, and the
// SPA shows them as a single badge on the project header.
func (r *Router) annotateProjectFlags(root *apiTreeNode) {
	if r.deps.Projects == nil {
		return
	}
	for _, c := range root.Children {
		if !c.IsProjectRoot {
			continue
		}
		flags := r.deps.Projects.Get(c.Name)
		c.HiddenFromMCP = flags.HiddenFromMCP
		c.SkipGitSync = flags.SkipGitSync
	}
}
