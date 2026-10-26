package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/gosidian/gosidian/internal/audit"
	"github.com/gosidian/gosidian/internal/auth"
	"github.com/gosidian/gosidian/internal/jobs"
	"github.com/gosidian/gosidian/internal/pipeline"
	"github.com/gosidian/gosidian/internal/plugins"
)

// Two tools for the pipelines (IMP-167), to keep the catalogue small:
// memory_pipelines reads (the pipelines of a project, the runs of one, a
// run with its steps), memory_pipeline_run acts (start, resume, cancel).

const pipelinesOff = "pipelines are off on this server ([pipelines] enabled, GOSIDIAN_PIPELINES_ENABLED)"

// defaultStepOutputShown caps a step's output in memory_pipelines unless
// [mcp] max_step_output_shown says otherwise: the whole output stays in
// the run, and a large one is written to a note anyway.
const defaultStepOutputShown = 4 << 10

func (s *Server) registerPipelineTools() {
	s.impl.AddTool(mcp.NewTool("memory_pipelines",
		mcp.WithDescription("Read the pipelines of a project (notes `type: pipeline` with a ```pipeline YAML block, run by the server when [pipelines] is on and the project has use_pipelines): with project only, the pipelines with their trigger, nodes, problems and last run, and the plugins with the node types they add (<plugin>.<node>, status ok when they run) and the tools they offer agents (<plugin>_<tool>, in the projects listed in exposed_in); with path, the runs of that pipeline, newest first; with run, one run with the status, error and output of each step. Start, resume or cancel a run with memory_pipeline_run. Format and node types: docs/pipelines.md."),
		mcp.WithString("project", mcp.Required(), mcp.Description("Project of the pipelines. "+scopedProjectNote)),
		mcp.WithString("path", mcp.Description("A pipeline note's path: list its runs.")),
		mcp.WithString("run", mcp.Description("A run's id: show it with its steps.")),
		mcp.WithNumber("limit", mcp.Description("Runs to list (default 20, at most 100).")),
	), s.handlePipelines)

	s.impl.AddTool(mcp.NewTool("memory_pipeline_run",
		mcp.WithDescription("Start, resume or cancel a pipeline run. start (the default): path names the pipeline note, input is the JSON object its nodes read as {{ trigger.… }} (pass files as vault paths, not bytes), idempotency_key starts it once for the same key, callback_url is POSTed the run's summary when it ends (only addresses in [pipelines] http_allowlist), wait (seconds, at most [pipelines] max_sync_wait, 60 by default) returns once the run ended or waits for a call (wait.call), with its steps and the output of the node the pipeline names as its answer, else at once with the run's id. resume: run (and step when more than one waits) gets data as the output of its waiting wait.call node. cancel: stops the run. Needs write access to the pipeline's project; read the result with memory_pipelines."),
		mcp.WithString("action", mcp.Description("start (default), resume or cancel.")),
		mcp.WithString("path", mcp.Description("start: the pipeline note.")),
		mcp.WithObject("input", mcp.Description("start: the trigger's payload, a JSON object.")),
		mcp.WithString("idempotency_key", mcp.Description("start: the same key starts the pipeline once and gives back that run.")),
		mcp.WithString("callback_url", mcp.Description("start: an address to POST the summary to when the run ends.")),
		mcp.WithNumber("wait", mcp.Description("start: seconds to wait for the run to end or wait (default the pipeline's category's, else 0; at most [pipelines] max_sync_wait).")),
		mcp.WithString("run", mcp.Description("resume, cancel: the run's id.")),
		mcp.WithString("step", mcp.Description("resume: the waiting step, when more than one waits.")),
		mcp.WithObject("data", mcp.Description("resume: the waiting step's output, a JSON object.")),
	), s.handlePipelineRun)
}

