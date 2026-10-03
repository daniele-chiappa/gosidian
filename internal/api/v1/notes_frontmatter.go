package v1

import (
	"errors"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/dbschema"
	"github.com/gosidian/gosidian/internal/frontmatter"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/server/events"
	"github.com/gosidian/gosidian/internal/views"
)

// patchFrontmatterRequest edits some frontmatter keys of a note without
// sending the whole note: the property panel and the cells of the web UI
// (IMP-127 phase 5).
type patchFrontmatterRequest struct {
	// Set holds the keys to write, with their new values: a string, a
	// number, a bool, null or a list of strings.
	Set map[string]any `json:"set"`
	// Unset lists the keys to remove.
	Unset []string `json:"unset"`
	// Expect holds, for some keys, the value the client last saw. A key
	// whose value has changed since makes the edit fail with 409.
	Expect map[string]any `json:"expect"`
	// IfMatch is the etag of the whole note, as the If-Match header.
	IfMatch string `json:"if_match"`
}

// patchFrontmatter writes and removes frontmatter keys of the markdown note
// at rel, rewriting only their lines (frontmatter.SetKeys).
//
// Concurrency is checked per field: expect compares the values the client
// saw with the note as it is now, so an agent that changed another field of
// the same note in the meantime does not make the edit fail; a field that
// changed answers 409 with its current value. if_match, or the If-Match
// header, is the strict check on the whole note (412, as for PUT).
//
// A row of a database must keep its schema for the keys it touches (422,
// with the problems). A value that cannot be written so that every reader
// reads it back unchanged is also 422; a key whose YAML is too complex to
// edit line by line is 400, to be edited as text.
func (r *Router) patchFrontmatter(w http.ResponseWriter, req *http.Request, rel string) {
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	if denyGuestWrite(w, user) {
		return
	}
	if r.denyWriteProject(w, user.principal(), projectOf(rel)) {
		return
	}
	if !r.deps.Vault.IsNoteFile(rel) || !strings.HasSuffix(strings.ToLower(rel), ".md") {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "frontmatter can be edited only on markdown notes")
		return
	}
	var body patchFrontmatterRequest
	if err := DecodeJSON(req, &body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	if len(body.Set) == 0 && len(body.Unset) == 0 {
		WriteError(w, http.StatusBadRequest, CodeValidationRequired, "nothing to change: give set or unset")
		return
	}
	ifMatch := strings.TrimSpace(req.Header.Get("If-Match"))
	if ifMatch == "" {
		ifMatch = strings.TrimSpace(body.IfMatch)
	}

	unlock := r.deps.Vault.LockPath(rel)
	defer unlock()
	existing, err := r.deps.Vault.Load(rel)
	if err != nil {
		writeLoadError(w, err)
		return
	}
	if ifMatch != "" && !etagMatches(ifMatch, existing.ETag()) {
		WriteErrorWithDetails(w, http.StatusPreconditionFailed, CodeConcurrencyEtag,
			"note was modified since the last read",
			map[string]any{"current_etag": quoteETag(existing.ETag())})
		return
	}
	raw := parser.ExtractFrontmatterRaw(existing.Content)
	if changed := changedFields(raw, body.Expect); len(changed) > 0 {
		WriteErrorWithDetails(w, http.StatusConflict, CodeConflict,
			"a field was modified since the last read",
			map[string]any{"fields": changed, "current_etag": quoteETag(existing.ETag())})
		return
	}
	var schema *dbschema.Schema
	if r.deps.Index != nil {
		if schema, err = dbschema.Covering(r.deps.Index, r.deps.Vault, rel); err != nil {
			WriteError(w, http.StatusInternalServerError, CodeServerInternal, "schema: "+err.Error())
			return
		}
	}
	if schema != nil {
		if probs := schema.CheckEdit(rel, body.Set, body.Unset); len(probs) > 0 {
			WriteErrorWithDetails(w, http.StatusUnprocessableEntity, CodeValidationFormat,
				"the change breaks the schema of "+schema.Path,
				map[string]any{"problems": probs, "schema": schema.Path})
			return
		}
	}

	content, err := frontmatter.SetKeys(existing.Content, orderedFields(body.Set, schema), body.Unset)
	var valueErr *frontmatter.ValueError
	var complexErr *frontmatter.ComplexError
	switch {
	case errors.As(err, &valueErr):
		WriteErrorWithDetails(w, http.StatusUnprocessableEntity, CodeValidationFormat, err.Error(),
			map[string]any{"field": valueErr.Key})
		return
	case errors.As(err, &complexErr), errors.Is(err, frontmatter.ErrUnterminated):
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error()+": edit the note as text")
		return
	case err != nil:
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "frontmatter: "+err.Error())
		return
	}

	fresh := existing
	if string(content) != string(existing.Content) {
		if err := r.writeAndIndex(rel, content); err != nil {
			WriteError(w, http.StatusInternalServerError, CodeServerInternal, "write: "+err.Error())
			return
		}
		if fresh, err = r.deps.Vault.Load(rel); err != nil {
			writeLoadError(w, err)
			return
		}
		r.auditNote(req, audit.ActionUpdate, user, rel, "", int64(len(content)))
		r.publishNoteEvent(events.TopicNote, rel, "update", fresh)
	}
	w.Header().Set("ETag", quoteETag(fresh.ETag()))
	WriteJSON(w, http.StatusOK, r.toNoteResponse(fresh))
}

