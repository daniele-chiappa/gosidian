package v1

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
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
//	                                         answer once the run ended; with
//	                                         Accept: text/event-stream, the
//	                                         text of its streaming nodes as
//	                                         it comes, then the answer
//	GET  /api/v1/runs?project=&status=       the runs the caller reads
//	GET  /api/v1/runs/queue?project=         the queue of the steps (IMP-196)
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
	var full pipeline.ErrCategoryFull
	switch {
	case errors.As(err, &full):
		w.Header().Set("Retry-After", strconv.Itoa(int(full.RetryAfter.Seconds())))
		WriteError(w, http.StatusTooManyRequests, CodeRateLimit, err.Error())
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
		runs, err := r.deps.Pipelines.Runner().Store().Runs(jobs.RunFilter{Kind: pipeline.Kind, Ref: path, Limit: limit})
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
	// [pipelines] max_sync_wait: the answer comes back with it. Unset, the
	// wait of the pipeline's category (IMP-196).
	Wait *float64 `json:"wait"`
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
	wait := -1.0
	if body.Wait != nil {
		if *body.Wait < 0 {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, "wait is a number of seconds")
			return
		}
		wait = *body.Wait
	}
	if q := req.URL.Query().Get("wait"); q != "" {
		v, err := strconv.ParseFloat(q, 64)
		if err != nil || v < 0 {
			WriteError(w, http.StatusBadRequest, CodeValidationFormat, "wait is a number of seconds")
			return
		}
		wait = v
	}
	events := wantsEvents(req)
	switch {
	case events && wait <= 0:
		// A stream follows the run as long as the server waits for one.
		wait = math.MaxInt32
	case wait < 0:
		// Unsaid: the category's wait.
		wait = r.deps.Pipelines.DefaultWait(path).Seconds()
	}
	sreq := pipeline.StartRequest{
		Path: path, Trigger: trigger, Input: body.Input, Actor: actor,
		IdemKey: key, CallbackURL: strings.TrimSpace(body.CallbackURL),
	}
	var (
		run      jobs.Run
		existing bool
		stream   *pipeline.Stream
		err      error
	)
	if events {
		run, existing, stream, err = r.deps.Pipelines.StartStream(sreq)
	} else {
		run, existing, err = r.deps.Pipelines.Start(sreq)
	}
	if err != nil {
		pipelineError(w, err)
		return
	}
	status := http.StatusOK
	if !existing {
		status = http.StatusAccepted
		_ = r.deps.Audit.Write(audit.Entry{Source: audit.SourceHTTP, Actor: actor, UserID: userID, Action: audit.ActionPipelineRun, Path: path, To: run.ID})
	}
	d := r.deps.Pipelines.SyncWait(time.Duration(wait * float64(time.Second)))
	if events {
		defer r.deps.Pipelines.CloseStream(stream)
		r.streamRun(w, req, run, existing, stream, d)
		return
	}
	// A start that waits for its run: 200 with the answer once it ended,
	// or once it waits for a call; 202 as before when the time passed.
	if d > 0 {
		if now, err := r.deps.Pipelines.Wait(req.Context(), run.ID, d); err == nil {
			run = now
		}
		if r.deps.Pipelines.Settled(run) {
			status = http.StatusOK
		}
	}
	WriteJSON(w, status, r.runAnswer(run, existing, d > 0))
}

// runAnswer is what a start says of its run: the run, and its answer when
// the start waited and the run settled.
func (r *Router) runAnswer(run jobs.Run, existing, waited bool) map[string]any {
	out := map[string]any{"existing": existing, "run": pipeline.Brief(run)}
	if waited && r.deps.Pipelines.Settled(run) {
		if steps, err := r.deps.Pipelines.Runner().Store().Steps(run.ID); err == nil {
			if answer, ok := pipeline.AnswerOf(run, steps); ok {
				out["answer"] = answer
			}
		}
	}
	return out
}

// wantsEvents says whether a start asks to follow its run as a stream.
func wantsEvents(req *http.Request) bool {
	return strings.Contains(req.Header.Get("Accept"), "text/event-stream")
}

// runStreamHeartbeat keeps a quiet stream open through the proxies.
const runStreamHeartbeat = 15 * time.Second

// streamRun answers a start that asked for text/event-stream (IMP-191
// phase 8): an event run with the run, delta and reset events with the
// text its streaming nodes write (and those of the runs it starts), then
// an event answer with what a start that waits answers, and the status it
// answers with, once the run settled or d passed.
func (r *Router) streamRun(w http.ResponseWriter, req *http.Request, run jobs.Run, existing bool, stream *pipeline.Stream, d time.Duration) {
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	applySecurityHeaders(w)
	w.WriteHeader(http.StatusOK)
	send := func(event string, v any) bool {
		b, err := json.Marshal(v)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	var cur pipeline.StreamCursor
	text := func() (<-chan struct{}, bool) {
		evs, wake := stream.Read(&cur)
		for _, ev := range evs {
			if !send(ev.Kind, ev) {
				return nil, false
			}
		}
		return wake, true
	}
	if !send("run", map[string]any{"existing": existing, "run": pipeline.Brief(run)}) {
		return
	}
	answer := func(now jobs.Run) {
		if _, ok := text(); !ok {
			return
		}
		// status is the one a start that waits answers with: 202 when the
		// time passed.
		out, status := r.runAnswer(now, existing, true), http.StatusAccepted
		if existing || r.deps.Pipelines.Settled(now) {
			status = http.StatusOK
		}
		out["status"] = status
		send("answer", out)
	}
	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()
	settled := make(chan jobs.Run, 1)
	go func() {
		now := run
		if d > 0 {
			if n, err := r.deps.Pipelines.Wait(ctx, run.ID, d); err == nil {
				now = n
			}
		}
		settled <- now
	}()
	beat := time.NewTicker(runStreamHeartbeat)
	defer beat.Stop()
	for {
		wake, ok := text()
		if !ok {
			return
		}
		select {
		case <-wake:
		case <-stream.Ended():
			// The server shuts down: the run as it is.
			cancel()
			answer(<-settled)
			return
		case now := <-settled:
			answer(now)
			return
		case <-beat.C:
			if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		case <-req.Context().Done():
			return
		}
	}
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
	// Only the pipelines' runs: the runner runs the automations' actions too
	// (IMP-166), which the Runs page cannot draw.
	runs, err := r.deps.Pipelines.Runner().Store().Runs(jobs.RunFilter{
		Kind: pipeline.Kind, Ref: strings.TrimSpace(q.Get("pipeline")), Projects: scope, Status: jobs.Status(strings.TrimSpace(q.Get("status"))), Limit: limit,
	})
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"runs": pipeline.Briefs(runs)})
}

// handleQueue answers the queue of the steps of the projects the caller
// reads (one with ?project=): the steps queued by resource, with the places
// taken and the limits of the whole server.
func (r *Router) handleQueue(w http.ResponseWriter, req *http.Request) {
	scope, err := r.searchScope(principalFromContext(req), strings.TrimSpace(req.URL.Query().Get("project")))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	q, err := r.deps.Pipelines.Queue(scope)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, CodeServerInternal, err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"queue": q})
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
	if id == "queue" && action == "" && req.Method == http.MethodGet {
		r.handleQueue(w, req)
		return
	}
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
		views := pipeline.StepViews(run, steps, maxStepOutputShown)
		r.deps.Pipelines.Waits(run, views)
		WriteJSON(w, http.StatusOK, map[string]any{"run": pipeline.Brief(run), "steps": views})
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
