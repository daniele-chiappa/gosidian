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
	"json": {"type": "boolean", "description": "Ask for a JSON answer, read into json"},
	"reasoning_effort": {"type": "string", "description": "How much a model that reasons first may think: none (off on Ollama), low, medium, high; a thinking model can spend all max_tokens before it answers"}`

const llmOutput = `
	"text": {"type": "string", "description": "The answer"},
	"json": {"description": "The answer read as JSON, with json: true"},
	"model": {"type": "string"},
	"provider": {"type": "string"},
	"usage": {"type": "object", "properties": {"prompt_tokens": {"type": "integer"}, "completion_tokens": {"type": "integer"}}},
	"finish_reason": {"type": "string", "description": "Why the answer ended: length when max_tokens ran out"},
	"reasoning": {"type": "string", "description": "The model's thinking, when it gives it"}`

const mediaSettings = `
	"media": {"type": "string", "enum": ["keep", "inline"], "description": "inline: the images as text, from their image notes: [icon: label] in a sentence, [image: label] description on a line of its own"},
	"media_words": {"type": "object", "properties": {"icon": {"type": "string"}, "image": {"type": "string"}}, "description": "The words of an image's text, icon and image by default"},
	"inline_max_side": {"type": "integer", "default": 128, "description": "An image in a sentence up to this longest side is an icon; 0: any"}`

const mediaStatsSchema = `{"type": "object", "description": "The images put as text", "properties": {
	  "images": {"type": "integer"}, "icons": {"type": "integer"}, "described": {"type": "integer"},
	  "missing": {"type": "integer", "description": "Without a label or a description"}, "word_alt": {"type": "integer", "description": "Alt texts Word wrote, left out"}}}`

// baseSchemas, in the order of the docs.
var baseSchemas = []NodeSchema{
	{Type: "note.read", Description: "Reads a note of the project",
		Input: json.RawMessage(`{"type": "object", "required": ["path"], "properties": {
	"path": {"type": "string", "format": "path", "description": "The note, inside the project (relative to it)"},` + mediaSettings + `}}`),
		Output: json.RawMessage(`{"type": "object", "properties": {
	"path": {"type": "string"}, "title": {"type": "string"}, "frontmatter": {"type": "object"},
	"body": {"type": "string"}, "etag": {"type": "string"},
	"media": ` + mediaStatsSchema + `}}`)},
	{Type: "note.write", Description: "Writes a note of the project",
		Input: json.RawMessage(`{"type": "object", "required": ["path"], "properties": {
	"path": {"type": "string", "format": "path", "description": "The note, inside the project (relative to it)"},
	"title": {"type": "string"},
	"body": {"type": "string", "format": "multiline", "description": "The note's text, after its frontmatter"},
	"frontmatter": {"type": "object", "description": "Fields of the frontmatter"},
	"frontmatter_merge": {"type": "boolean", "default": false,
	  "description": "Merge frontmatter into the note's own instead of replacing it (a map merges into a map, null removes a key), and keep its body when body is not given"},
	"mode": {"type": "string", "enum": ["upsert", "create", "replace", "append"], "default": "upsert",
	  "description": "upsert writes or updates, create fails when the note is there, replace fails when it is not, append adds body after the note's and merges the frontmatter (a note it does not find it creates)"}}}`),
		Output: json.RawMessage(`{"type": "object", "properties": {
	"path": {"type": "string"}, "etag": {"type": "string"}, "created": {"type": "boolean"}}}`)},
	{Type: "note.list", Description: "Lists notes of the project, a page at a time",
		Input: json.RawMessage(`{"type": "object", "properties": {
	"folder": {"type": "string", "format": "path", "description": "A folder of the project (relative to it); empty for all of it"},
	"recursive": {"type": "boolean", "default": true, "description": "Its subfolders too"},
	"where": {"type": "array", "description": "Conditions on the frontmatter, as memory_query: {field, op, value}",
	  "items": {"type": "object", "properties": {"field": {"type": "string"}, "op": {"type": "string"}, "value": {}}}},
	"sort": {"type": "string", "default": "path", "description": "A field, path, title or modified, with asc or desc; keys separated by commas"},
	"fields": {"type": "array", "items": {"type": "string"}, "description": "Frontmatter fields to give with each note"},
	"limit": {"type": "integer", "default": 500, "description": "At most 5000 a page"},
	"cursor": {"type": "string", "description": "The next of the page before"}}}`),
		Output: json.RawMessage(`{"type": "object", "properties": {
	"notes": {"type": "array", "items": {"type": "object", "properties": {
	  "path": {"type": "string"}, "title": {"type": "string"}, "fields": {"type": "object"}}}},
	"count": {"type": "integer", "description": "The notes of this page"},
	"total": {"type": "integer", "description": "The notes that match, in all"},
	"next": {"type": "string", "description": "The cursor of the next page; empty at the last"}}}`)},
	{Type: "note.sections", Description: "Cuts a note by its headings into sections and paragraphs, the chunks of an archive of vectors",
		Input: json.RawMessage(`{"type": "object", "required": ["path"], "properties": {
	"path": {"type": "string", "format": "path", "description": "The note, inside the project (relative to it)"},
	"version": {"type": "string", "description": "Ends each chunk's id after an @ (a commit), so the chunks of two versions live side by side"},
	"levels": {"type": "array", "items": {"type": "string", "enum": ["section", "paragraph"]}, "default": ["section", "paragraph"]},
	"max_chars": {"type": "integer", "default": 6000, "description": "A longer section is cut in parts at its paragraphs"},
	"context": {"type": "string", "enum": ["heading", "path", "none"], "default": "heading", "description": "What a chunk's text starts with: its section's heading, the title and the path of the headings, or nothing"},
	"text": {"type": "boolean", "default": true, "description": "false: the chunks without their text, with their base and hash, to tell which changed"},
	"only": {"type": "array", "items": {"type": "string"}, "description": "Only the chunks of these bases"},
	"meta": {"type": "object", "description": "Keys added to each chunk's meta (status, the dates of a version)"},
	"media_numbers": {"type": "boolean", "default": false, "description": "With media: inline, each image (not an icon) numbered in the note's order, [image 3: label], and each chunk's media"},` + mediaSettings + `}}`),
		Output: json.RawMessage(`{"type": "object", "properties": {
	"doc": {"type": "string"}, "title": {"type": "string"}, "version": {"type": "string"},
	"sections": {"type": "integer", "description": "The sections of the note, parts of a long one each"},
	"paragraphs": {"type": "integer"},
	"chunks": {"type": "array", "description": "As an archive's upsert takes them", "items": {"type": "object", "properties": {
	  "id": {"type": "string"}, "text": {"type": "string"}, "title": {"type": "string"}, "doc": {"type": "string"},
	  "parent": {"type": "string"}, "level": {"type": "integer", "description": "1 a section, 2 a paragraph"},
	  "meta": {"type": "object", "properties": {"base": {"type": "string"}, "hash": {"type": "string"}, "chars": {"type": "integer"},
	    "section": {"type": "string"}, "position": {"type": "integer"}}},
	  "media": {"type": "array", "description": "With media_numbers: the images whose text is in the chunk's", "items": {"type": "object", "properties": {
	    "n": {"type": "integer"}, "path": {"type": "string", "description": "The image in the vault; none for one from outside"},
	    "note": {"type": "string", "description": "Its image note"}, "label": {"type": "string"}}}}}}},
	"count": {"type": "integer", "description": "The chunks given"},
	"media": ` + mediaStatsSchema + `}}`)},
	{Type: "note.mark", Description: "Puts a set of coloured marks on a note, shown beside it in the web UI, never in its text",
		Input: json.RawMessage(`{"type": "object", "required": ["path", "set"], "properties": {
	"path": {"type": "string", "format": "path", "description": "The note, inside the project (relative to it)"},
	"set": {"type": "string", "description": "The set's name (cut, review): a run replaces its set; no marks remove it"},
	"marks": {"type": "array", "description": "At most 500", "items": {"type": "object", "required": ["color"], "properties": {
	  "base": {"type": "string", "description": "A chunk of note.sections, by its base"},
	  "section": {"type": "string", "description": "Else a section, by the path of its headings (A > B)"},
	  "block": {"type": "integer", "description": "Its paragraph, from 1; none: the whole section"},
	  "color": {"type": "string", "enum": ["blue", "green", "orange", "red", "cyan", "grey"]},
	  "label": {"type": "string", "description": "At most 60 characters, beside the span"},
	  "text": {"type": "string", "description": "At most 500 characters, on hover"}}}}}}`),
		Output: json.RawMessage(`{"type": "object", "properties": {
	"path": {"type": "string"}, "set": {"type": "string"}, "count": {"type": "integer"}}}`)},
	{Type: "wait.call", Description: "Waits for a service to resume the run at {{ run.resume_url }}",
		Input: json.RawMessage(`{"type": "object", "properties": {
	"timeout": {"type": "string", "format": "duration", "default": "24h", "description": "The longest wait, at most the server's max_wait"}}}`),
		Output: json.RawMessage(`{"description": "The data the resume hands it"}`)},
	{Type: "pipeline.run", Description: "Starts a pipeline of the project, and waits for it to end",
		Input: json.RawMessage(`{"type": "object", "required": ["pipeline"], "properties": {
	"pipeline": {"type": "string", "format": "path", "description": "A pipeline note of the project (relative to it)"},
	"input": {"type": "object", "description": "What the pipeline reads as its trigger"},
	"wait": {"type": "boolean", "default": true, "description": "Wait for its run to end; false in an each, one run per element"},
	"timeout": {"type": "string", "format": "duration", "default": "24h", "description": "The longest wait, at most the server's max_wait"},
	"key": {"type": "string", "description": "Tells apart the runs one node starts of the same pipeline"}}}`),
		Output: json.RawMessage(`{"type": "object", "properties": {
	"run": {"type": "string"}, "pipeline": {"type": "string"}, "status": {"type": "string"},
	"error": {"type": "string"}, "existing": {"type": "boolean", "description": "Without wait: the run was there already"},
	"steps": {"type": "object", "description": "With wait: the status of each of its nodes"},
	"outputs": {"type": "object", "description": "With wait: the output of each of its nodes, when they fit"}}}`)},
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
	{Type: "llm.describe_media", Description: "Describes the images of a note in their image notes, with the text around each",
		Input: json.RawMessage(`{"type": "object", "required": ["note"], "properties": {
	"note": {"type": "string", "format": "path", "description": "The note whose image notes (in <note>_img/) get a description: {{ import.path }}"},
	"prompt": {"type": "string", "format": "multiline", "description": "{title}, {section} and {context} stand for the note's title, the image's section and the text around it; ask for JSON with label and description"},
	"inline_prompt": {"type": "string", "format": "multiline", "description": "The prompt for an image inside a sentence (an icon)"},
	"images": {"type": "string", "enum": ["all", "icons", "others"], "default": "all", "description": "The images this pass describes: icons are the images in a sentence up to inline_max_side; others the rest"},
	"inline_max_side": {"type": "integer", "default": 128, "description": "An image inside a sentence with a longer side gets prompt, not inline_prompt; 0: every one gets inline_prompt"},
	"system": {"type": "string", "format": "multiline"},
	"context_chars": {"type": "integer", "default": 1200, "description": "The text around an image given to the model, in characters"},
	"max_pixels": {"type": "integer", "default": 2000000, "description": "A larger image is scaled down for the model (the attachment stays as it is); 0 sends it whole"},
	"concurrency": {"type": "integer", "default": 1, "description": "Calls at once, at most 8"},
	"prompt_version": {"type": "integer", "default": 1, "description": "Written in each image note with the model"},
	"redo": {"type": "boolean", "description": "Describe again the images that have a description"},
	"provider": {"type": "string", "description": "The LLM provider of the server's config", "default": "default"},
	"model": {"type": "string", "description": "A model other than the provider's"},
	"max_tokens": {"type": "integer", "description": "The longest answer, in tokens", "default": 600},
	"temperature": {"type": "number", "description": "From 0 (always the same answer) up"}}}`),
		Output: json.RawMessage(`{"type": "object", "properties": {
	"described": {"type": "integer"}, "skipped": {"type": "integer", "description": "Described already, or no longer in the note"},
	"unreadable": {"type": "integer", "description": "Images no model reads (svg)"}, "failed": {"type": "integer"},
	"excluded": {"type": "integer", "description": "Images left to another pass by images"},
	"downscaled": {"type": "integer", "description": "Images scaled down for the model (max_pixels)"},
	"errors": {"type": "array", "items": {"type": "string"}},
	"usage": {"type": "object", "properties": {"prompt_tokens": {"type": "integer"}, "completion_tokens": {"type": "integer"}, "total_tokens": {"type": "integer"}}},
	"seconds": {"type": "number"}}}`)},
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
	{Type: "in.import", Description: "Brings a markdown file of the IN folder into the project as a note, its files as attachments, an image note for each image",
		Input: json.RawMessage(`{"type": "object", "required": ["file", "dest"], "properties": {
	"file": {"type": "string", "description": "The .md file, in the IN folder: {{ trigger.file }}"},
	"base": {"type": "string", "description": "The folder of the IN folder whose tree is kept under dest; empty for all of it"},
	"dest": {"type": "string", "format": "path", "description": "The folder of the project the note goes into, with the tree under base"},
	"media_notes": {"type": "boolean", "default": true, "description": "An image note for each image, in <note>_img/"},
	"list_images": {"type": "boolean", "default": true, "description": "List the image notes in the output; no: their count only (an each over many files)"},
	"convert": {"type": "string", "description": "A node that converts the images as they arrive, images.convert"},
	"convert_with": {"type": "object", "description": "The settings of the convert node"},
	"convert_batch": {"type": "integer", "default": 25, "description": "The images of one call of the convert node; at most the server's max_plugin_files"}}}`),
		Output: json.RawMessage(`{"type": "object", "properties": {
	"path": {"type": "string"}, "created": {"type": "boolean"}, "attachments": {"type": "integer"},
	"image_count": {"type": "integer"},
	"images": {"type": "array", "description": "With list_images", "items": {"type": "object", "properties": {
	  "note": {"type": "string"}, "attachment": {"type": "string"}, "src": {"type": "string"},
	  "position": {"type": "integer"}, "inline": {"type": "boolean"}}}},
	"removed": {"type": "array", "items": {"type": "string"}, "description": "Image notes of images no longer in the file, into the trash"},
	"unresolved": {"type": "array", "items": {"type": "string"}, "description": "Links left as written"},
	"skipped": {"type": "array", "items": {"type": "object", "properties": {"src": {"type": "string"}, "reason": {"type": "string"}}}}}}`)},
}

// inFilesOutput is the files an in.list gives.
const inFilesOutput = `"files": {"type": "array", "items": {"type": "object", "properties": {
	"path": {"type": "string"}, "name": {"type": "string"}, "stem": {"type": "string"}, "ext": {"type": "string"},
	"dir": {"type": "string"}, "size": {"type": "integer"}, "sha256": {"type": "string"}, "status": {"type": "string"},
	"modified": {"type": "string"}, "batch": {"type": "string"}, "source": {"type": "string"}}}}`

// BaseSchemas lists the base node types with their schemas.
func BaseSchemas() []NodeSchema { return append([]NodeSchema(nil), baseSchemas...) }
