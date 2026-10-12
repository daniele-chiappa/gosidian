# Pipelines

A pipeline is a note that tells the server what to do when something
happens: a document arrives, an agent or a service calls, the clock
strikes. Its nodes read notes, call a language model or a web service,
run a short script, wait for an answer, and write notes back, as the
pipeline (ADR-035, IMP-166, IMP-167). [Plugins](plugins.md), MCP servers
beside gosidian, add more node types: converting documents, for one.

Pipelines are **off by default**, twice over:

- the server runs none until `[pipelines] enabled = true`
  (`GOSIDIAN_PIPELINES_ENABLED=true`);
- even then, a project's pipelines run only when the project has the
  `use_pipelines` flag (Projects window, chip `pipelines`, or
  `PUT /api/v1/projects/<name>` with `{"use_pipelines": true}`).

## Turning them on

```toml
[pipelines]
enabled = true
workers = 2              # steps run at once
step_timeout = "15m"     # the longest a step may run
retention = "720h"       # runs kept after they end; 0 keeps them
max_wait = "168h"        # the longest a wait.call may wait
max_llm_tokens = 200000  # LLM tokens one run may use; 0: no cap
http_allowlist = ["https://api.example.com/", "http://gpu.lan:5001/"]
public_url = "https://vault.example.com"   # default: [oauth] issuer
files_url = "http://gosidian:8080"         # where plugins fetch files; default: public_url
timezone = "Europe/Rome"                   # default: [automations] timezone

[[pipelines.llm]]
name = "default"
base_url = "http://gpu.lan:11434/v1"  # any OpenAI-compatible API
model = "qwen2.5vl:7b"

[[pipelines.llm]]
name = "openrouter"
base_url = "https://openrouter.ai/api/v1"
api_key_secret = "openrouter"              # gosidian secret set openrouter
model = "anthropic/claude-sonnet-4.5"

[[pipelines.plugins]]                      # docs/plugins.md
name = "docling"
url = "http://gosidian-docling:8000/mcp"
token_secret = "docling-token"
```

The environment overrides the file (see [configuration](configuration.md)):
`GOSIDIAN_PIPELINES_ENABLED`, `_WORKERS`, `_RETENTION`, `_STEP_TIMEOUT`,
`_MAX_WAIT`, `_MAX_LLM_TOKENS`, `_HTTP_ALLOWLIST` (comma-separated),
`_PUBLIC_URL`, `_FILES_URL`, `_TIMEZONE`, `_LLM_URL`, `_LLM_MODEL`,
`_LLM_KEY_SECRET` for one provider named `default`, and `_PLUGINS`
(`name=url,…`, plugins without a token).

The runs live in `<state-dir>/jobs.db` (SQLite, outside the vault and its
git history). Secrets live in `<state-dir>/secrets.json`, each sealed with
AES-GCM under `GOSIDIAN_SECRETS_KEY` (32 bytes in base64:
`openssl rand -base64 32`). Without the key the nodes that need a secret
fail and say why; the rest work.

## A pipeline note

````markdown
---
title: Arrivals
type: pipeline
---

# Arrivals

Describes each image that arrives in inbox/ and files a note for it.

```pipeline
trigger: {on: ingest, kinds: [png, jpg], folder: inbox}
nodes:
  - id: describe
    use: llm.describe
    with:
      image: "{{ trigger.file }}"
      prompt: Describe the image in Italian, in three sentences.
  - id: file
    use: note.write
    with:
      path: "archive/{{ trigger.stem }}"
      title: "{{ trigger.stem }}"
      body: |
        ![[{{ trigger.file }}]]

        {{ describe.text }}
```
````

