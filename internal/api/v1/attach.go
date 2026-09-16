package v1

import (
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gosidian/gosidian/internal/attach"
)

// uploadResponse is the rich shape returned by /api/v1/upload —
// designed for agents and SPA flows that need to construct an embed
// later (or skip it). The /attach response is a strict subset
// (just `markdown`) to keep the editor paste/drop path tight.
type uploadResponse struct {
	Path             string `json:"path"`
	URL              string `json:"url"`
	MIME             string `json:"mime"`
	Kind             string `json:"kind"`
	Size             int    `json:"size"`
	OriginalFilename string `json:"original_filename"`
	Hash             string `json:"hash"`
}

type attachResponse struct {
	Path     string `json:"path"`
	Filename string `json:"filename"`
	Markdown string `json:"markdown"`
}

// handleAttach mirrors the v1.x /api/attach route used by the editor
// paste/drop flow. Multipart upload, optional ?project= (defaults to
// the active project tracked client-side, but null is fine — Store
// drops the file in the vault root attachments/ if absent), returns
// the markdown embed string ready to splice into the editor.
func (r *Router) handleAttach(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	if denyGuestWrite(w, UserFromContext(req.Context())) {
		return
	}
	project := strings.TrimSpace(req.URL.Query().Get("project"))
	if r.denyWriteProject(w, principalFromContext(req), project) {
		return
	}
	data, header, errCode, errMsg := r.readAttachUpload(w, req)
	if errCode != 0 {
		WriteError(w, errCode, CodeValidationFormat, errMsg)
		return
	}
	res, err := attach.Store(r.deps.Vault, data, header.Filename, project)
	if err != nil {
		if strings.Contains(err.Error(), "MIME mismatch") {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
			return
		}
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "save: "+err.Error())
		return
	}
	WriteJSON(w, http.StatusCreated, attachResponse{
		Path:     res.Path,
		Filename: res.Filename,
		Markdown: res.Markdown,
	})
}

// handleUpload is the agent-friendly upload path — same storage
// pipeline as /attach but the response carries metadata (mime, kind,
// hash) instead of the editor-ready markdown so a multi-step flow
// (upload → enrich → attach) is possible. project= is required;
// uploads outside a project don't make sense here.
func (r *Router) handleUpload(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	if denyGuestWrite(w, UserFromContext(req.Context())) {
		return
	}
	project := strings.TrimSpace(req.URL.Query().Get("project"))
	if project == "" {
		WriteError(w, http.StatusBadRequest, CodeValidationRequired, "project query param is required")
		return
	}
	if r.denyWriteProject(w, principalFromContext(req), project) {
		return
	}
	data, header, errCode, errMsg := r.readAttachUpload(w, req)
	if errCode != 0 {
		WriteError(w, errCode, CodeValidationFormat, errMsg)
		return
	}
	ext := strings.ToLower(filepath.Ext(header.Filename))
	mime, isImage := header.MIME, header.IsImage
	res, err := attach.Store(r.deps.Vault, data, header.Filename, project)
	if err != nil {
		if strings.Contains(err.Error(), "MIME mismatch") {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
			return
		}
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "save: "+err.Error())
		return
	}
	kind := "document"
	if isImage {
		kind = "image"
	}
	WriteJSON(w, http.StatusCreated, uploadResponse{
		Path:             res.Path,
		URL:              "/vault-files/" + res.Path,
		MIME:             mime,
		Kind:             kind,
		Size:             len(data),
		OriginalFilename: header.Filename,
		Hash:             strings.TrimSuffix(res.Filename, ext),
	})
}

// readAttachUpload parses the multipart body, validates the file
// field, and enforces the size cap from internal/attach. Returns
// (errCode, errMsg) when the request should fail before the call
// reaches storage; both are zero on success.
func (r *Router) readAttachUpload(w http.ResponseWriter, req *http.Request) (data []byte, header *attachHeader, errCode int, errMsg string) {
	// Cap the body itself before parsing: ParseMultipartForm's argument only
	// bounds in-memory buffering and spools the rest to disk, so without
	// MaxBytesReader the size check below runs after the whole upload landed.
	req.Body = http.MaxBytesReader(w, req.Body, attach.MaxBytes+(1<<20))
	if err := req.ParseMultipartForm(attach.MaxBytes + (1 << 20)); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return nil, nil, http.StatusRequestEntityTooLarge, "request body too large (max 10 MiB file)"
		}
		return nil, nil, http.StatusBadRequest, "bad multipart: " + err.Error()
	}
	file, h, err := req.FormFile("file")
	if err != nil {
		return nil, nil, http.StatusBadRequest, "missing file field: " + err.Error()
	}
	defer file.Close()

	limited := io.LimitReader(file, attach.MaxBytes+1)
	read, err := io.ReadAll(limited)
	if err != nil {
		return nil, nil, http.StatusInternalServerError, "read: " + err.Error()
	}
	if int64(len(read)) > attach.MaxBytes {
		return nil, nil, http.StatusRequestEntityTooLarge, "file too large (max 10 MiB)"
	}
	ext := strings.ToLower(filepath.Ext(h.Filename))
	mime, isImage, err := attach.ValidateExt(ext)
	if err != nil {
		return nil, nil, http.StatusUnsupportedMediaType, err.Error()
	}
	return read, &attachHeader{Filename: h.Filename, Size: h.Size, MIME: mime, IsImage: isImage}, 0, ""
}

// attachHeader is a minimal projection of multipart.FileHeader so the
// helper above can return without leaking the multipart import into
// callers.
type attachHeader struct {
	Filename string
	Size     int64
	MIME     string // from the validated extension
	IsImage  bool
}
