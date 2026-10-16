package v1

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/jobs"
	"github.com/gosidian/gosidian/internal/pipeline"
)

// The pipelines over REST (IMP-166, IMP-167, ADR-035):
//
//	GET  /api/v1/pipelines?project=          the pipelines the caller reads
//	GET  /api/v1/pipelines/<path>/runs       the runs of one
//	POST /api/v1/pipelines/<path>/runs       start it: a session with write
//	                                         access, or a run token; with
//	                                         wait (seconds, or ?wait=), the
//	                                         answer once the run ended
//	GET  /api/v1/runs?project=&status=       the runs the caller reads
//	GET  /api/v1/runs/<id>                   a run with its steps
//	POST /api/v1/runs/<id>/resume[?key=]     hand data to a waiting step: the
//	                                         run's resume key, or a writer
//	POST /api/v1/runs/<id>/cancel            stop it (a writer)
//
// A run token (gosidian_run_…, `gosidian pipeline token create`) starts its
// one pipeline, when that starts on external or manual, and does nothing
// else. The resume key resumes its one run while it waits.

const (
	// runTokenWindow, runTokenMax: the starts of one run token, and the
	// resumes of one run by its key; [pipelines] run_token_rate sets the
	// number when the pipelines are on.
	runTokenWindow = time.Minute
	runTokenMax    = 60
	// maxStepOutputShown caps a step's output in a run's view.
	maxStepOutputShown = 64 << 10
)

func (r *Router) pipelinesOff(w http.ResponseWriter) bool {
	if r.deps.Pipelines != nil {
		return false
	}
	WriteError(w, http.StatusNotFound, CodeNotFound, "pipelines are off on this server")
	return true
}

// pipelineError maps an engine refusal onto a status.
func pipelineError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pipeline.ErrNoNote), errors.Is(err, jobs.ErrNotFound):
		WriteError(w, http.StatusNotFound, CodeNotFound, err.Error())
	case errors.Is(err, jobs.ErrState), errors.Is(err, pipeline.ErrDisabled):
		WriteError(w, http.StatusConflict, CodeConflict, err.Error())
	default:
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
	}
}

// handlePipelines lists the pipelines of the projects the caller reads.
func (r *Router) handlePipelines(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	if r.pipelinesOff(w) {
		return
	}
	project := strings.TrimSpace(req.URL.Query().Get("project"))
	p := principalFromContext(req)
	if project != "" && !r.canAccessProject(p, project) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "no such project")
		return
	}
	ps, err := r.deps.Pipelines.Pipelines(project)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	seen := ps[:0]
	for _, pl := range ps {
		if r.canSee(p, pl.Path) {
			seen = append(seen, pl)
		}
	}
	WriteJSON(w, http.StatusOK, map[string]any{"pipelines": r.deps.Pipelines.Infos(seen)})
}

// pipelineRoutes serves /api/v1/pipelines/<path>/runs: a POST with a run
// token is authenticated here, everything else goes through authed.
func (r *Router) pipelineRoutes(authed func(http.HandlerFunc) http.Handler) http.Handler {
	session := authed(r.handlePipelineRuns)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost && strings.HasPrefix(extractBearer(req), jobs.TokenPrefix) {
			r.startWithRunToken(w, req)
			return
		}
		session.ServeHTTP(w, req)
	})
}

// pipelinePath is the pipeline of /api/v1/pipelines/<path>/runs.
func pipelinePath(req *http.Request) (string, bool) {
	rest := strings.TrimPrefix(req.URL.Path, "/api/v1/pipelines/")
	p, ok := strings.CutSuffix(rest, "/runs")
	if !ok || p == "" || !strings.HasSuffix(p, ".md") {
		return "", false
	}
	return p, true
}

func (r *Router) handlePipelineRuns(w http.ResponseWriter, req *http.Request) {
	if r.pipelinesOff(w) {
		return
	}
	path, ok := pipelinePath(req)
	if !ok {
		WriteError(w, http.StatusNotFound, CodeNotFound, "use /api/v1/pipelines/<path.md>/runs")
		return
	}
	p := principalFromContext(req)
	if !r.canSee(p, path) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "no such pipeline")
		return
	}
	switch req.Method {
	case http.MethodGet:
		limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
		runs, err := r.deps.Pipelines.Runner().Store().Runs(jobs.RunFilter{Ref: path, Limit: limit})
		if err != nil {
			WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"pipeline": path, "runs": pipeline.Briefs(runs)})
	case http.MethodPost:
		user := UserFromContext(req.Context())
		if denyGuestWrite(w, user) || r.denyWriteProject(w, p, projectOf(path)) {
			return
		}
		r.startRun(w, req, path, pipeline.TriggerManual, user.Username, user.ID)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
	}
}

