package mcp

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/infolder"
	"github.com/gosidian/gosidian/internal/vault"
)

// handleINUploadHTTP is the HTTP side of memory_in_upload (IMP-172 phase
// 5b), mounted at <basePath>/in-upload/<id>. The upload's ticket is the
// bearer: the token that minted it is bound again, as of now, so that a
// token revoked, or an account taken off the project, stops it.
//
//	PUT  <basePath>/in-upload/<id>/files/<n>?offset=X   a piece of file n
//	GET  <basePath>/in-upload/<id>[?files=false]        the upload
//	POST <basePath>/in-upload/<id>/complete             {policy, drop}
func (s *Server) handleINUploadHTTP(w http.ResponseWriter, r *http.Request) {
	i := strings.LastIndex(r.URL.Path, "/in-upload/")
	if i < 0 || s.in == nil {
		writeJSONError(w, http.StatusNotFound, "unknown upload endpoint")
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path[i+len("/in-upload/"):], "/"), "/")
	id := parts[0]
	// A piece refused is read whole before the answer, as for the REST
	// API: with a body left unread, Go closes the connection, and the
	// client, still sending, sees a reset instead of the refusal.
	refuse := func(code int, msg string) {
		if r.Method == http.MethodPut {
			_, _ = io.Copy(io.Discard, http.MaxBytesReader(nil, r.Body, infolder.MaxChunk+1))
		}
		writeJSONError(w, code, msg)
	}
	u, tokenID, err := s.in.UploadByTicket(id, auth.ExtractBearer(r.Header.Get("Authorization")))
	if err != nil {
		refuse(http.StatusUnauthorized, "no upload for this ticket: send the ticket of memory_in_upload as the bearer (open the upload again for a new one)")
		return
	}
	tok := s.tokenByID(tokenID)
	if tok == nil {
		refuse(http.StatusForbidden, "the token that opened this upload no longer exists")
		return
	}
	if !tok.HasScope(auth.ScopeWrite) || !tok.AllowsPath(vault.INRoot(u.Project)) || !tok.AllowsWrite(vault.INRoot(u.Project)) ||
		s.projectHidden(u.Project) || s.projects == nil || !s.projects.UsesIN(u.Project) {
		refuse(http.StatusForbidden, "the token that opened this upload may not write the IN folder of "+u.Project+" any more")
		return
	}
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		got, err := s.in.GetUpload(u.Project, id, u.OwnerID, r.URL.Query().Get("files") != "false")
		if err != nil {
			writeINUploadError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, got)
	case len(parts) == 2 && parts[1] == "complete" && r.Method == http.MethodPost:
		var body struct {
			Policy string `json:"policy"`
			Drop   []int  `json:"drop"`
		}
		if r.ContentLength != 0 {
			if err := decodeSmallJSON(r, &body); err != nil {
				writeJSONError(w, http.StatusBadRequest, "the body is {policy, drop}: "+err.Error())
				return
			}
		}
		got, err := s.in.CompleteUpload(u.Project, id, u.OwnerID, body.Policy, body.Drop)
		if err != nil {
			writeINUploadError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, got)
	case len(parts) == 3 && parts[1] == "files" && r.Method == http.MethodPut:
		n, err := strconv.Atoi(parts[2])
		if err != nil || n < 0 {
			refuse(http.StatusBadRequest, "the file is its number n in the upload")
			return
		}
		offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
		if err != nil || offset < 0 {
			refuse(http.StatusBadRequest, "offset is the bytes of the file received so far")
			return
		}
		if r.ContentLength > infolder.MaxChunk {
			writeJSONError(w, http.StatusRequestEntityTooLarge, "a piece is at most "+strconv.Itoa(infolder.MaxChunk)+" bytes")
			return
		}
		body := http.MaxBytesReader(w, r.Body, infolder.MaxChunk+1)
		f, err := s.in.WriteChunk(u.Project, id, u.OwnerID, n, offset, body)
		_, _ = io.Copy(io.Discard, body)
		if err != nil {
			writeINUploadError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"n": f.N, "received": f.Received, "size": f.Size, "sha256": f.SHA256})
	case len(parts) <= 3:
		writeJSONError(w, http.StatusMethodNotAllowed, "PUT …/files/<n>?offset=X, GET the upload, or POST …/complete")
	default:
		writeJSONError(w, http.StatusNotFound, "unknown upload endpoint")
	}
}

// writeINUploadError answers an error of the uploads as the REST API does.
func writeINUploadError(w http.ResponseWriter, err error) {
	var off *infolder.OffsetError
	var inc *infolder.IncompleteError
	var maxErr *http.MaxBytesError
	switch {
	case errors.As(err, &off):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "received": off.Received})
	case errors.As(err, &inc):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "files": inc.Files})
	case errors.Is(err, infolder.ErrUploadNotFound), errors.Is(err, fs.ErrNotExist):
		writeJSONError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, infolder.ErrNoSpace):
		writeJSONError(w, http.StatusInsufficientStorage, err.Error())
	case errors.Is(err, infolder.ErrTooLarge), errors.As(err, &maxErr):
		writeJSONError(w, http.StatusRequestEntityTooLarge, err.Error())
	case errors.Is(err, infolder.ErrBadUpload), errors.Is(err, vault.ErrNotIN):
		writeJSONError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, infolder.ErrUploadState):
		writeJSONError(w, http.StatusConflict, err.Error())
	default:
		writeJSONError(w, http.StatusInternalServerError, err.Error())
	}
}

// decodeSmallJSON reads a JSON body of at most 1 MiB.
func decodeSmallJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