func (s *Server) handlePipelines(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tok, errRes := s.authorizeRead(ctx)
	if errRes != nil {
		return errRes, nil
	}
	if s.pipelines == nil {
		return mcp.NewToolResultError(pipelinesOff), nil
	}
	project, err := s.resolveProject(tok, req)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	store := s.pipelines.Runner().Store()
	if id := strings.TrimSpace(req.GetString("run", "")); id != "" {
		run, err := store.Run(id)
		if err != nil || run.Project != project {
			return mcp.NewToolResultErrorf("no run %q in project %q", id, project), nil
		}
		steps, err := store.Steps(id)
		if err != nil {
			return mcp.NewToolResultErrorFromErr("steps", err), nil
		}
		views := pipeline.StepViews(run, steps, s.stepOutputShown)
		s.pipelines.Waits(run, views)
		return mcp.NewToolResultJSON(map[string]any{"run": pipeline.Brief(run), "pipeline": run.Ref, "steps": views})
	}
	limit := int(req.GetFloat("limit", 20))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if p := strings.TrimSpace(req.GetString("path", "")); p != "" {
		if !strings.HasPrefix(p, project+"/") {
			return mcp.NewToolResultErrorf("%s is not in project %q", p, project), nil
		}
		runs, err := store.Runs(jobs.RunFilter{Kind: pipeline.Kind, Ref: p, Limit: limit})
		if err != nil {
			return mcp.NewToolResultErrorFromErr("runs", err), nil
		}
		return mcp.NewToolResultJSON(map[string]any{"pipeline": p, "runs": pipeline.Briefs(runs)})
	}
	ps, err := s.PipelineHost().Pipelines(project)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("pipelines", err), nil
	}
	res := map[string]any{"project": project, "pipelines": s.pipelines.Infos(ps)}
	// The queue of the project's steps (IMP-196): whether a run started now
	// would wait, and for which plugin.
	if q, err := s.pipelines.Queue([]string{project}); err == nil {
		res["queue"] = q
	}
	if s.plugins != nil && len(s.plugins.Names()) > 0 {
		res["plugins"] = pluginBriefs(s.plugins)
	}
	return mcp.NewToolResultJSON(res)
}

// pluginBrief is a plugin as an agent writing a pipeline needs it: its
// node types, and whether they run now; its tools (IMP-182) and the
// projects they are offered in. No address, no error: they name hosts of
// the network.
type pluginBrief struct {
	Name    string             `json:"name"`
	Status  string             `json:"status"`
	Version string             `json:"version,omitempty"`
	Nodes   []plugins.NodeInfo `json:"nodes"`
	Tools   []string           `json:"tools,omitempty"`
	Exposed []string           `json:"exposed_in,omitempty"`
}

func pluginBriefs(reg *plugins.Registry) []pluginBrief {
	states := reg.States()
	out := make([]pluginBrief, 0, len(states))
	for _, st := range states {
		b := pluginBrief{Name: st.Name, Status: st.Status, Version: st.Version, Nodes: st.Nodes}
		if b.Nodes == nil {
			b.Nodes = []plugins.NodeInfo{}
		}
		if st.Accepted != nil && len(st.Accepted.Manifest.Tools) > 0 {
			b.Tools = pluginToolNames(st.Accepted.Manifest)
			b.Exposed = reg.ExposedIn(st.Name)
		}
		out = append(out, b)
	}
	return out
}

func (s *Server) handlePipelineRun(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if s.pipelines == nil {
		if _, errRes := s.authorizeRead(ctx); errRes != nil {
			return errRes, nil
		}
		return mcp.NewToolResultError(pipelinesOff), nil
	}
	args := req.GetArguments()
	switch action := strings.TrimSpace(req.GetString("action", "start")); action {
	case "", "start":
		return s.pipelineStart(ctx, req, args)
	case "resume", "cancel":
		return s.pipelineAct(ctx, req, args, action)
	default:
		return mcp.NewToolResultErrorf("action %q: start, resume or cancel", action), nil
	}
}