// orderedFields lists the keys of set in the order a new key is written
// with: the fields of the schema in declaration order, then the others
// alphabetically. A key already in the note keeps its place.
func orderedFields(set map[string]any, schema *dbschema.Schema) []frontmatter.Field {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	rank := func(k string) int {
		if schema != nil {
			if i := slices.Index(schema.FieldNames(), k); i >= 0 {
				return i
			}
		}
		return len(keys) + 1<<20
	}
	sort.Slice(keys, func(i, j int) bool {
		if ri, rj := rank(keys[i]), rank(keys[j]); ri != rj {
			return ri < rj
		}
		return keys[i] < keys[j]
	})
	out := make([]frontmatter.Field, len(keys))
	for i, k := range keys {
		out[i] = frontmatter.Field{Key: k, Value: set[k]}
	}
	return out
}

// changedFields compares the values the client saw (expect, as JSON
// decodes them) with the frontmatter as it is now, read the way the index
// reads it. It returns the current value of each key that differs: a
// string, a list of strings, or nil for a key that is absent or empty.
func changedFields(raw string, expect map[string]any) map[string]any {
	out := map[string]any{}
	if len(expect) == 0 {
		return out
	}
	fields := parser.ParseFrontmatterFields(raw)
	for key, seen := range expect {
		_, wantList := seen.([]any)
		cur := currentField(raw, fields, key, wantList)
		if !sameField(cur, seen) {
			out[key] = cur
		}
	}
	return out
}

func currentField(raw string, fields map[string]any, key string, list bool) any {
	if !regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `:`).MatchString(raw) {
		return nil
	}
	if key == "tags" {
		if tags, ok := fields["tags"].([]string); ok {
			return tags
		}
		return []string{}
	}
	if list {
		if items := parser.FrontmatterList(raw, key); items != nil {
			return items
		}
		return []string{}
	}
	if s, ok := fields[key].(string); ok {
		return s
	}
	return nil
}

// sameField reports whether cur, the value in the note, is the value seen:
// a number or a bool matches its text, and an absent key matches null, ""
// and an empty list.
func sameField(cur, seen any) bool {
	switch s := seen.(type) {
	case nil:
		return cur == nil || cur == "" || isEmptyList(cur)
	case string:
		if s == "" && cur == nil {
			return true
		}
		c, ok := cur.(string)
		return ok && c == s
	case bool:
		c, ok := cur.(string)
		return ok && c == strconv.FormatBool(s)
	case float64:
		c, ok := cur.(string)
		if !ok {
			return false
		}
		n, err := strconv.ParseFloat(c, 64)
		return err == nil && n == s
	case []any:
		c, _ := cur.([]string)
		if len(c) != len(s) {
			return false
		}
		for i, e := range s {
			if !sameField(c[i], e) {
				return false
			}
		}
		return true
	}
	return false
}

func isEmptyList(v any) bool {
	l, ok := v.([]string)
	return ok && len(l) == 0
}

// rowFieldsResponse is what the web UI's property panel shows for a note
// (IMP-127 phase 5). For a row of a database the reader may see: the
// schema's fields, the note's values as the index reads them, and whether
// the reader may edit them. For any other note only Values, empty.
type rowFieldsResponse struct {
	Database string         `json:"database,omitempty"`
	Source   string         `json:"source,omitempty"`
	Columns  []views.Column `json:"columns,omitempty"`
	// Others lists the note's fields the schema does not declare.
	Others   []string                `json:"others,omitempty"`
	Values   map[string]any          `json:"values"`
	Links    map[string][]views.Link `json:"links,omitempty"`
	Writable bool                    `json:"writable"`
}

var frontmatterKeyRe = regexp.MustCompile(`(?m)^([a-zA-Z_][\w-]*):`)

// readRowFields answers GET /notes/{path}/fields.
func (r *Router) readRowFields(w http.ResponseWriter, req *http.Request, rel string) {
	p := principalFromContext(req)
	if !r.canSee(p, rel) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "note not found")
		return
	}
	note, err := r.deps.Vault.Load(rel)
	if err != nil {
		writeLoadError(w, err)
		return
	}
	resp := rowFieldsResponse{Values: map[string]any{}}
	if r.deps.Index == nil {
		WriteJSON(w, http.StatusOK, resp)
		return
	}
	schema, err := dbschema.Covering(r.deps.Index, r.deps.Vault, rel)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "schema: "+err.Error())
		return
	}
	if schema == nil || !r.canSee(p, schema.Path) {
		WriteJSON(w, http.StatusOK, resp)
		return
	}
	// The schema's fields, then the others the note has, minus the implicit
	// title and tags.
	names := schema.FieldNames()
	for _, m := range frontmatterKeyRe.FindAllStringSubmatch(parser.FrontmatterRawForPath(rel, note.Content), -1) {
		if k := m[1]; k != "title" && k != "tags" && !slices.Contains(names, k) {
			names = append(names, k)
			resp.Others = append(resp.Others, k)
		}
	}
	hits, total, err := r.deps.Index.Query(index.QueryOptions{
		Folders: []string{schema.Source}, Paths: []string{rel}, Fields: names, Limit: 1,
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "query: "+err.Error())
		return
	}
	res := &views.Result{Spec: &views.Spec{As: "table", Columns: names}, Hits: hits, Total: total, Schema: schema}
	d := res.Data(views.Context{CanWrite: r.viewCanWrite(p), Resolve: previewResolver{r: r, p: p}.Resolve})
	resp.Database, resp.Source = d.Database, d.Source
	resp.Columns = d.Columns[:len(schema.Fields)]
	if len(d.Rows) == 1 {
		resp.Values, resp.Links, resp.Writable = d.Rows[0].Fields, d.Rows[0].Links, d.Rows[0].Writable
	}
	WriteJSON(w, http.StatusOK, resp)
}