func (r *Router) startWithRunToken(w http.ResponseWriter, req *http.Request) {
	if r.pipelinesOff(w) {
		return
	}
	tok, err := r.deps.Pipelines.Runner().Store().CheckToken(extractBearer(req))
	if err != nil {
		WriteError(w, http.StatusUnauthorized, CodeAuthTokenInvalid, "invalid run token")
		return
	}
	path, ok := pipelinePath(req)
	if !ok || path != tok.Ref {
		WriteError(w, http.StatusForbidden, CodeAuthForbidden, "this run token starts "+tok.Ref+" only")
		return
	}
	if !r.runLimiter.Allow("token:"+tok.ID, time.Now()) {
		w.Header().Set("Retry-After", strconv.Itoa(int(runTokenWindow.Seconds())))
		WriteError(w, http.StatusTooManyRequests, CodeRateLimit, "too many starts with this run token")
		return
	}
	r.startRun(w, req, path, pipeline.TriggerExternal, "run-token:"+tok.Name, "")
}

type startRunRequest struct {
	Input          any    `json:"input"`
	IdempotencyKey string `json:"idempotency_key"`
	CallbackURL    string `json:"callback_url"`
	// Wait is how many seconds to wait for the run to end, at most
	// [pipelines] max_sync_wait: the answer comes back with it.
	Wait float64 `json:"wait"`
}

func (r *Router) startRun(w http.ResponseWriter, req *http.Request, path, trigger, actor, userID string) {
	var body startRunRequest
	if req.ContentLength != 0 {
		if err := DecodeJSON(req, &body); err != nil && !errors.Is(err, io.EOF) {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
			return
		}
	}
	if body.Input != nil {
		if _, ok := body.Input.(map[string]any); !ok {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, "input is a JSON object")
			return
		}
	}
	key := strings.TrimSpace(body.IdempotencyKey)
	if key == "" {
		key = strings.TrimSpace(req.Header.Get("Idempotency-Key"))
	}
	run, existing, err := r.deps.Pipelines.Start(pipeline.StartRequest{
		Path: path, Trigger: trigger, Input: body.Input, Actor: actor,
		IdemKey: key, CallbackURL: strings.TrimSpace(body.CallbackURL),
	})
	if err != nil {
		pipelineError(w, err)
		return
	}
	status := http.StatusOK
	if !existing {
		status = http.StatusAccepted
		_ = r.deps.Audit.Write(audit.Entry{Source: audit.SourceHTTP, Actor: actor, UserID: userID, Action: audit.ActionPipelineRun, Path: path, To: run.ID})
	}
	wait := body.Wait
	if q := req.URL.Query().Get("wait"); q != "" {
		v, err := strconv.ParseFloat(q, 64)
		if err != nil || v < 0 {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, "wait is a number of seconds")
			return
		}
		wait = v
	}
	out := map[string]any{"existing": existing}
	// A start that waits for its run: 200 with the answer once it ended,
	// or once it waits for a call; 202 as before when the time passed.
	if d := r.deps.Pipelines.SyncWait(time.Duration(wait * float64(time.Second))); d > 0 {
		if now, err := r.deps.Pipelines.Wait(req.Context(), run.ID, d); err == nil {
			run = now
		}
		if r.deps.Pipelines.Settled(run) {
			status = http.StatusOK
			if steps, err := r.deps.Pipelines.Runner().Store().Steps(run.ID); err == nil {
				if answer, ok := pipeline.AnswerOf(run, steps); ok {
					out["answer"] = answer
				}
			}
		}
	}
	out["run"] = pipeline.Brief(run)
	WriteJSON(w, status, out)
}

// handleRuns lists the runs of the projects the caller reads.
func (r *Router) handleRuns(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	if r.pipelinesOff(w) {
		return
	}
	q := req.URL.Query()
	scope, err := r.searchScope(principalFromContext(req), strings.TrimSpace(q.Get("project")))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	runs, err := r.deps.Pipelines.Runner().Store().Runs(jobs.RunFilter{
		Ref: strings.TrimSpace(q.Get("pipeline")), Projects: scope, Status: jobs.Status(strings.TrimSpace(q.Get("status"))), Limit: limit,
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"runs": pipeline.Briefs(runs)})
}

// runRoutes serves /api/v1/runs/<id>[/resume|/cancel]: a resume with the
// run's key is authenticated here, everything else goes through authed.
func (r *Router) runRoutes(authed func(http.HandlerFunc) http.Handler) http.Handler {
	session := authed(r.handleRun)
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodPost && req.URL.Query().Get("key") != "" {
			r.resumeWithKey(w, req)
			return
		}
		session.ServeHTTP(w, req)
	})
}

