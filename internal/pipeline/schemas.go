package pipeline

import "encoding/json"

// The settings (with) and the output of the base node types as JSON
// Schemas, for the editor of the web UI (IMP-169, F4): it builds each
// node's form from them, and offers the fields of an output in the
// expressions of the nodes after it. They say what docs/pipelines.md
// says; the nodes check their settings themselves when they run.
//
// Beside the standard keywords, "format" tells the editor how to edit a
// text: "multiline" (a text area), "code" (a script), "path" (a note of
// the project), "attachment" (a file of the project), "duration" (30s, 5m).

// NodeSchema is a node type with the schemas of its settings and output.
type NodeSchema struct {
	Type        string          `json:"type"`
	Description string          `json:"description,omitempty"`
	Input       json.RawMessage `json:"input_schema"`
	Output      json.RawMessage `json:"output_schema,omitempty"`
}

const llmSettings = `
	"provider": {"type": "string", "description": "The LLM provider of the server's config", "default": "default"},
	"model": {"type": "string", "description": "A model other than the provider's"},
	"max_tokens": {"type": "integer", "description": "The longest answer, in tokens", "default": 1024},
	"temperature": {"type": "number", "description": "From 0 (always the same answer) up"},
	"json": {"type": "boolean", "description": "Ask for a JSON answer, read into json"}`

const llmOutput = `
	"text": {"type": "string", "description": "The answer"},
	"json": {"description": "The answer read as JSON, with json: true"},
	"model": {"type": "string"},
	"provider": {"type": "string"},
	"usage": {"type": "object", "properties": {"prompt_tokens": {"type": "integer"}, "completion_tokens": {"type": "integer"}}}`