A note `type: pipeline` holds one ```` ```pipeline ```` block of YAML. The
rest of the note is free: say what the pipeline is for. `memory_lint`
(rule `pipeline-invalid`), `memory_pipelines` and the Runs window say
why a pipeline cannot run.

### Triggers

| `on` | Starts when | `trigger` holds |
|---|---|---|
| `manual` (default) | someone starts it: the Runs window, `memory_pipeline_run`, `POST /api/v1/pipelines/<path>/runs` | the input given |
| `external` | as manual, and also a service with a run token | the input given |
| `ingest` | a file of one of `kinds` is stored as an attachment of the project (with `folder`: a note made with it in that folder) | `file`, `name`, `stem`, `ext`, `folder`, `note`, `project` |
| `schedule` | `every: "monday 09:00"`, `"day 18:30"` (the automations' syntax), once per slot; a slot missed by more than 10 minutes is not run late | `slot` |
| `in` | a batch of files of the project's [IN folder](in.md) closes: one run per file it takes, or one per batch with `batch: true`; `folder` (of the IN folder), `kinds` (none: all) and `match` (a glob on the name) choose the files; `take: false` reads them without taking them (their status stays the other pipelines') | per file: `file`, `name`, `stem`, `ext`, `dir`, `size`, `sha256`, `etag` (a source's file), `source` (`local`, or `<plugin>/<source>`), `project`; per batch: `batch`, `dir`, `count`, `bytes`, `files` (the files, when they fit in the input; else `files_listed` is false and `in.list` reads them), `project` |

Every file store counts as an arrival: `memory_ingest`,
`memory_upload_attachment`, `memory_upload_resource`, the media and table
notes, the MCP `/upload` endpoint and the web UI's uploads. A file starts
each pipeline once: its path (named by its content) is the run's
idempotency key. A package import (`as: package`) starts none.

An `in` run per file is keyed by the file's path and content (its SHA-256,
or, for a file of a [source](in.md#sources) that gives none, its etag): the
same file again starts no run, a changed one does. A batch run is keyed by its batch.
Each file a run takes is marked `working` with the run, then `done` or
`error` as the run ends, unless the pipeline set its status itself.

Any pipeline also starts by hand, whatever its trigger: an ingest pipeline
run with `{"file": "team/attachments/….pdf", …}` as input works on that
file again, and the IN window starts a pipeline on the files chosen, the
way its trigger would: once per file for an `in` pipeline per file, once
for the others.

### Nodes

| Field | |
|---|---|
| `id` | lowercase letters, digits and `_`; not `trigger`, `run`, `item`, `index` |
| `use` | the node type |
| `with` | its settings; strings may hold expressions |
| `after` | the nodes it waits for; default the node before it, `[]` to start at once (in parallel) |
| `when`, `unless` | an expression: the node is skipped when `when` is false or `unless` is true |
| `each` | an expression giving a list: the node runs once per element, with `item` and `index`; its output is the list of outputs |
| `retry` | `{attempts: 3, backoff: 30s}`: tries in all (at most 10), the wait doubling; a service's `Retry-After` is honoured |
| `timeout` | default `5m`, at most the server's step timeout |
| `on_error` | `stop` (default: the run fails) or `continue` (the nodes after it run, and read its output as missing) |

A node waits until every node in its `after` is done or skipped. It is
canceled when one of them failed without `on_error: continue`, or was
canceled. A run ends `done` when every node ended and none failed (but
those that continue on error), `failed` otherwise.

### Expressions

`{{ name.field.0 }}` reads `trigger`, `run` (`id`, `pipeline`,
`project`, `trigger`, `resume_url`, `resume_key`), the output of a node
that runs before, and `item`/`index` inside an `each`. A string that is
one expression keeps the value's type (a list, a number); text around
an expression turns it into text. There are no operators: compute in a
`starlark` node.

### Node types

| Type | `with` | Output |
|---|---|---|
| `note.read` | `path` (inside the project; relative to it) | `path`, `title`, `frontmatter`, `body`, `etag` |
| `note.write` | `path`, `body`, `title`, `frontmatter` (a map), `mode`: `upsert` (default), `create`, `replace` | `path`, `etag`, `created` |
| `wait.call` | `timeout` (default `24h`, at most `max_wait`) | the data the resume hands it |
| `starlark` | `code` setting `output`, a script (loops and `if` at the top level, `while`, `set`); `input`; the run's values in `ctx`, `json.encode`/`json.decode`; no `load`, no I/O, 10 million steps at most | the value of `output` itself: `{{ node }}`, or `{{ node.field }}` for a map |
| `http.request` | `url` (in `http_allowlist`), `method`, `json` or `body`, `headers`, `secret_header: "Authorization=secret_name"`, `accept_errors` | `status`, `headers`, `json` or `body` (4 MiB at most) |
| `llm.chat` | `prompt`, `system`, `provider` (default `default`), `model`, `max_tokens` (default 1024), `temperature`, `json: true` | `text`, `json`, `model`, `provider`, `usage` |
| `llm.describe` | `image` (an attachment of the project, png/jpg/gif/webp, 10 MiB at most), `prompt`, and as `llm.chat` | as `llm.chat`, plus `image` |
| `in.list` | `folder` (of the IN folder; empty: all), `recursive` (default yes), `match` (a glob on the name), `kinds`, `status`, `batch` (`{{ trigger.batch }}`), `limit` (default 500, at most 5000), `cursor` | `files` (`path`, `name`, `stem`, `ext`, `dir`, `size`, `sha256`, `etag`, `status`, `modified`, `batch`, `source`), `count`, `next` (the cursor of the next page; empty at the last) |
| `in.move` | `file` (in the IN folder), `to` (a folder, `done/`, or a path, in the IN folder; empty, or where it is already: it stays; for a file of a [source](in.md#sources), inside the source: `done/` is its `done/`), `status` (`done`, `error`, `ignored`, `new`) | `path`, `from`, `status` |
| `<plugin>.<node>` | what the plugin's manifest says; a setting it lists in `files` names an attachment of the project or a file of its IN folder (or a list of them, for a plugin that takes files by reference: `{{ trigger.files }}`) | the plugin tool's result, plus `files` (`name`, `path`, `mime`, `markdown`): the files it gave back, stored as attachments |

A plugin's nodes exist once the owner accepted its manifest
(`gosidian plugin accept`, [plugins](plugins.md)); `memory_pipelines`
and the Runs window list them.

A node's output is at most 1 MiB (`max_output_bytes`): write a large text
into a note and pass its path. A run's input is at most 256 KiB
(`max_input_bytes`): pass files by their vault path, not their bytes.

`llm.describe` and a plugin node read the file they name whole, 20 MiB at
most, unless the plugin takes files **by reference** (`file_refs` in its
manifest, [plugins](plugins.md#a-nodes-call)): then it gets the file's
name, type, size, SHA-256 and a URL to fetch it from while the step runs,
a file of any size.

## The IN folder

The files of a project's [IN folder](in.md) arrive in batches; the `in`
trigger takes them. Per file:

```pipeline
trigger: {on: in, folder: fatture, kinds: [pdf]}
nodes:
  - id: convert
    use: docling.convert
    with: {file: "{{ trigger.file }}"}
    timeout: 14m
  - id: save
    use: note.write
    with:
      path: "fatture/{{ trigger.stem }}"
      body: "{{ convert.markdown }}"
  - id: done
    use: in.move
    with: {file: "{{ trigger.file }}", to: "done/", status: done}