func objectArg(args map[string]any, name string) (map[string]any, []byte, error) {
	v, ok := args[name]
	if !ok || v == nil {
		return nil, []byte("null"), nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("%s is a JSON object", name)
	}
	b, err := json.Marshal(m)
	return m, b, err
}

func (s *Server) pipelineStart(ctx context.Context, req mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, error) {
	path := strings.TrimSpace(req.GetString("path", ""))
	if path == "" {
		return mcp.NewToolResultError("path names the pipeline note to start"), nil
	}
	tok, errRes := s.authorizeWrite(ctx, path)
	if errRes != nil {
		return errRes, nil
	}
	if s.pathInHiddenProject(path) {
		return mcp.NewToolResultErrorf("project %q is hidden from MCP by config", auth.ProjectOf(path)), nil
	}
	input, raw, err := objectArg(args, "input")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if res := s.checkWriteLimits(ctx, tok, len(raw)); res != nil {
		return res, nil
	}
	run, existing, err := s.pipelines.Start(pipeline.StartRequest{
		Path: path, Trigger: pipeline.TriggerManual, Input: input, Actor: tok.Name,
		IdemKey: strings.TrimSpace(req.GetString("idempotency_key", "")), CallbackURL: strings.TrimSpace(req.GetString("callback_url", "")),
	})
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if !existing {
		s.auditWrite(ctx, audit.ActionPipelineRun, path, run.ID, int64(len(raw)))
	}
	// Unsaid, the wait is the category's (IMP-196).
	wait := s.pipelines.DefaultWait(path)
	if _, said := args["wait"]; said {
		wait = time.Duration(req.GetFloat("wait", 0) * float64(time.Second))
	}
	if d := s.pipelines.SyncWait(wait); d > 0 {
		if r, err := s.pipelines.Wait(ctx, run.ID, d); err == nil {
			run = r
		}
	}
	out := map[string]any{"run": pipeline.Brief(run), "pipeline": run.Ref, "existing": existing}
	if run.Status.Terminal() || run.Status == jobs.Waiting {
		if steps, err := s.pipelines.Runner().Store().Steps(run.ID); err == nil {
			out["steps"] = pipeline.StepViews(run, steps, s.stepOutputShown)
			if answer, ok := pipeline.AnswerOf(run, steps); ok {
				out["answer"] = answer
			}
		}
	}
	return mcp.NewToolResultJSON(out)
}

func (s *Server) pipelineAct(ctx context.Context, req mcp.CallToolRequest, args map[string]any, action string) (*mcp.CallToolResult, error) {
	id := strings.TrimSpace(req.GetString("run", ""))
	if id == "" {
		return mcp.NewToolResultErrorf("%s needs run, the run's id", action), nil
	}
	store := s.pipelines.Runner().Store()
	run, err := store.Run(id)
	if err != nil {
		if errors.Is(err, jobs.ErrNotFound) {
			return mcp.NewToolResultErrorf("no run %q", id), nil
		}
		return mcp.NewToolResultErrorFromErr("run", err), nil
	}
	tok, errRes := s.authorizeWrite(ctx, run.Ref)
	if errRes != nil {
		return errRes, nil
	}
	_, raw, err := objectArg(args, "data")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if res := s.checkWriteLimits(ctx, tok, len(raw)); res != nil {
		return res, nil
	}
	auditAction := audit.ActionPipelineCancel
	if action == "resume" {
		auditAction = audit.ActionPipelineResume
		err = s.pipelines.Runner().Resume(id, strings.TrimSpace(req.GetString("step", "")), raw)
	} else {
		err = s.pipelines.Runner().Cancel(id)
	}
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	s.auditWrite(ctx, auditAction, run.Ref, id, int64(len(raw)))
	if run, err = store.Run(id); err != nil {
		return mcp.NewToolResultErrorFromErr("run", err), nil
	}
	return mcp.NewToolResultJSON(map[string]any{"run": pipeline.Brief(run), "pipeline": run.Ref})
}
