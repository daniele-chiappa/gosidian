package v1

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/infolder"
	"github.com/gosidian/gosidian/internal/vault"
)

// The upload into an IN folder (IMP-172 phase 5), for the web UI and for
// curl alike:
//
//	GET    /api/v1/in/{project}/uploads                  the account's uploads
//	POST   /api/v1/in/{project}/uploads                  {dir, policy, files: [{path, size, unzip, modified}]}
//	GET    /api/v1/in/{project}/uploads/{id}?files=false  an upload, with its files but with files=false
//	PUT    /api/v1/in/{project}/uploads/{id}/files/{n}?offset=X   a piece of a file
//	POST   /api/v1/in/{project}/uploads/{id}/complete    {policy, drop: [n]}
//	DELETE /api/v1/in/{project}/uploads/{id}             drop it, or forget it once done
//
// Every one needs write access to the project; an upload is its opener's.

// inChannel is the way in of the uploads of this API, for the caps of [in].
const inChannel = "web"

// maxUploadOpenBody caps the list of files of an upload: 100000 paths.
const maxUploadOpenBody = 32 << 20

// uploadsIN serves /api/v1/in/{project}/uploads and what is under it.
func (r *Router) uploadsIN(w http.ResponseWriter, req *http.Request, user *RequestUser, project, rest string) {
	if denyGuestWrite(w, user) || r.denyWriteProject(w, user.principal(), project) {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(rest, "uploads"), "/"), "/")
	if len(parts) == 1 && parts[0] == "" {
		parts = nil
	}
	switch {
	case len(parts) == 0 && req.Method == http.MethodGet:
		list, err := r.deps.IN.Uploads(project, user.ID)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"uploads": list})
	case len(parts) == 0 && req.Method == http.MethodPost:
		r.openUpload(w, req, user, project)
	case len(parts) == 1 && req.Method == http.MethodGet:
		u, err := r.deps.IN.GetUpload(project, parts[0], user.ID, req.URL.Query().Get("files") != "false")
		if err != nil {
			uploadError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, u)
	case len(parts) == 1 && req.Method == http.MethodDelete:
		if err := r.deps.IN.CancelUpload(project, parts[0], user.ID); err != nil {
			uploadError(w, err)
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"id": parts[0], "dropped": true})
	case len(parts) == 2 && parts[1] == "complete" && req.Method == http.MethodPost:
		r.completeUpload(w, req, user, project, parts[0])
	case len(parts) == 3 && parts[1] == "files" && req.Method == http.MethodPut:
		r.putPiece(w, req, user, project, parts[0], parts[2])
	case len(parts) <= 3:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
	default:
		WriteError(w, http.StatusNotFound, CodeNotFound, "use /uploads, /uploads/<id>, /uploads/<id>/files/<n> or /uploads/<id>/complete")
	}
}

