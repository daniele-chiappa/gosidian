package v1

import (
	"encoding/json"
	"net/http"

	"github.com/gosidian/gosidian/internal/pipeline"
)

// The pipeline editor of the web UI (IMP-169, F4) edits a ```pipeline
// block even where the pipelines are off: these two routes answer always,
// to every signed-in account. With the pipelines off the node types are
// the base ones; with them on, the accepted plugins' are there too.
//
//	GET  /api/v1/pipeline-nodes  the node types, with the JSON Schemas of
//	                             their settings and output
//	POST /api/v1/pipeline-check  {source}: whether the server would run
//	                             the block, and why not

// maxPipelineSource caps the YAML a check reads.
const maxPipelineSource = 256 << 10

type pipelineNodeView struct {
	Type string `json:"type"`
	// Group is "base", or the plugin the node comes from.
	Group       string          `json:"group"`
	Description string          `json:"description,omitempty"`
	Status      string          `json:"status,omitempty"`
	Files       []string        `json:"files,omitempty"`
	Input       json.RawMessage `json:"input_schema,omitempty"`
	Output      json.RawMessage `json:"output_schema,omitempty"`
}

func (r *Router) handlePipelineNodes(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	out := []pipelineNodeView{}
	for _, n := range pipeline.BaseSchemas() {
		out = append(out, pipelineNodeView{Type: n.Type, Group: "base", Description: n.Description, Input: n.Input, Output: n.Output})
	}
	if r.deps.Pipelines != nil && r.deps.Plugins != nil {
		for _, n := range r.deps.Plugins.NodeSchemas() {
			out = append(out, pipelineNodeView{Type: n.Type, Group: n.Plugin, Description: n.Description, Status: n.Status,
				Files: n.Files, Input: n.Input, Output: n.Output})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"pipelines_enabled": r.deps.Pipelines != nil, "nodes": out})
}

type pipelineCheckRequest struct {
	Source string `json:"source"`
}

func (r *Router) handlePipelineCheck(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "method not allowed")
		return
	}
	var body pipelineCheckRequest
	if err := DecodeJSON(req, &body); err != nil {
		WriteError(w, http.StatusBadRequest, CodeValidationFormat, err.Error())
		return
	}
	if len(body.Source) > maxPipelineSource {
		WriteError(w, http.StatusRequestEntityTooLarge, CodeValidationFormat, "the pipeline is larger than 256 KiB")
		return
	}
	types := pipeline.NewRegistry()
	if r.deps.Pipelines != nil {
		types = r.deps.Pipelines.Types()
	}
	if _, err := pipeline.Parse(body.Source, types); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "problem": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