// baseSchemas, in the order of the docs.
var baseSchemas = []NodeSchema{
	{Type: "note.read", Description: "Reads a note of the project",
		Input: json.RawMessage(`{"type": "object", "required": ["path"], "properties": {
	"path": {"type": "string", "format": "path", "description": "The note, inside the project (relative to it)"}}}`),
		Output: json.RawMessage(`{"type": "object", "properties": {
	"path": {"type": "string"}, "title": {"type": "string"}, "frontmatter": {"type": "object"},
	"body": {"type": "string"}, "etag": {"type": "string"}}}`)},
	{Type: "note.write", Description: "Writes a note of the project",
		Input: json.RawMessage(`{"type": "object", "required": ["path"], "properties": {
	"path": {"type": "string", "format": "path", "description": "The note, inside the project (relative to it)"},
	"title": {"type": "string"},
	"body": {"type": "string", "format": "multiline", "description": "The note's text, after its frontmatter"},
	"frontmatter": {"type": "object", "description": "Fields of the frontmatter"},
	"mode": {"type": "string", "enum": ["upsert", "create", "replace"], "default": "upsert",
	  "description": "upsert writes or updates, create fails when the note is there, replace fails when it is not"}}}`),
		Output: json.RawMessage(`{"type": "object", "properties": {
	"path": {"type": "string"}, "etag": {"type": "string"}, "created": {"type": "boolean"}}}`)},
	{Type: "wait.call", Description: "Waits for a service to resume the run at {{ run.resume_url }}",
		Input: json.RawMessage(`{"type": "object", "properties": {
	"timeout": {"type": "string", "format": "duration", "default": "24h", "description": "The longest wait, at most the server's max_wait"}}}`),
		Output: json.RawMessage(`{"description": "The data the resume hands it"}`)},
	{Type: "starlark", Description: "Runs a short script that sets output",
		Input: json.RawMessage(`{"type": "object", "required": ["code"], "properties": {
	"code": {"type": "string", "format": "code", "description": "Starlark that sets output; the run's values are in ctx"},
	"input": {"description": "A value the script reads as input"}}}`),
		Output: json.RawMessage(`{"description": "The value of output"}`)},
	{Type: "http.request", Description: "Calls a web service of the server's http_allowlist",
		Input: json.RawMessage(`{"type": "object", "required": ["url"], "properties": {
	"url": {"type": "string", "description": "An address in the server's http_allowlist"},
	"method": {"type": "string", "enum": ["GET", "POST", "PUT", "PATCH", "DELETE"], "default": "GET"},
	"json": {"description": "A JSON body"},
	"body": {"type": "string", "format": "multiline", "description": "A text body"},
	"headers": {"type": "object", "description": "Request headers"},
	"secret_header": {"type": "string", "description": "Header=secret_name: a header from a secret of the server"},
	"accept_errors": {"type": "boolean", "description": "A 4xx or 5xx answer is an output, not a failure"}}}`),
		Output: json.RawMessage(`{"type": "object", "properties": {
	"status": {"type": "integer"}, "headers": {"type": "object"}, "json": {"description": "The answer read as JSON"},
	"body": {"type": "string"}}}`)},
	{Type: "llm.chat", Description: "Asks a language model",
		Input: json.RawMessage(`{"type": "object", "required": ["prompt"], "properties": {
	"prompt": {"type": "string", "format": "multiline"},
	"system": {"type": "string", "format": "multiline"},` + llmSettings + `}}`),
		Output: json.RawMessage(`{"type": "object", "properties": {` + llmOutput + `}}`)},
	{Type: "llm.describe", Description: "Asks a language model about an image",
		Input: json.RawMessage(`{"type": "object", "required": ["image"], "properties": {
	"image": {"type": "string", "format": "attachment", "description": "An image of the project: png, jpg, gif or webp, 10 MiB at most"},
	"prompt": {"type": "string", "format": "multiline"},
	"system": {"type": "string", "format": "multiline"},` + llmSettings + `}}`),
		Output: json.RawMessage(`{"type": "object", "properties": {` + llmOutput + `,
	"image": {"type": "string"}}}`)},
	{Type: "in.list", Description: "Lists files of the project's IN folder",
		Input: json.RawMessage(`{"type": "object", "properties": {
	"folder": {"type": "string", "description": "A folder of the IN folder; empty for all of it"},
	"recursive": {"type": "boolean", "default": true, "description": "Its subfolders too"},
	"match": {"type": "string", "description": "A glob on the file name: *.pdf, fattura_??.xml"},
	"kinds": {"type": "array", "items": {"type": "string"}, "description": "Extensions: pdf, xml"},
	"status": {"type": "string", "enum": ["new", "working", "done", "error", "ignored"]},
	"batch": {"type": "string", "description": "The files of a batch: {{ trigger.batch }}"},
	"limit": {"type": "integer", "default": 500, "description": "At most 5000 a page"},
	"cursor": {"type": "string", "description": "The next of the page before"}}}`),
		Output: json.RawMessage(`{"type": "object", "properties": {` + inFilesOutput + `,
	"count": {"type": "integer"},
	"next": {"type": "string", "description": "The cursor of the next page; empty at the last"}}}`)},
	{Type: "in.move", Description: "Moves a file of the IN folder, or sets its status",
		Input: json.RawMessage(`{"type": "object", "required": ["file"], "properties": {
	"file": {"type": "string", "description": "The file, in the IN folder: {{ trigger.file }}, {{ item.path }}"},
	"to": {"type": "string", "description": "Where in the IN folder: a folder (done/) or a path; empty does not move it"},
	"status": {"type": "string", "enum": ["done", "error", "ignored", "new"], "description": "Its status from now on"}}}`),
		Output: json.RawMessage(`{"type": "object", "properties": {
	"path": {"type": "string"}, "from": {"type": "string"}, "status": {"type": "string"}}}`)},
}

// inFilesOutput is the files an in.list gives.
const inFilesOutput = `"files": {"type": "array", "items": {"type": "object", "properties": {
	"path": {"type": "string"}, "name": {"type": "string"}, "stem": {"type": "string"}, "ext": {"type": "string"},
	"dir": {"type": "string"}, "size": {"type": "integer"}, "sha256": {"type": "string"}, "status": {"type": "string"},
	"modified": {"type": "string"}, "batch": {"type": "string"}, "source": {"type": "string"}}}}`

// BaseSchemas lists the base node types with their schemas.
func BaseSchemas() []NodeSchema { return append([]NodeSchema(nil), baseSchemas...) }