func (r *Router) openUpload(w http.ResponseWriter, req *http.Request, user *RequestUser, project string) {
	var body struct {
		Dir    string                `json:"dir"`
		Policy string                `json:"policy"`
		Files  []infolder.UploadFile `json:"files"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, req.Body, maxUploadOpenBody))
	if err := dec.Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "dir, policy and files [{path, size, unzip, modified}]: "+err.Error())
		return
	}
	for i := range body.Files {
		// What the server says of a file is not the client's to say.
		body.Files[i].N, body.Files[i].Received, body.Files[i].SHA256 = i, 0, ""
		body.Files[i].Dropped, body.Files[i].Exists, body.Files[i].Result = false, nil, nil
	}
	dir := strings.TrimPrefix(strings.TrimPrefix(inVaultPath(project, body.Dir), vault.INRoot(project)), "/")
	u, err := r.deps.IN.OpenUpload(infolder.UploadRequest{Project: project, Dir: dir, Policy: body.Policy,
		Owner: user.Username, OwnerID: user.ID, Channel: inChannel, Files: body.Files})
	if err != nil {
		uploadError(w, err)
		return
	}
	WriteJSON(w, http.StatusCreated, u)
}

func (r *Router) putPiece(w http.ResponseWriter, req *http.Request, user *RequestUser, project, id, n string) {
	file, err := strconv.Atoi(n)
	if err != nil || file < 0 {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "the file is its number in the upload")
		return
	}
	offset, err := strconv.ParseInt(req.URL.Query().Get("offset"), 10, 64)
	if err != nil || offset < 0 {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "offset is the bytes of the file received so far")
		return
	}
	if req.ContentLength > infolder.MaxChunk {
		WriteError(w, http.StatusRequestEntityTooLarge, CodeTooLarge, "a piece is at most "+strconv.Itoa(infolder.MaxChunk)+" bytes")
		return
	}
	body := http.MaxBytesReader(w, req.Body, infolder.MaxChunk+1)
	f, err := r.deps.IN.WriteChunk(project, id, user.ID, file, offset, body)
	// The rest of a piece refused is read before the answer: with a body
	// left unread, Go closes the connection, and the client, still
	// sending, sees a reset instead of the 409 or the 507.
	_, _ = io.Copy(io.Discard, body)
	if err != nil {
		var off *infolder.OffsetError
		if errors.As(err, &off) {
			WriteErrorWithDetails(w, http.StatusConflict, CodeConflict, err.Error(), map[string]any{"received": off.Received})
			return
		}
		uploadError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"n": f.N, "received": f.Received, "size": f.Size, "sha256": f.SHA256})
}

func (r *Router) completeUpload(w http.ResponseWriter, req *http.Request, user *RequestUser, project, id string) {
	var body struct {
		Policy string `json:"policy"`
		Drop   []int  `json:"drop"`
	}
	if req.ContentLength != 0 {
		if err := DecodeJSON(req, &body); err != nil {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
			return
		}
	}
	u, err := r.deps.IN.CompleteUpload(project, id, user.ID, body.Policy, body.Drop)
	if err != nil {
		var inc *infolder.IncompleteError
		if errors.As(err, &inc) {
			WriteErrorWithDetails(w, http.StatusConflict, CodeConflict, err.Error(), map[string]any{"files": inc.Files})
			return
		}
		uploadError(w, err)
		return
	}
	WriteJSON(w, http.StatusAccepted, u)
}

// uploadError answers an error of the uploads.
func uploadError(w http.ResponseWriter, err error) {
	var maxErr *http.MaxBytesError
	switch {
	case errors.Is(err, infolder.ErrUploadNotFound):
		WriteError(w, http.StatusNotFound, CodeNotFound, err.Error())
	case errors.Is(err, infolder.ErrNoSpace):
		WriteError(w, http.StatusInsufficientStorage, CodeStorageFull, err.Error())
	case errors.Is(err, infolder.ErrTooLarge):
		WriteError(w, http.StatusRequestEntityTooLarge, CodeTooLarge, err.Error())
	case errors.As(err, &maxErr):
		WriteError(w, http.StatusRequestEntityTooLarge, CodeTooLarge, "a piece is at most "+strconv.Itoa(infolder.MaxChunk)+" bytes")
	case errors.Is(err, infolder.ErrBadUpload), errors.Is(err, vault.ErrNotIN):
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
	case errors.Is(err, infolder.ErrUploadState):
		WriteError(w, http.StatusConflict, CodeConflict, err.Error())
	case errors.Is(err, fs.ErrNotExist):
		WriteError(w, http.StatusNotFound, CodeNotFound, err.Error())
	default:
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
	}
}

// AuditUpload is the audit of an upload whose completion ended: an entry
// for it (the folder, its id, the bytes placed), and one for each file it
// placed but a zip's (its folder has the zip's entry). One of an agent's
// (the channel mcp) is the MCP's, with the token as its actor.
func AuditUpload(log *audit.Log, u infolder.Upload) {
	if log == nil {
		return
	}
	source := audit.SourceHTTP
	if u.Channel == "mcp" {
		source = audit.SourceMCP
	}
	var bytes int64
	for _, f := range u.Files {
		if f.Result == nil || f.Result.Path == "" {
			continue
		}
		var action audit.Action
		switch f.Result.Result {
		case infolder.ResultNew, infolder.ResultRenamed:
			action = audit.ActionCreate
		case infolder.ResultReplaced:
			action = audit.ActionUpdate
		case infolder.ResultUnzipped:
			action = audit.ActionCreate
		default:
			continue
		}
		bytes += f.Size
		_ = log.Write(audit.Entry{Source: source, Actor: u.Owner, UserID: u.OwnerID, Action: action, Path: f.Result.Path, Size: f.Size})
	}
	_ = log.Write(audit.Entry{Source: source, Actor: u.Owner, UserID: u.OwnerID, Action: audit.ActionINUpload,
		Path: vault.INRoot(u.Project) + strings.TrimSuffix("/"+u.Dir, "/"), To: u.ID, Size: bytes})
}
