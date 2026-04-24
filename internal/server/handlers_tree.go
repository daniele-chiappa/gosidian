package server

import (
	"net/http"
	"sort"
	"strings"
)

type treeNode struct {
	Name       string
	Path       string
	IsDir      bool
	Kind       string // folder, plan, skill, memory, agent, doc, index, note
	InProgress bool
	NoteCount  int
	Children   []*treeNode
}

func (s *Server) handleTree(w http.ResponseWriter, r *http.Request) {
	paths, err := s.vault.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Pre-build a set of paths that have the status:in-progress tag so we can
	// flag them in the tree with a small pulsing badge. One query, O(1) lookup.
	inProgress := make(map[string]bool)
	if s.index != nil {
		if rows, err := s.index.NotesByTag("status:in-progress"); err == nil {
			for _, n := range rows {
				inProgress[n.Path] = true
			}
		}
	}

	root := buildTree(paths, inProgress)
	computeNoteCount(root)
	// Inject i18n helper + Lang so the tree.html template can translate its
	// filter placeholder and tree-btn tooltips (IMP: sidebar partial used to
	// bypass injectI18n, leaving `.T` undefined).
	data := s.injectI18n(r, map[string]any{"Root": root})
	s.renderPartial(w, "tree.html", data)
}

func (s *Server) handleBacklinks(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" {
		http.Error(w, "missing path", http.StatusBadRequest)
		return
	}
	bl, err := s.index.Backlinks(p)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.renderPartial(w, "backlinks.html", map[string]any{"Backlinks": bl})
}

func buildTree(paths []string, inProgress map[string]bool) *treeNode {
	root := &treeNode{Name: "", Path: "", IsDir: true, Kind: "folder"}
	for _, p := range paths {
		parts := strings.Split(p, "/")
		cur := root
		for i, part := range parts {
			isLast := i == len(parts)-1
			var child *treeNode
			for _, c := range cur.Children {
				if c.Name == part && c.IsDir == !isLast {
					child = c
					break
				}
			}
			if child == nil {
				child = &treeNode{
					Name:  part,
					Path:  strings.Join(parts[:i+1], "/"),
					IsDir: !isLast,
				}
				if child.IsDir {
					child.Kind = "folder"
				} else {
					child.Kind = classifyNoteKind(child.Path)
					if inProgress[child.Path] {
						child.InProgress = true
					}
				}
				cur.Children = append(cur.Children, child)
			}
			cur = child
		}
	}
	sortTree(root)
	return root
}

// classifyNoteKind returns a logical kind based on the path convention used
// by the gosidian bootstrap pattern. Projects that don't follow it fall back
// to "note" cleanly.
func classifyNoteKind(path string) string {
	lower := strings.ToLower(path)
	base := path
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	baseLower := strings.ToLower(base)

	// Index files at any depth.
	if baseLower == "readme.md" || baseLower == "hot.md" || baseLower == "log.md" {
		return "index"
	}

	// Directory-qualified kinds. Check the first /.../ segment after the
	// top-level project folder.
	parts := strings.Split(lower, "/")
	for _, seg := range parts[:len(parts)-1] { // exclude filename itself
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

func sortTree(n *treeNode) {
	sort.Slice(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		return a.Name < b.Name
	})
	for _, c := range n.Children {
		sortTree(c)
	}
}

// computeNoteCount walks the tree post-order and fills NoteCount on directory
// nodes with the number of note descendants (not counting nested folders).
func computeNoteCount(n *treeNode) int {
	if !n.IsDir {
		return 1
	}
	total := 0
	for _, c := range n.Children {
		total += computeNoteCount(c)
	}
	n.NoteCount = total
	return total
}
