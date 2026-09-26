package v1

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/index"
)

// queryRequest is the body of POST /api/v1/query: the same query as the MCP
// memory_query tool (IMP-099).
type queryRequest struct {
	Project string `json:"project"`
	Where   []struct {
		Field string `json:"field"`
		Op    string `json:"op"`
		Value any    `json:"value"`
	} `json:"where"`
	Sort   string   `json:"sort"`
	Order  string   `json:"order"`
	Fields []string `json:"fields"`
	Limit  int      `json:"limit"`
}

type queryNote struct {
	Path     string         `json:"path"`
	Title    string         `json:"title"`
	Modified string         `json:"modified"`
	Fields   map[string]any `json:"fields,omitempty"`
}

// handleQuery selects notes by their frontmatter for the web UI. The
// principal's readable projects scope the query inside the index, like the
// search; canSee repeats it per note.
func (r *Router) handleQuery(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	if r.deps.Index == nil {
		WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "index not configured")
		return
	}
	var body queryRequest
	if err := DecodeJSON(req, &body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	if len(body.Where) == 0 {
		WriteError(w, http.StatusBadRequest, CodeValidationRequired, "where needs at least one condition")
		return
	}
	where := make([]index.FieldCond, 0, len(body.Where))
	for _, c := range body.Where {
		values, err := index.CondValues(c.Value)
		if err != nil {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
			return
		}
		where = append(where, index.FieldCond{Field: c.Field, Op: c.Op, Values: values})
	}
	desc, err := index.SortDesc(body.Sort, body.Order)
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	fields := body.Fields
	if len(fields) == 0 {
		fields = index.DefaultQueryFields(where, body.Sort)
	}

	p := principalFromContext(req)
	scope, err := r.searchScope(p, strings.TrimSpace(body.Project))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	hits, total, err := r.deps.Index.Query(index.QueryOptions{
		Projects: scope, Where: where, Sort: strings.TrimSpace(body.Sort), Desc: desc,
		Limit: index.ClampQueryLimit(body.Limit), Fields: fields,
	})
	if errors.Is(err, index.ErrBadQuery) {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	out := make([]queryNote, 0, len(hits))
	for _, h := range hits {
		if !r.canSee(p, h.Path) {
			total--
			continue
		}
		out = append(out, queryNote{Path: h.Path, Title: h.Title,
			Modified: time.Unix(h.ModTime, 0).UTC().Format(time.RFC3339), Fields: h.FieldValues()})
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"notes": out, "count": len(out), "total": total, "truncated": total > len(out),
	})
}
