package v1

import (
	"net/http"
	"sort"
	"strings"

	"github.com/gosidian/gosidian/internal/authz"
)

type tagView struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

// handleTags returns the tag set with counts. Filter by `?project=`
// (top-level dir) to scope results to a single project's notes.
func (r *Router) handleTags(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	if r.deps.Index == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "index not configured")
		return
	}
	project := strings.TrimSpace(req.URL.Query().Get("project"))
	p := principalFromContext(req)
	var rows []indexTagCount
	var err error
	switch {
	case project != "":
		if !r.canAccessProject(p, project) {
			WriteJSON(w, http.StatusOK, map[string]any{"items": []tagView{}, "total": 0})
			return
		}
		raw, e := r.deps.Index.TagsByProject(project)
		err = e
		for _, t := range raw {
			rows = append(rows, indexTagCount{Tag: t.Tag, Count: t.Count})
		}
	default:
		// Owner/member: full vault tag set. Guest: public projects only.
		rows, err = r.visibleTags(p)
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	out := make([]tagView, 0, len(rows))
	for _, t := range rows {
		out = append(out, tagView{Tag: t.Tag, Count: t.Count})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": out, "total": len(out)})
}

// handleTagByName lists notes carrying a specific tag. The tag name
// is everything after `/api/v1/tags/`. Trailing slashes are tolerated.
// Filter by `?project=` to intersect with a project; useful for
// "show me all status:in-progress notes in this project" queries.
func (r *Router) handleTagByName(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	if r.deps.Index == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "index not configured")
		return
	}
	tag := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/api/v1/tags/"), "/")
	if tag == "" {
		WriteError(w, http.StatusBadRequest, CodeValidationRequired, "tag name required")
		return
	}
	project := strings.TrimSpace(req.URL.Query().Get("project"))

	rows, err := r.deps.Index.NotesByTag(tag)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	p := principalFromContext(req)
	out := make([]noteSummary, 0, len(rows))
	for _, n := range rows {
		if project != "" && !strings.HasPrefix(n.Path, project+"/") && n.Path != project {
			continue
		}
		if !r.canSee(p, n.Path) {
			continue
		}
		out = append(out, noteSummary{Path: n.Path, Title: n.Title})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": out, "total": len(out)})
}

// indexTagCount mirrors index.TagCount field-for-field so the handler
// can iterate without re-importing the type into the response shape.
type indexTagCount struct {
	Tag   string
	Count int
}

// visibleTags returns the tag counts the principal may see. The owner gets
// the full vault tag set; everyone else gets tags aggregated over the
// projects they may read, so private tags never leak through the tag list.
func (r *Router) visibleTags(p authz.Principal) ([]indexTagCount, error) {
	if r.seesAllProjects(p) {
		raw, err := r.deps.Index.Tags()
		if err != nil {
			return nil, err
		}
		out := make([]indexTagCount, 0, len(raw))
		for _, t := range raw {
			out = append(out, indexTagCount{Tag: t.Tag, Count: t.Count})
		}
		return out, nil
	}
	merged := map[string]int{}
	projs, err := r.deps.Vault.Projects()
	if err != nil {
		return nil, err
	}
	for _, proj := range projs {
		if !r.canAccessProject(p, proj.Name) {
			continue
		}
		raw, err := r.deps.Index.TagsByProject(proj.Name)
		if err != nil {
			return nil, err
		}
		for _, t := range raw {
			merged[t.Tag] += t.Count
		}
	}
	out := make([]indexTagCount, 0, len(merged))
	for tag, c := range merged {
		out = append(out, indexTagCount{Tag: tag, Count: c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tag < out[j].Tag })
	return out, nil
}
