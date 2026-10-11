package v1

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/index"
	"github.com/gosidian/gosidian/internal/infolder"
	"github.com/gosidian/gosidian/internal/pipeline"
	"github.com/gosidian/gosidian/internal/server/events"
	"github.com/gosidian/gosidian/internal/vault"
)

// openIN prepares a project's IN folder as use_in goes on (IMP-172): a
// folder named IN that holds notes would hide them from the index, so it
// is refused; a missing one is made. It returns a status and why, or 0.
func (r *Router) openIN(project string) (int, string) {
	notes, err := r.deps.Vault.NotesInIN(project, 5)
	if err != nil {
		return http.StatusInternalServerError, err.Error()
	}
	if len(notes) > 0 {
		return http.StatusConflict, "the folder " + vault.INRoot(project) + " holds notes (" + strings.Join(notes, ", ") +
			"): in an IN folder nothing is a note, move them out first"
	}
	if err := r.deps.Vault.EnsureIN(project); err != nil {
		return http.StatusConflict, err.Error()
	}
	return 0, ""
}

// inChanged follows a change of use_in or track_in_git: the managed
// .gitignore at the next commit, and, when the IN folder closed, its notes
// back into the index, a folder like any other again.
func (r *Router) inChanged(project string, closed bool) {
	if r.deps.GitSync != nil {
		r.deps.GitSync.TriggerCommit()
	}
	if r.deps.IN != nil && !closed {
		r.deps.IN.ScanSoon(project)
	}
	if !closed || r.deps.Index == nil {
		return
	}
	notes, err := r.deps.Vault.ProjectNotes(project)
	if err != nil {
		return
	}
	prefix := vault.INRoot(project) + "/"
	for _, n := range notes {
		if !strings.HasPrefix(n.Path, prefix) {
			continue
		}
		if note, err := r.deps.Vault.Load(n.Path); err == nil {
			_ = r.deps.Index.Upsert(index.NoteDoc{Path: note.Path, Title: note.Title, Body: string(note.Content),
				ModTime: note.ModTime.Unix(), Size: note.Size})
		}
	}
}

// The IN window's API (IMP-172), on a project with use_in:
//
//	GET  /api/v1/in/{project}?dir=&recursive=&match=&kinds=&status=&limit=&cursor=
//	     → counts, the subfolders of dir, a page of its files
//	POST /api/v1/in/{project}/move    {from, to}
//	POST /api/v1/in/{project}/status  {paths, status}
//	POST /api/v1/in/{project}/delete  {paths}: into the trash (a folder needs it)
//	POST /api/v1/in/{project}/run     {pipeline, paths}: a run on the files
//
// Paths are relative to the IN folder, or vault paths. Reading needs read
// access to the project, the rest write access.
func (r *Router) handleIN(w http.ResponseWriter, req *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(req.URL.Path, "/api/v1/in/"), "/")
	project, action, _ := strings.Cut(rest, "/")
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return
	}
	princ := user.principal()
	if project == "" || !r.canAccessProject(princ, project) || r.deps.IN == nil || r.deps.Projects == nil || !r.deps.Projects.UsesIN(project) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "the project has no IN folder")
		return
	}
	if action == "" {
		if req.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
			return
		}
		r.listIN(w, req, project)
		return
	}
	if req.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	if denyGuestWrite(w, user) || r.denyWriteProject(w, princ, project) {
		return
	}
	switch action {
	case "move":
		r.moveIN(w, req, user, project)
	case "status":
		r.statusIN(w, req, project)
	case "delete":
		r.deleteIN(w, req, user, project)
	case "run":
		r.runIN(w, req, user, project)
	default:
		WriteError(w, http.StatusNotFound, CodeNotFound, "use /api/v1/in/<project>, /move, /status, /delete or /run")
	}
}

// inVaultPath is a path of a project's IN folder as a client names it
// (relative to the IN folder, or the vault path) as its vault path.
func inVaultPath(project, p string) string {
	p = strings.Trim(strings.TrimSpace(p), "/")
	root := vault.INRoot(project)
	switch {
	case p == "" || p == vault.INFolder:
		return root
	case p == root || strings.HasPrefix(p, root+"/"):
		return p
	case strings.HasPrefix(p, vault.INFolder+"/"):
		return project + "/" + p
	}
	return root + "/" + p
}

func (r *Router) listIN(w http.ResponseWriter, req *http.Request, project string) {
	q := req.URL.Query()
	dir := strings.TrimPrefix(strings.TrimPrefix(inVaultPath(project, q.Get("dir")), vault.INRoot(project)), "/")
	limit, _ := strconv.Atoi(q.Get("limit"))
	query := infolder.Query{
		Folder: dir, Shallow: q.Get("recursive") != "true",
		Match: strings.TrimSpace(q.Get("match")), Status: strings.TrimSpace(q.Get("status")),
		Limit: limit, Cursor: q.Get("cursor"),
	}
	if k := strings.TrimSpace(q.Get("kinds")); k != "" {
		query.Kinds = strings.Split(k, ",")
	}
	st := r.deps.IN.Store()
	page, err := st.List(project, query)
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	folders := []infolder.Folder{}
	if query.Shallow && query.Cursor == "" {
		if folders, err = st.Folders(project, dir); err != nil {
			WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
			return
		}
	}
	counts, err := st.Counts(project)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	if page.Files == nil {
		page.Files = []infolder.File{}
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"project": project, "root": vault.INRoot(project), "dir": dir, "counts": counts,
		"folders": folders, "files": page.Files, "next": page.Next,
		"track_in_git": r.deps.Projects.Get(project).TrackINInGit,
	})
}