```

Per batch, deciding on all the files at once and working on each:

```pipeline
trigger: {on: in, batch: true, match: "*.xml"}
nodes:
  - id: files
    use: in.list
    with: {batch: "{{ trigger.batch }}", kinds: [xml]}
  - id: read
    use: starlark
    with:
      input: "{{ files.files }}"
      code: |
        output = [f["path"] for f in input if f["size"] > 0]
  - id: done
    use: in.move
    each: "{{ read }}"
    with: {file: "{{ item }}", to: "done/{{ trigger.dir }}/", status: done}
```

A run per batch reads its files with `in.list`, a page at a time
(`cursor: "{{ files.next }}"`), and spends the LLM tokens of one run
(`max_llm_tokens`): for heavy work on each file, a run per file is better.

## Editing in the web UI

A pipeline note's block is drawn where it stands: the trigger, the nodes
with their types, and what each waits for. **Code** shows the YAML as
written. **Edit**, for who writes in the project, opens the **pipeline
window** beside the note; so does **Edit** beside a pipeline in the Runs
window, and the address keeps the note, so a reload opens it again. The
window works with the pipelines off too: it then offers the base node
types only, and runs nothing.

- **The drawing.** **Add a node** lists the node types, the base ones and
  those of each accepted plugin, with what they do; the new node comes
  after the last one. A line dragged from a node's dot onto another node
  makes the second wait for the first; from the trigger's dot, the node
  starts with the run (`after: []`). A click selects a node or a line,
  **Delete** takes it away: what waited for a node taken away waits for
  what it waited for. The first drag fixes every node where it is, in
  `layout:` (the server ignores it, Obsidian shows it as YAML); **Lay out
  again** takes it away.
- **The panel** of the selected node: its id (renamed, the `after`, the
  `{{ }}` and the `layout:` that name it follow), its type, its settings as
  a form made from the type's schema (a text, several lines, a script, a
  choice, yes or no, a number, a duration, YAML for a list or a map, and
  any setting the schema does not name), then `when`, `unless`, `each`,
  `retry`, `timeout` and `on_error`. A text field offers, as you type
  `{{`, what the node may read: the trigger's fields, the run's, the
  outputs of the nodes that run before it, from their types' output
  schema. The trigger's panel sets its kind, its `kinds` and `folder`, its
  `every`, or, for `in`, its `match` and whether it runs per batch.
- **YAML**: the block as text. Every change is read as the server reads
  it (`POST /api/v1/pipeline-check`), and the problem shown under the
  drawing.

**Save** (Ctrl+S) writes the block in place of the old one and nothing
else: the rest of the note, the comments and the order of the fields stay
as written. A note changed meanwhile gets the conflict banner: reload it,
or put the pipeline into the new version.

Where the pipelines run (the server and the project), the window shows the
pipeline's runs: the last one, or one chosen, drawn on its nodes (each
with its state and attempts; selected, its output, error and times),
live while it goes. **Start** runs the saved pipeline with a JSON input,
**Resume** hands its data to a run that waits, **Cancel** stops one. The
Runs window draws the run it opens, too.

The node types and the schemas of their settings and outputs:
`GET /api/v1/pipeline-nodes`, which answers with the pipelines off too.

## What a run leaves

- **The notes it writes**, as the pipeline: the audit says source
  `pipeline`, actor `pipeline:<note path>`. Each carries its origin in the
  frontmatter: `pipeline: '[[team/pipelines/arrivals]]'`, `pipeline_run`,
  and `source: '[[<file>]]'` for an arrival.
- **One line a day** in `<pipeline>-runs.md` beside the pipeline: how many
  runs ended, how many failed, the last one with its time and error.
- **The run itself**, in `jobs.db`: every step with its status, attempts,
  error and output, shown by the Runs window and `memory_pipelines`, kept
  for `retention` after it ends.

A step that was running when the server stopped runs again at the next
start: a node should be safe to repeat (note.write with `upsert` is).

## Starting, waiting, resuming

**From an agent** — `memory_pipeline_run({path, input, idempotency_key,
callback_url, wait})` starts a run (write access to the project); `wait`
(at most 60 s) returns when it ended or waits. `{action: "resume", run,
data}` hands data to a waiting step, `{action: "cancel", run}` stops it.
`memory_pipelines({project, path?, run?})` reads pipelines, runs and steps.
`memory_bootstrap` says `capabilities.pipelines: true` where they run.

**From the web UI** — Menu → Runs: the pipelines and their runs, live.
Who writes in the project starts a pipeline (with a JSON input), resumes a
run that waits and cancels one; the pipeline window does the same beside
the drawing ([above](#editing-in-the-web-ui)).

**From a service** — a run token starts one pipeline whose trigger is
`external` or `manual`, and does nothing else:

```bash
docker exec gosidian gosidian pipeline token create \
  --pipeline team/pipelines/arrivals.md --name scanner
curl -X POST https://vault.example.com/api/v1/pipelines/team/pipelines/arrivals.md/runs \
  -H "Authorization: Bearer gosidian_run_…" -H "Idempotency-Key: scan-0042" \
  -d '{"input": {"file": "team/attachments/1f3c….pdf"}, "callback_url": "https://scanner.example.com/done"}'
```

The answer is `202` with the run (`200` and the same run for a key
already used). The token is shown once; `gosidian pipeline token list`
and `revoke <id>` manage them. A token starts at most 60 runs a minute.

**An asynchronous job** — a node hands `{{ run.resume_url }}` to a
service and a `wait.call` node waits for it: the service POSTs its result
there (any JSON) without an account, the key in the address works for
that run only and while it waits. The result is the `wait.call` node's
output. A wait that nobody resumes before its timeout fails.

**A callback** — `callback_url` (in `http_allowlist`) is POSTed the run's
summary when it ends: `{run, pipeline, project, status, error, started,
finished, steps: {name: status}}`.

REST reference: `internal/api/v1/spec/openapi.yaml`, tag `pipelines`.

## What a pipeline may do

- Read and write **only in its own project**; attachments and the files of
  the IN folder are read through the vault's confined paths (ADR-021,
  ADR-037), and moved only inside the IN folder. Every write goes through the same
  path as the MCP tools (ADR-034): the shrink guard, the size limits, the
  audit, the live events.
- Call **only the addresses in `http_allowlist`** (prefixes), redirects
  included. The language model providers are the owner's configuration.
- Use secrets **by name**: their values never appear in a note, a run or
  the audit.
- Call **only the registered plugins**, and only through the nodes of the
  manifest the owner accepted. A plugin gets the files the node names,
  nothing else of the vault; the files it gives back become attachments
  of the project and start no ingest pipeline.
- Run Starlark without file, network or module access, with a step cap.
- Spend at most `max_llm_tokens` per run, the tokens a plugin reports in its output's `usage` included ([plugins](plugins.md#a-nodes-call)).

## CLI

```
gosidian secret set <name>        # the value on standard input
gosidian secret list | delete <name>
gosidian pipeline token create --pipeline <path.md> --name <service>
gosidian pipeline token list [--pipeline <path.md>] | revoke <id>
gosidian plugin list | show <name> | accept <name> <sha> | forget <name>
```