func runPath(req *http.Request) (id, action string) {
	rest := strings.Trim(strings.TrimPrefix(req.URL.Path, "/api/v1/runs/"), "/")
	id, action, _ = strings.Cut(rest, "/")
	return id, action
}

func (r *Router) handleRun(w http.ResponseWriter, req *http.Request) {
	if r.pipelinesOff(w) {
		return
	}
	id, action := runPath(req)
	store := r.deps.Pipelines.Runner().Store()
	run, err := store.Run(id)
	p := principalFromContext(req)
	if err != nil || !r.canSee(p, run.Ref) {
		WriteError(w, http.StatusNotFound, CodeNotFound, "no such run")
		return
	}
	switch {
	case action == "" && req.Method == http.MethodGet:
		steps, err := store.Steps(id)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]any{"run": pipeline.Brief(run), "steps": pipeline.StepViews(run, steps, maxStepOutputShown)})
	case (action == "resume" || action == "cancel") && req.Method == http.MethodPost:
		user := UserFromContext(req.Context())
		if denyGuestWrite(w, user) || r.denyWriteProject(w, p, run.Project) {
			return
		}
		if action == "cancel" {
			if err := r.deps.Pipelines.Runner().Cancel(id); err != nil {
				pipelineError(w, err)
				return
			}
			_ = r.deps.Audit.Write(audit.Entry{Source: audit.SourceHTTP, Actor: user.Username, UserID: user.ID, Action: audit.ActionPipelineCancel, Path: run.Ref, To: id})
			r.writeRun(w, id)
			return
		}
		r.resume(w, req, run, user.Username, user.ID)
	case action == "" || action == "resume" || action == "cancel":
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
	default:
		WriteError(w, http.StatusNotFound, CodeNotFound, "use /api/v1/runs/<id>, /resume or /cancel")
	}
}

func (r *Router) resumeWithKey(w http.ResponseWriter, req *http.Request) {
	if r.pipelinesOff(w) {
		return
	}
	id, action := runPath(req)
	run, err := r.deps.Pipelines.Runner().Store().Run(id)
	key := req.URL.Query().Get("key")
	if action != "resume" || err != nil || run.ResumeKey == "" || subtle.ConstantTimeCompare([]byte(key), []byte(run.ResumeKey)) != 1 {
		WriteError(w, http.StatusNotFound, CodeNotFound, "no such run, or not this key")
		return
	}
	if !r.runLimiter.Allow("resume:"+id, time.Now()) {
		w.Header().Set("Retry-After", strconv.Itoa(int(runTokenWindow.Seconds())))
		WriteError(w, http.StatusTooManyRequests, CodeRateLimit, "too many resumes of this run")
		return
	}
	r.resume(w, req, run, "resume-key", "")
}

// resume hands the body, any JSON value (an empty body is {}), to the
// waiting step named by ?step= (needed when more than one waits).
func (r *Router) resume(w http.ResponseWriter, req *http.Request, run jobs.Run, actor, userID string) {
	max := r.deps.Pipelines.Config().MaxInputBytes
	data, err := io.ReadAll(http.MaxBytesReader(w, req.Body, int64(max)+1))
	if err != nil || len(data) > max {
		WriteError(w, http.StatusRequestEntityTooLarge, CodeValidationFormat, "the data is larger than "+strconv.Itoa(max)+" bytes")
		return
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		data = []byte("{}")
	}
	if !json.Valid(data) {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, "the body is not JSON")
		return
	}
	if err := r.deps.Pipelines.Runner().Resume(run.ID, strings.TrimSpace(req.URL.Query().Get("step")), data); err != nil {
		pipelineError(w, err)
		return
	}
	_ = r.deps.Audit.Write(audit.Entry{Source: audit.SourceHTTP, Actor: actor, UserID: userID, Action: audit.ActionPipelineResume, Path: run.Ref, To: run.ID})
	r.writeRun(w, run.ID)
}

func (r *Router) writeRun(w http.ResponseWriter, id string) {
	run, err := r.deps.Pipelines.Runner().Store().Run(id)
	if err != nil {
		pipelineError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"run": pipeline.Brief(run)})
}

// arrived starts the ingest pipelines of the attachment's project for a
// document just uploaded from the web UI (IMP-167).
func (r *Router) arrived(req *http.Request, att, name string) {
	if r.deps.Pipelines == nil {
		return
	}
	actor := ""
	if u := UserFromContext(req.Context()); u != nil {
		actor = u.Username
	}
	r.deps.Pipelines.OnAttachment(projectOf(att), att, filepath.Base(name), "", actor)
}
