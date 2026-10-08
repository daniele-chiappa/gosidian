package v1

import (
	"archive/zip"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/audit"
)

// Zip exports (IMP-099): the archive is built on the fly from every file
// of a project or of the whole vault, so each download reads all of it.
// exportMax per exportWindow and per account is far above any human use
// and stops a script from looping on it.
const (
	exportWindow = 10 * time.Minute
	exportMax    = 10
)

// storedExts are formats that are already compressed: deflating them again
// costs CPU for nothing, so they go into the archive as they are.
var storedExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true,
	".zip": true, ".gz": true, ".pdf": true, ".docx": true, ".xlsx": true,
	".pptx": true, ".mp3": true, ".mp4": true, ".webm": true, ".ogg": true,
}

// handleProjectExport serves GET /projects/{name}/export.zip: every file of
// the project for an account that can read it.
func (r *Router) handleProjectExport(w http.ResponseWriter, req *http.Request, project string) {
	user, ok := r.exportUser(w, req)
	if !ok {
		return
	}
	if !r.canAccessProject(user.principal(), project) || !r.projectExists(project) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "project not found")
		return
	}
	r.streamExport(w, req, user, project, project+"-"+time.Now().UTC().Format("20060102")+".zip")
}

// handleVaultExport serves GET /admin/export.zip (owner only): every file of
// the vault.
func (r *Router) handleVaultExport(w http.ResponseWriter, req *http.Request) {
	user, ok := r.exportUser(w, req)
	if !ok {
		return
	}
	r.streamExport(w, req, user, "", "vault-"+time.Now().UTC().Format("20060102")+".zip")
}

// exportUser checks what both exports share: a GET from a signed-in account
// under its rate limit. The open-mode guest may read public projects note
// by note but not download them in bulk.
func (r *Router) exportUser(w http.ResponseWriter, req *http.Request) (*RequestUser, bool) {
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return nil, false
	}
	user := UserFromContext(req.Context())
	if user == nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "no user in context")
		return nil, false
	}
	if user.isAnonymous() {
		WriteError(w, http.StatusForbidden, CodeAuthForbidden, "sign in to export")
		return nil, false
	}
	if !r.exportLimiter.Allow(user.ID, time.Now()) {
		w.Header().Set("Retry-After", strconv.Itoa(int(exportWindow.Seconds())))
		WriteError(w, http.StatusTooManyRequests, CodeRateLimit, "too many exports, retry later")
		return nil, false
	}
	return user, true
}

// streamExport writes the zip of dir ("" = the vault) as it walks it: the
// server holds one file at a time, whatever the size of the archive.
func (r *Router) streamExport(w http.ResponseWriter, req *http.Request, user *RequestUser, dir, filename string) {
	// The walk leaves out the state dir only below where it starts: started
	// at the state dir itself, set inside the vault under a visible name, it
	// would zip the credentials.
	if dir != "" && r.inStateDir(dir) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "folder not found")
		return
	}
	// Names in the archive start at the exported folder, as for a project
	// (its folder is at the vault root): docs/… for Work/a/docs.
	strip := ""
	if parent := path.Dir(dir); dir != "" && parent != "." {
		strip = parent + "/"
	}
	h := w.Header()
	attach.SetInertHeaders(h)
	h.Set("Content-Type", "application/zip")
	// FormatMediaType quotes the name and switches to filename* (RFC 2231)
	// when a project name is not plain ASCII.
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	h.Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)

	cw := &countingWriter{w: w}
	zw := zip.NewWriter(cw)
	files, skipped := 0, 0
	err := r.deps.Vault.WalkExport(dir, []string{r.deps.StateDir}, func(rel string, info fs.FileInfo) error {
		if err := req.Context().Err(); err != nil {
			return err // the client went away: stop reading the vault
		}
		// A backslash is a legal byte in a Linux file name but a separator
		// for Windows extractors, some of which would then honour ".."
		// segments in it and write outside the target folder.
		if strings.ContainsRune(rel, '\\') {
			skipped++
			slog.Default().Warn("api/v1: export skips a name with a backslash", "path", rel)
			return nil
		}
		files++
		return addZipFile(zw, filepath.Join(r.deps.Vault.Root, filepath.FromSlash(rel)), strings.TrimPrefix(rel, strip), info)
	})
	if err == nil {
		err = zw.Close()
	}
	// Individual reads are not audited; a bulk copy is, like mirror_sync:
	// Path = the project ("" for the vault), Size = bytes sent.
	r.auditNote(req, audit.ActionExport, user, dir, "", cw.n)
	// dir comes from the URL. It names an existing project, but the newline
	// strip is the sanitizer CodeQL's go/log-injection model recognizes.
	logDir := strings.ReplaceAll(strings.ReplaceAll(dir, "\n", ""), "\r", "")
	slog.Default().Info("api/v1: export", "dir", logDir, "user", user.Username, "files", files, "skipped", skipped, "bytes", cw.n, "ok", err == nil)
	if err != nil {
		slog.Default().Warn("api/v1: export failed", "dir", logDir, "user", user.Username, "err", err)
		// The 200 is already out. Returning would end the chunked body
		// cleanly and the client would save a truncated zip as if it were
		// complete; aborting the connection makes the download fail.
		panic(http.ErrAbortHandler)
	}
}

// exportOpen opens a file to copy into an archive; tests swap it to make a
// file unreadable (they run as root, where a chmod does not).
var exportOpen = os.Open

func addZipFile(zw *zip.Writer, abs, rel string, info fs.FileInfo) error {
	fh, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	fh.Name = rel
	fh.Method = zip.Deflate
	if storedExts[strings.ToLower(filepath.Ext(rel))] {
		fh.Method = zip.Store
	}
	dst, err := zw.CreateHeader(fh)
	if err != nil {
		return err
	}
	f, err := exportOpen(abs)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(dst, f)
	return err
}

// countingWriter counts the bytes that reach the client, for the audit.
type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
