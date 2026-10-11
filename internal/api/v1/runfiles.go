package v1

import (
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/attach"
	"github.com/gosidian/gosidian/internal/pipeline"
)

// handleRunFile serves a file a plugin node got by reference (IMP-172) to
// the plugin: no account, the ticket in the address is the key, for the
// run's project's file it names, while the step may run. Ranges and
// conditional requests work (http.ServeContent).
func (r *Router) handleRunFile(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	id := strings.TrimPrefix(req.URL.Path, pipeline.FileTicketPath)
	if r.deps.Pipelines == nil || id == "" || strings.Contains(id, "/") {
		WriteError(w, http.StatusNotFound, CodeNotFound, "no such file")
		return
	}
	if !r.runLimiter.Allow("file:"+id, time.Now()) {
		w.Header().Set("Retry-After", strconv.Itoa(int(runTokenWindow.Seconds())))
		WriteError(w, http.StatusTooManyRequests, CodeRateLimit, "too many requests for this file")
		return
	}
	f, st, ct, err := r.deps.Pipelines.OpenTicketFile(id)
	if err != nil {
		WriteError(w, http.StatusNotFound, CodeNotFound, "no such file")
		return
	}
	defer f.Close()
	h := w.Header()
	attach.SetInertHeaders(h)
	h.Set("Content-Type", ct)
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": st.Name}))
	h.Set("Cache-Control", "no-store")
	http.ServeContent(w, req, st.Name, st.ModTime, f)
}
