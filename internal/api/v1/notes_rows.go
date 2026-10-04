package v1

import (
	"cmp"
	"errors"
	"net/http"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/dbschema"
	"github.com/gosidian/gosidian/internal/frontmatter"
	"github.com/gosidian/gosidian/internal/parser"
	"github.com/gosidian/gosidian/internal/server/events"
	"github.com/gosidian/gosidian/internal/views"
)

// newRowResponse is what the web UI's form for a new row of a database
// starts from (IMP-127 phase 5).
type newRowResponse struct {
	// Name is the suggested file name of the row, without .md
	// (dbschema.NextName); "" when the rows are not numbered.
	Name     string `json:"name"`
	Source   string `json:"source"`
	Template string `json:"template,omitempty"`
	// Columns are the fields of the schema, typed.
	Columns []views.Column `json:"columns"`
	// Preset lists the fields the template sets, so the form does not ask
	// for them.
	Preset []string `json:"preset,omitempty"`
}

// createRowRequest is the body of POST /notes/{database}/rows.
type createRowRequest struct {
	// Name is the file name of the row, without .md; it is also its id when
	// the schema declares one.
	Name  string `json:"name"`
	Title string `json:"title"`
	// Values holds the fields to set, as for PATCH .../frontmatter.
	Values map[string]any `json:"values"`
}

// rowPlaceholder matches the placeholders of a row template: those of the
// scaffold templates, {{PROJECT}} and {{TODAY}}, plus {{ID}} and {{TITLE}}.
var rowPlaceholder = regexp.MustCompile(`\{\{(ID|TITLE|TODAY|PROJECT)\}\}`)

// database loads the schema of the database note at rel for a request that
// adds rows to it: the reader must see the note and may write the project
// of its rows. It writes the error and returns nil otherwise.
func (r *Router) database(w http.ResponseWriter, req *http.Request, rel string) (*dbschema.Schema, *RequestUser) {
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return nil, nil
	}
	if !r.canSee(user.principal(), rel) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "note not found")
		return nil, nil
	}
	note, err := r.deps.Vault.Load(rel)
	if err != nil {
		writeLoadError(w, err)
		return nil, nil
	}
	schema, err := dbschema.Parse(rel, parser.FrontmatterRawForPath(rel, note.Content))
	switch {
	case errors.Is(err, dbschema.ErrNotDatabase):
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "not a database note")
		return nil, nil
	case err != nil:
		WriteError(w, http.StatusUnprocessableEntity, CodeValidationFormat, "the schema of "+rel+" does not parse: "+err.Error())
		return nil, nil
	}
	if denyGuestWrite(w, user) || r.denyWriteProject(w, user.principal(), projectOf(schema.Source)) {
		return nil, nil
	}
	return schema, user
}

// rowNames lists the file names of the rows of schema, without .md.
func (r *Router) rowNames(schema *dbschema.Schema) []string {
	if r.deps.Index == nil {
		return nil
	}
	notes, err := r.deps.Index.NotesByPrefix(schema.Source)
	if err != nil {
		return nil
	}
	var out []string
	for _, n := range notes {
		if schema.Covers(n.Path) {
			out = append(out, strings.TrimSuffix(path.Base(n.Path), path.Ext(n.Path)))
		}
	}
	return out
}

// readNewRow answers GET /notes/{database}/new-row: the suggested name of a
// new row and the fields to ask for.
func (r *Router) readNewRow(w http.ResponseWriter, req *http.Request, rel string) {
	schema, _ := r.database(w, req, rel)
	if schema == nil {
		return
	}
	resp := newRowResponse{Name: dbschema.NextName(r.rowNames(schema)), Source: schema.Source, Template: schema.Template}
	for _, f := range schema.Fields {
		resp.Columns = append(resp.Columns, views.Column{Name: f.Name, Type: f.Type, Options: f.Options, Required: f.Required})
	}
	if schema.Template != "" {
		if tpl, err := r.deps.Vault.Load(schema.Template); err == nil {
			for _, m := range frontmatterKeyRe.FindAllStringSubmatch(parser.ExtractFrontmatterRaw(tpl.Content), -1) {
				resp.Preset = append(resp.Preset, m[1])
			}
		}
	}
	WriteJSON(w, http.StatusOK, resp)
}