// inError answers an error of the IN folder: a path it refuses is a bad
// request, a missing one is not found.
func inError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		WriteError(w, http.StatusNotFound, CodeNotFound, err.Error())
	case errors.Is(err, vault.ErrNotIN):
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
	default:
		WriteError(w, http.StatusConflict, CodeConflict, err.Error())
	}
}

func (r *Router) moveIN(w http.ResponseWriter, req *http.Request, user *RequestUser, project string) {
	var body struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := DecodeJSON(req, &body); err != nil || strings.TrimSpace(body.From) == "" || strings.TrimSpace(body.To) == "" {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "from and to are required")
		return
	}
	from, to := inVaultPath(project, body.From), inVaultPath(project, body.To)
	if strings.HasSuffix(strings.TrimSpace(body.To), "/") {
		to += "/" + path.Base(from)
	}
	e, err := r.deps.IN.Move(project, from, to)
	if err != nil {
		inError(w, err)
		return
	}
	r.auditNote(req, audit.ActionRename, user, from, e.Path, e.Size)
	r.publishINEvent(project)
	WriteJSON(w, http.StatusOK, map[string]any{"path": e.Path, "from": from})
}

func (r *Router) statusIN(w http.ResponseWriter, req *http.Request, project string) {
	var body struct {
		Paths  []string `json:"paths"`
		Status string   `json:"status"`
	}
	if err := DecodeJSON(req, &body); err != nil || len(body.Paths) == 0 {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "paths and status are required")
		return
	}
	paths := make([]string, len(body.Paths))
	for i, p := range body.Paths {
		paths[i] = inVaultPath(project, p)
	}
	if err := r.deps.IN.SetStatus(project, paths, body.Status, ""); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	r.publishINEvent(project)
	WriteJSON(w, http.StatusOK, map[string]any{"updated": len(paths)})
}

func (r *Router) deleteIN(w http.ResponseWriter, req *http.Request, user *RequestUser, project string) {
	var body struct {
		Paths []string `json:"paths"`
	}
	if err := DecodeJSON(req, &body); err != nil || len(body.Paths) == 0 {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "paths are required")
		return
	}
	var discard func(rel string, dir bool) error
	if bin := r.deps.Trash; bin != nil {
		discard = func(rel string, dir bool) error {
			var err error
			if dir {
				_, _, err = bin.DiscardFolder(rel)
			} else {
				_, err = bin.DiscardNote(rel)
			}
			return err
		}
	}
	var removed []string
	for _, p := range body.Paths {
		rel := inVaultPath(project, p)
		if discard == nil {
			if e, err := r.deps.Vault.INStat(rel); err == nil && e.Dir {
				WriteError(w, http.StatusServiceUnavailable, CodeServerUnavailable, "deleting a folder needs the trash, which is not enabled in server config")
				return
			}
		}
		if err := r.deps.IN.Remove(project, rel, discard); err != nil {
			inError(w, err)
			return
		}
		removed = append(removed, rel)
		r.auditNote(req, audit.ActionDelete, user, rel, "", 0)
	}
	r.publishINEvent(project)
	WriteJSON(w, http.StatusOK, map[string]any{"removed": removed, "trash": discard != nil})
}

func (r *Router) runIN(w http.ResponseWriter, req *http.Request, user *RequestUser, project string) {
	if r.pipelinesOff(w) {
		return
	}
	var body struct {
		Pipeline string   `json:"pipeline"`
		Paths    []string `json:"paths"`
	}
	if err := DecodeJSON(req, &body); err != nil || strings.TrimSpace(body.Pipeline) == "" || len(body.Paths) == 0 {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "pipeline and paths are required")
		return
	}
	if projectOf(body.Pipeline) != project {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "the pipeline is not in the project")
		return
	}
	files := make([]infolder.File, 0, len(body.Paths))
	for _, p := range body.Paths {
		f, err := r.deps.IN.Store().File(project, inVaultPath(project, p))
		if err != nil {
			WriteError(w, http.StatusNotFound, CodeNotFound, "not a file of the IN folder: "+p)
			return
		}
		files = append(files, f)
	}
	runs, skipped, err := r.deps.Pipelines.StartINFiles(body.Pipeline, files, user.Username)
	briefs := make([]any, 0, len(runs))
	for _, run := range runs {
		briefs = append(briefs, pipeline.Brief(run))
		_ = r.deps.Audit.Write(audit.Entry{Source: audit.SourceHTTP, Actor: user.Username, UserID: user.ID, Action: audit.ActionPipelineRun, Path: body.Pipeline, To: run.ID})
	}
	if err != nil {
		pipelineError(w, err)
		return
	}
	if skipped == nil {
		skipped = []pipeline.INSkipped{}
	}
	r.publishINEvent(project)
	WriteJSON(w, http.StatusAccepted, map[string]any{"runs": briefs, "skipped": skipped})
}

// publishINEvent tells the web UI that a project's IN folder changed.
func (r *Router) publishINEvent(project string) {
	if r.deps.Events != nil {
		r.deps.Events.Publish(events.TopicTree, map[string]any{"action": "in", "project": project, "path": vault.INRoot(project)})
	}
}
