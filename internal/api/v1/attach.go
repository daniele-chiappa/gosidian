package v1

import (
	"errors"
	"io"
	"net/http"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/uploadquota"
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

// storeAttachment stores data as an attachment of project and records it in
// the audit log, as the MCP uploads are, while its lock is held: git sync
// names its writer, and leaves the file for the commit after the record
// (BUG-148). Neither route wrote an entry.
func (r *Router) storeAttachment(req *http.Request, data []byte, filename, project string) (*attach.Result, error) {
	held := attach.Hold(r.deps.Vault, r.deps.Vault)
	defer held.Release()
	res, err := attach.Store(held, data, filename, project)
	if err != nil {
		return nil, err
	}
	r.auditNote(req, audit.ActionUploadAttachment, UserFromContext(req.Context()), res.Path, "", int64(len(data)))
	return res, nil
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
	refund, ok := r.reserveUpload(w, req, len(data))
	if !ok {
		return
	}
	res, err := r.storeAttachment(req, data, header.Filename, project)
	if err != nil {
		refund()
		if strings.Contains(err.Error(), "MIME mismatch") {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
			return
		}
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "save: "+err.Error())
		return
	}
	r.arrived(req, res.Path, header.Filename)
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
	refund, ok := r.reserveUpload(w, req, len(data))
	if !ok {
		return
	}
	res, err := r.storeAttachment(req, data, header.Filename, project)
	if err != nil {
		refund()
		if strings.Contains(err.Error(), "MIME mismatch") {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
			return
		}
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "save: "+err.Error())
		return
	}
	r.arrived(req, res.Path, header.Filename)
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
	req.Body = http.MaxBytesReader(w, req.Body, attach.MaxBytes()+(1<<20))
	if err := req.ParseMultipartForm(attach.MaxBytes() + (1 << 20)); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return nil, nil, http.StatusRequestEntityTooLarge, "request body too large (max " + attach.MaxSize() + " file)"
		}
		return nil, nil, http.StatusBadRequest, "bad multipart: " + err.Error()
	}
	file, h, err := req.FormFile("file")
	if err != nil {
		return nil, nil, http.StatusBadRequest, "missing file field: " + err.Error()
	}
	defer file.Close()

	limited := io.LimitReader(file, attach.MaxBytes()+1)
	read, err := io.ReadAll(limited)
	if err != nil {
		return nil, nil, http.StatusInternalServerError, "read: " + err.Error()
	}
	if int64(len(read)) > attach.MaxBytes() {
		return nil, nil, http.StatusRequestEntityTooLarge, attach.TooLarge()
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

// reserveUpload counts an upload of n bytes against the account's upload
// quota, shared with its MCP tokens (IMP-034). When it does not fit it
// answers 429 with Retry-After (413 when n alone is over the quota) and
// returns false; otherwise refund gives the bytes back after a failed store.
func (r *Router) reserveUpload(w http.ResponseWriter, req *http.Request, n int) (refund func(), ok bool) {
	userID := ""
	if u := UserFromContext(req.Context()); u != nil {
		userID = u.ID
	}
	key := uploadquota.Key(userID, "")
	refusal := r.deps.UploadQuota.Reserve(key, int64(n))
	if refusal == nil {
		return func() { r.deps.UploadQuota.Refund(key, int64(n)) }, true
	}
	if refusal.Wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int((refusal.Wait+time.Second-1)/time.Second)))
		WriteError(w, http.StatusTooManyRequests, CodeRateLimit, refusal.Error())
	} else {
		WriteError(w, http.StatusRequestEntityTooLarge, CodeValidationFormat, refusal.Error())
	}
	return nil, false
}

// attachmentView is an attachment of a project, for a picker.
type attachmentView struct {
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	Image    bool      `json:"image"`
}

// handleAttachmentList answers GET /api/v1/attachments?project=<p>: the
// attachments of a project the caller may read, newest first, the images
// only with kind=image. The picture a page layout places is chosen among
// them (IMP-226).
func (r *Router) handleAttachmentList(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	project := req.URL.Query().Get("project")
	if project == "" || strings.Contains(project, "/") {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "project: the project whose attachments to list")
		return
	}
	if !r.canSeeProject(req, project) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "project not found")
		return
	}
	infos, err := r.deps.Vault.ListAttachments(project, attach.ExtSet())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, "list: "+err.Error())
		return
	}
	images := req.URL.Query().Get("kind") == "image"
	out := []attachmentView{}
	for _, a := range infos {
		_, isImage, err := attach.ValidateExt(strings.ToLower(path.Ext(a.Path)))
		if err != nil || (images && !isImage) {
			continue
		}
		out = append(out, attachmentView{Path: a.Path, Size: a.Size, Modified: a.ModTime, Image: isImage})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	writeJSON(w, http.StatusOK, map[string]any{"attachments": out})
}