// createRow answers POST /notes/{database}/rows: it writes a new row of the
// database, from its template when it declares one, with the name, the
// title and the values given. The whole row must match the schema (422), so
// a row made here passes lint; a row of that name already there is 409,
// with the next name suggested.
func (r *Router) createRow(w http.ResponseWriter, req *http.Request, rel string) {
	schema, user := r.database(w, req, rel)
	if schema == nil {
		return
	}
	var body createRowRequest
	if err := DecodeJSON(req, &body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	name := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(body.Name), ".md"))
	title := strings.TrimSpace(body.Title)
	switch {
	case name == "":
		WriteError(w, http.StatusBadRequest, CodeValidationRequired, "name required")
		return
	case strings.ContainsAny(name, "/\\\r\n") || strings.HasPrefix(name, "."):
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "name must be a file name, without folders or a leading dot")
		return
	case strings.ContainsAny(title, "\r\n"):
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "title must be one line")
		return
	}
	if title == "" {
		title = name
	}
	clean, err := r.deps.Vault.Rel(schema.Source + "/" + name + ".md")
	if err != nil || !schema.Covers(clean) {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "invalid name")
		return
	}

	var tpl []byte
	if schema.Template != "" {
		note, err := r.deps.Vault.Load(schema.Template)
		if errors.Is(err, os.ErrNotExist) {
			WriteError(w, http.StatusUnprocessableEntity, CodeValidationFormat,
				"the template "+schema.Template+" of "+schema.Path+" does not exist")
			return
		} else if err != nil {
			writeLoadError(w, err)
			return
		}
		tpl = note.Content
	}
	// A database whose rows have given values (rows: {type: plan}) gives
	// them to a new row, unless the request sets them otherwise.
	if len(schema.Rows) > 0 {
		vals := make(map[string]any, len(body.Values)+len(schema.Rows))
		for k, v := range body.Values {
			vals[k] = v
		}
		for _, kv := range schema.RowConds() {
			if _, set := vals[kv[0]]; !set {
				vals[kv[0]] = kv[1]
			}
		}
		body.Values = vals
	}
	vars := map[string]string{
		"ID": name, "TITLE": title, "PROJECT": projectOf(schema.Source),
		"TODAY": time.Now().UTC().Format("2006-01-02"),
	}
	content, err := buildRow(tpl, schema, name, title, body.Values, vars)
	var valueErr *frontmatter.ValueError
	var complexErr *frontmatter.ComplexError
	switch {
	case errors.As(err, &valueErr):
		WriteErrorWithDetails(w, http.StatusUnprocessableEntity, CodeValidationFormat, err.Error(),
			map[string]any{"field": valueErr.Key})
		return
	case errors.As(err, &complexErr), errors.Is(err, frontmatter.ErrUnterminated):
		WriteError(w, http.StatusUnprocessableEntity, CodeValidationFormat,
			"the template "+schema.Template+" cannot be filled: "+err.Error())
		return
	case err != nil:
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "row: "+err.Error())
		return
	}
	if probs := schema.Validate(clean, parser.ExtractFrontmatterRaw(content)); len(probs) > 0 {
		WriteErrorWithDetails(w, http.StatusUnprocessableEntity, CodeValidationFormat,
			"the row breaks the schema of "+schema.Path,
			map[string]any{"problems": probs, "schema": schema.Path})
		return
	}

	unlock := r.deps.Vault.LockPath(clean)
	defer unlock()
	if _, err := r.deps.Vault.Load(clean); err == nil {
		WriteErrorWithDetails(w, http.StatusConflict, CodeConflict, "a row named "+name+" already exists",
			map[string]any{"name": dbschema.NextName(append(r.rowNames(schema), name))})
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "load probe: "+err.Error())
		return
	}
	if err := r.writeAndIndex(clean, content); err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "write: "+err.Error())
		return
	}
	note, err := r.deps.Vault.Load(clean)
	if err != nil {
		writeLoadError(w, err)
		return
	}
	r.auditNote(req, audit.ActionCreate, user, clean, "", int64(len(content)))
	r.publishNoteEvent(events.TopicNote, clean, "create", note)
	r.publishNoteEvent(events.TopicTree, clean, "create", note)
	w.Header().Set("ETag", quoteETag(note.ETag()))
	WriteJSON(w, http.StatusCreated, r.toNoteResponse(note))
}

// buildRow writes the content of a new row: the template with its
// placeholders filled, or a note with just a heading, then the id (when the
// schema declares one), the title and tags when the template has none, and
// the values. Placeholders in the frontmatter are filled field by field and
// written with frontmatter.SetKeys, so a title holding a colon or quotes
// keeps the YAML valid; in the body they are replaced as text.
func buildRow(tpl []byte, schema *dbschema.Schema, name, title string, values map[string]any, vars map[string]string) ([]byte, error) {
	fill := func(s string) string {
		return rowPlaceholder.ReplaceAllStringFunc(s, func(m string) string { return vars[m[2:len(m)-2]] })
	}
	set := map[string]any{}
	content := []byte("\n# " + title + "\n")
	raw := ""
	if tpl != nil {
		body := parser.BodyAfterFrontmatter(tpl)
		head := string(tpl[:len(tpl)-len(body)])
		content = []byte(head + fill(body))
		raw = parser.ExtractFrontmatterRaw(tpl)
		fields := parser.ParseFrontmatterFields(raw)
		for _, key := range placeholderKeys(raw) {
			line := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `:[ \t]*(.*)$`).FindStringSubmatch(raw)
			if rest := strings.TrimSpace(line[1]); rest == "" || strings.HasPrefix(rest, "[") {
				items := parser.FrontmatterList(raw, key)
				filled := make([]string, len(items))
				for i, it := range items {
					filled[i] = fill(it)
				}
				set[key] = filled
			} else if s, ok := fields[key].(string); ok {
				set[key] = fill(s)
			}
		}
	}
	if !parser.HasFrontmatterKey(raw, "title") {
		set["title"] = title
	}
	if !parser.HasFrontmatterKey(raw, "tags") {
		set["tags"] = []string{vars["PROJECT"]}
	}
	for k, v := range values {
		set[k] = v
	}
	if _, ok := schema.Field("id"); ok {
		set["id"] = name
	}
	// A new title goes first, as in a note written by hand; a key the
	// template has keeps its place whatever the order.
	fields := orderedFields(set, schema)
	slices.SortStableFunc(fields, func(a, b frontmatter.Field) int {
		return cmp.Compare(boolRank(a.Key != "title"), boolRank(b.Key != "title"))
	})
	return frontmatter.SetKeys(content, fields, nil)
}

func boolRank(b bool) int {
	if b {
		return 1
	}
	return 0
}

// placeholderKeys lists the top-level keys of raw frontmatter whose value
// holds a placeholder, on the key's line or the lines of a block below it.
func placeholderKeys(raw string) []string {
	var out []string
	key := ""
	for _, line := range strings.Split(raw, "\n") {
		if m := frontmatterKeyRe.FindStringSubmatch(line); m != nil {
			key = m[1]
		}
		if key != "" && rowPlaceholder.MatchString(line) && (len(out) == 0 || out[len(out)-1] != key) {
			out = append(out, key)
		}
	}
	return out
}
