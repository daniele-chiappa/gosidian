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

A project's pipelines put git tags on the vault's commits
([`vault.commit`](#revisions-as-git-history)) only with a second flag,
`pipeline_tags`, off by default too (chip `git tags`, shown once the
pipelines are on, or `{"pipeline_tags": true}`).

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
`_MAX_WAIT`, `_MAX_LLM_TOKENS`, `_MAX_INPUT_BYTES`, `_MAX_OUTPUT_BYTES`,
`_HTTP_ALLOWLIST` (comma-separated),
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
| `chat` | a message of a chat, the rating of an answer, or a request for a person, from the chat plugin with a run token (or by hand, to try it): its runs are [interactive](#interactive-runs) | `kind` (`message`, the default, `rating` or `handoff`); a message: `message`, `history` (`[{role: user or assistant, content}]`), `scopes` (the names the person chose), `session`, `user`, `as_of` (a day, YYYY-MM-DD); a rating: `turn` (the id of the answer's run), `rating` (-10 to 10), `comment`; a request for a person: `message`, `contact`, `history`, `session`; other keys as given |
| `ingest` | a file of one of `kinds` is stored as an attachment of the project (with `folder`: a note made with it in that folder) | `file`, `name`, `stem`, `ext`, `folder`, `note`, `project` |
| `schedule` | `every: "monday 09:00"`, `"day 18:30"`, `"hour :05"` (every hour, at five past; `"hour"` on the hour) — the automations' syntax — once per slot; a slot missed by more than 10 minutes is not run late | `slot` |
| `in` | a batch of files of the project's [IN folder](in.md) closes: one run per file it takes, or one per batch with `batch: true`; `folder` (of the IN folder), `kinds` (none: all) and `match` (a glob on the name) choose the files; `take: false` reads them without taking them (their status stays the other pipelines') | per file: `file`, `name`, `stem`, `ext`, `dir`, `size`, `sha256`, `etag` (a source's file), `source` (`local`, or `<plugin>/<source>`), `project`; per batch: `batch`, `dir`, `count`, `bytes`, `files` (the files, when they fit in the input; else `files_listed` is false and `in.list` reads them), `project` |
| `commit` | a commit of the vault's [git sync](configuration.md#environment-variables) changes files under `folder` (of the project; empty: all of it) whose name matches `match` (a glob, `*.md`): one run per commit | `commit` (its SHA), `date` (in seconds), `project`, `folder`, `count`, `files` (`path`, `change`: `added`, `modified`, `deleted`, `renamed`, and `from` for a renamed file; when they fit in the input, else `files_listed` is false), `files_listed` |

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

A `commit` run is keyed by the commit: the same commit starts no second
run. It is for a pipeline that keeps something beside the notes, such as
the chunks of an archive of vectors ([below](#chunks-for-an-archive-of-vectors)),
and follows their versions: the commit is the version. A file whose last
write was a run of the same pipeline, or of a pipeline it starts with
`pipeline.run` (named as written, not by an expression), is left out of
`files` (the authors the index keeps from the audit), so that a pipeline
writing in its own folder does not start itself again; so are the runs
notes, which every run's end rewrites, or two commit pipelines of the same
folder would start each other forever. A commit of such files only starts
none. Git sync commits the whole vault, some seconds after the last change
(`commit_debounce`): a run reads the notes as they are when it runs, maybe
newer than the commit. With git sync off a commit pipeline starts only by
hand, and the Runs window says so.

Any pipeline also starts by hand, whatever its trigger: an ingest pipeline
run with `{"file": "team/attachments/….pdf", …}` as input works on that
file again, and the IN window starts a pipeline on the files chosen, the
way its trigger would: once per file for an `in` pipeline per file, once
for the others. Another run starts any pipeline of its project with
[`pipeline.run`](#one-pipeline-starting-another): its trigger is then
`pipeline`, a trigger no pipeline declares.

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

Beside `trigger` and `nodes`, a pipeline may say:

| Field | |
|---|---|
| `answer` | the node whose output is the run's answer: a start that [waits](#starting-waiting-resuming) gets it back, and so does the callback |
| `priority` | `interactive` for a pipeline whose runs someone waits for ([below](#interactive-runs)); `normal` (the default) otherwise |

#### Interactive runs

A chat's message cannot wait behind a batch of thousands of documents.
The steps of an interactive run (a pipeline with `priority: interactive`,
or one that starts on `chat`, and the runs such a run starts with
`pipeline.run`) are taken before the others', and `[pipelines]
interactive_workers` (default 1, on top of `workers`) run only them: a
message gets a worker even while the others are all busy. A long step of
an interactive pipeline holds that lane while it runs: keep them short.

### Expressions

`{{ name.field.0 }}` reads `trigger`, `run` (`id`, `pipeline`,
`project`, `trigger`, `resume_url`, `resume_key`, `created`: when the run
was made, `2026-10-15T07:30:00Z`), the output of a node
that runs before, and `item`/`index` inside an `each`. A string that is
one expression keeps the value's type (a list, a number); text around
an expression turns it into text. There are no operators: compute in a
`starlark` node. A key of a map may hold expressions too, written into
it as text: `ratings: {"{{ trigger.turn }}": "{{ trigger.rating }}"}`
(two keys that give the same text fail the node).

### Node types

| Type | `with` | Output |
|---|---|---|
| `note.read` | `path` (inside the project; relative to it), `media`: `keep` (default) or `inline`, the images as text ([below](#chunks-for-an-archive-of-vectors)), with `media_words` and `inline_max_side` | `path`, `title`, `frontmatter`, `body`, `etag`; with `media: inline`, `media` (the images counted) |
| `note.write` | `path`, `body`, `title`, `frontmatter` (a map), `mode`: `upsert` (default), `create`, `replace`, or `append` (adds `body` after the note's, a blank line between, and merges `frontmatter` into its own; a note it does not find it creates), `frontmatter_merge` (merges `frontmatter` into the note's instead of replacing it, and keeps its body unless `body` is given: a map merges into a map, `null` removes a key) | `path`, `etag`, `created` |
| `note.list` | `folder` (inside the project, relative to it; empty: all of it), `recursive` (default yes), `where` (memory_query's conditions, `{field, op, value}`), `sort` (default `path`), `fields` (frontmatter fields to give), `limit` (default 500, at most `[pipelines] max_list_items`, 5000), `cursor` | `notes` (`path`, `title`, `fields`), `count` (this page), `total` (all that match), `next` (the cursor of the next page; empty at the last) |
| `note.sections` | `path`, `version` (ends each chunk's id after an `@`), `levels` (default `[section, paragraph]`; `[paragraph]` still cuts the sections, the paragraphs' parents and paths, but gives the paragraphs only), `max_chars` (default 6000; 200 to `max_output_bytes`), `context`: `heading` (default), `path`, `none`, `text` (default yes), `only` (bases, up to `max_list_items`), `meta` (added to each chunk's, up to `max_chunk_meta` bytes), `media`: `inline` (default) or `keep`, `media_words`, `inline_max_side`, `media_numbers` | `doc`, `title`, `version`, `sections`, `paragraphs`, `chunks` (as an archive's `upsert` takes them; with `media_numbers`, each with its `media`), `count`, `media` — see [below](#chunks-for-an-archive-of-vectors) |
| `note.mark` | `path`, `set` (a name: `cut`, `review`), `marks` (each `base`, a chunk of `note.sections`, or `section`, the path of its headings, and `block`, its paragraph from 1; `color`: `blue`, `green`, `orange`, `red`, `cyan`, `grey`; `label`, `text`) | `path`, `set`, `count` — see [below](#marks-on-a-note) |
| `wait.call` | `timeout` (default `24h`, at most `max_wait`) | the data the resume hands it |
| `pipeline.run` | `pipeline` (a pipeline note of the project, relative to it), `input` (a map, the started run's `trigger`), `wait` (default yes), `timeout` (of the wait: default `24h`, at most `max_wait`), `key` | with `wait`: `run`, `pipeline`, `status`, `error`, `steps` (each node's status), `outputs` (each node's output, when they fit); without: `run`, `pipeline`, `status`, `existing` — see [below](#one-pipeline-starting-another) |
| `starlark` | `code` setting `output`, a script (loops and `if` at the top level, `while`, `set`); `input`; the run's values in `ctx`, `json.encode`/`json.decode`; no `load`, no I/O, 10 million steps at most | the value of `output` itself: `{{ node }}`, or `{{ node.field }}` for a map |
| `http.request` | `url` (in `http_allowlist`), `method`, `json` or `body`, `headers`, `secret_header: "Authorization=secret_name"`, `accept_errors` | `status`, `headers`, `json` or `body` (4 MiB at most) |
| `llm.chat` | `prompt`, `system`, `provider` (default `default`), `model`, `max_tokens` (default 1024), `temperature`, `json: true`, `reasoning_effort` (`none`, `low`, `medium`, `high`: how much a model that reasons first may think; `none` turns it off on Ollama — a thinking model can spend all `max_tokens` before it answers) | `text`, `json`, `model`, `provider`, `usage`, `finish_reason` (`length` when `max_tokens` ran out), `reasoning` (the model's thinking, when it gives it) |
| `llm.describe` | `image` (an attachment of the project, png/jpg/gif/webp, 10 MiB at most), `prompt`, and as `llm.chat` | as `llm.chat`, plus `image` |
| `llm.describe_media` | `note` (whose image notes, in `<note>_img/`, get a description), `prompt` and `inline_prompt` (with `{title}`, `{section}`, `{context}`), `inline_max_side` (default 128: an image in a sentence with a longer side, a screenshot, gets `prompt`; 0: every one gets `inline_prompt`), `images` (`all`, the default; `icons`, the images in a sentence up to `inline_max_side`; `others`, the rest: two passes, each with its own model), `context_chars` (default 1200), `max_pixels` (default 2000000; 0 sends an image whole), `concurrency` (default 1, at most 8), `prompt_version` (default 1), `redo`, and as `llm.chat` (`max_tokens` default 600) | `described`, `skipped`, `unreadable`, `excluded` (left to another pass by `images`), `failed`, `downscaled`, `errors`, `usage`, `seconds` — see [below](#documents-from-the-in-folder) |
| `in.list` | `folder` (of the IN folder; empty: all), `recursive` (default yes), `match` (a glob on the name), `kinds`, `status`, `batch` (`{{ trigger.batch }}`), `limit` (default 500, at most 5000), `cursor` | `files` (`path`, `name`, `stem`, `ext`, `dir`, `size`, `sha256`, `etag`, `status`, `modified`, `batch`, `source`), `count`, `next` (the cursor of the next page; empty at the last) |
| `in.move` | `file` (in the IN folder), `to` (a folder, `done/`, or a path, in the IN folder; empty, or where it is already: it stays; for a file of a [source](in.md#sources), inside the source: `done/` is its `done/`), `status` (`done`, `error`, `ignored`, `new`) | `path`, `from`, `status` |
| `in.import` | `file` (a `.md` in the IN folder), `dest` (a folder of the project), `base` (the folder of the IN folder whose tree is kept under `dest`; empty: all of it), `media_notes` (default yes), `list_images` (default yes; no keeps only their count, for an `each` over many files), `convert` (a node that converts the images, `images.convert`), `convert_with` (its settings) and `convert_batch` (the images of one call, default 25) | `path`, `created`, `attachments`, `image_count`, `images` (`note`, `attachment`, `src`, `position`, `inline`), `removed`, `unresolved`, `skipped` (`src`, `reason`), with `convert` `converted` and `convert_errors` — see [below](#documents-from-the-in-folder) |
| `vault.commit` | `message` (default `pipeline <path>: run <id>`), `tag` (a git tag on the commit, with at least one `/`; the project needs `pipeline_tags`) | `commit` (the SHA; HEAD's when nothing was pending), `short`, `committed` (false when nothing was pending), `tag`, `pushed`, `push_error` — see [below](#revisions-as-git-history) |
| `<plugin>.<node>` | what the plugin's manifest says; a setting it lists in `files` names an attachment of the project or a file of its IN folder (or a list of them, for a plugin that takes files by reference: `{{ trigger.files }}`) | the plugin tool's result, plus `files` (`name`, `path`, `mime`, `markdown`): the files it gave back, stored as attachments |

A plugin's nodes exist once the owner accepted its manifest
(`gosidian plugin accept`, [plugins](plugins.md)); `memory_pipelines`
and the Runs window list them.

A node's output is at most 1 MiB (`max_output_bytes`): write a large text
into a note and pass its path. A run's input is at most 256 KiB
(`max_input_bytes`): pass files by their vault path, not their bytes. Both
are settings: a pipeline that keeps many results in a step (a test of a
search over hundreds of questions, say) needs a larger output, and a
plugin's own cap goes up with it.

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

## Documents from the IN folder

Markdown already written, with its images beside it (a wiki exported
with its pictures, a documentation folder), comes into the project with
`in.import`, and a vision model describes its images with
`llm.describe_media` (IMP-179):

```pipeline
trigger: {on: in, folder: wiki, kinds: [md]}
nodes:
  - id: import
    use: in.import
    with: {file: "{{ trigger.file }}", base: wiki, dest: documents/wiki}
  - id: describe
    use: llm.describe_media
    with: {note: "{{ import.path }}", concurrency: 4}
    timeout: 2h
    retry: {attempts: 3, backoff: 1m}
  - id: done
    use: in.move
    with: {file: "{{ trigger.file }}", status: done}
```

**`in.import`** makes `IN/wiki/a/b/page.md` the note
`documents/wiki/a/b/page.md`:

- the files it links (relative links, inside `base`) are stored as
  attachments, named by their content, so a file linked twice is stored
  once, and the links point at them (`/vault-files/…`), as a package import
  does; a link to another `.md` becomes a wikilink to the note it becomes;
  a link it cannot follow stays as written and is listed in `unresolved`;
- the rest of the text and the document's frontmatter stay as written; it
  adds `pipeline`, `pipeline_run`, the project's tag when there are no
  tags, and `in_file`, the file's path in the IN folder, as text: a file of
  the IN folder is no note, and a wikilink to it would never resolve;
- each image gets an [image note](vault/format.md#image-media-notes) in
  `page_img/`, named after the image's file, with no title until it is
  described (so that a document's image notes do not outrank it in a
  search): `type: image`, `media` (the
  attachment), `article` (the note), `position` and `occurrences` in the
  note, `inline` (inside a sentence), `section`, `alt_original`, `width`,
  `height`, `in_sha256`, `in_file`. The document points at the image, the
  image note at the document: it lists its images among its backlinks;
- imported again, an image note keeps its description and any key added
  to it; an image that changed loses its description; the image note of
  an image no longer there goes (into the trash). A note of the folder that
  is not an image note of the document is left alone;
- the files it read are `done`, with its run;
- with `convert: images.convert` ([the images plugin](plugins.md)), the
  document's PNG, JPEG, GIF, WebP and TIFF images go to that node before
  they are stored, 25 at a time (`convert_batch`, at most the server's
  `max_plugin_files`), by reference, with `convert_with` as its
  settings (`{format: webp}`): the vault gets the converted file, and the
  note and the image note point at it. An image the node keeps (it would
  not get lighter) or fails on is stored as it is, the failures listed in
  `convert_errors`. The node takes the images as `files` and answers with
  `results` in their order, each naming the file it gave back (`output`),
  as `images.convert` does.

An image note keeps `in_sha256`, the SHA-256 of the original in the IN
folder: its description goes only when the original changed. A new
conversion of the same original (another format, a plugin that writes
other bytes) changes its `media` and keeps the description.

**`llm.describe_media`** describes the image notes of a note that have
no description yet: the model gets the image, the note's title, the
headings the image is under and the text around it (`context_chars`, two
thirds before it and a third after, within its section; for an image
inside a sentence, the sentence), and answers in JSON with a `label` and a
`description`. The description is the image note's body, the text the
search finds; the label is its `title` and `label`, and `model`,
`prompt_version` and `described` go into its frontmatter too. Each note is written as soon as it is described: a step
cut short, or run again, goes on from those still without one (`redo`
describes them all again). An image that fails three tries gets
`describe_error` and is counted in `failed`; the step fails when more than
half fail, or when the run's `max_llm_tokens` would be passed. svg images
are left out (`unreadable`). An image of more than `max_pixels` goes to the
model scaled down, as a PNG, its pixels averaged so that text stays
readable; the attachment stays whole. A model reads an image at about one
token per 32×32 pixels (Qwen-VL): a 4K screenshot passes the 4096 tokens of
context Ollama gives a model by default.

### Chunks for an archive of vectors

**`note.sections`** cuts a note by its headings (IMP-170), into the chunks
an archive of vectors keeps ([the database plugin](plugins.md)'s `upsert`
takes them as they are: `id`, `text`, `title`, `doc`, `parent`, `level`,
`meta`):

- a **section** for each heading with text of its own, and for the text
  before the first heading; a heading with none (only subheadings under
  it) is in the path of those under it. A line in a fenced block is no
  heading. A section longer than `max_chars` is cut into parts at its
  paragraphs (a paragraph longer than that is a part alone);
- a **paragraph** (`level` 2, `parent` its section or part) for each block
  between blank lines: a list, a table, a fenced block whole;
- the text of each starts with its section's heading (`context: heading`),
  the title and the path of the headings (`path`) or nothing (`none`); its
  `title` is the note's title and the path, which a preset's document
  template may use;
- its **base** names it from one version of the note to the next: the
  note, the path of its headings (and which, when two have the same), its
  part, its paragraph. The `id` is the base, and `@` and `version` when
  given (a commit), so the chunks of two versions live side by side;
  `meta` holds `base`, `hash` (of the text), `chars`, `section`,
  `position`, and the keys of `with.meta`;
- `text: false` gives each chunk's `id`, `parent`, `level`, `base`,
  `hash` and `chars` only: what tells which chunks changed against those
  stored (a plugin's `list` with `fields`), within a step's output
  (`max_output_bytes`) for a long note. `only` then gives the chunks of some bases with
  their text, a batch at a time for `upsert`.

With **`media: inline`** (the default of `note.sections`; `note.read` and
`memory_get` take it too) the images become text, from their image notes:
an icon, an image inside a sentence up to `inline_max_side` pixels (128),
`[icon: label]` in its sentence; a larger image in a sentence
`[image: label (description)]`; an image on a line of its own a paragraph,
`[image: label] description`. `media_words` changes the words
(`{icon: icona, image: immagine}`). An image without a description keeps
its alt text, unless Word wrote it (`… Descrizione generata
automaticamente`). `media` counts the images, the icons, those described,
those without (`missing`) and the alt texts left out (`word_alt`).

With **`media_numbers: true`** `note.sections` numbers the images (not the
icons) in the note's order, `[image 3: label] description`, and gives each
chunk its `media`: the images whose text it holds, each with its number
`n`, its `path` in the vault (none for an image from outside), its image
`note` and its `label`. A pipeline that answers from the sections can then
ask the model to mark where an image goes, `[image 3]`, and put the image
there: the chunks of an archive keep the words without numbers, which
would change from one version of a note to the next.

### Marks on a note

**`note.mark`** puts a set of coloured marks on a note (IMP-188): spans of
it — a chunk of `note.sections` by its `base`, or a section by the path of
its headings, whole or its paragraph `block` — each with a colour, a label
beside it and a text on hover. The web UI shows them in the note window's
cut view, beside the text, never in it: written into the note, they would
change the author's text, make a commit at each run and grow old with the
rules that made them.

- A run replaces its set (`set`); a set without marks goes. A note has at
  most 20 sets, a set 500 marks; a label 60 characters, a text 500
  (`[pipelines] max_mark_sets`, `max_marks`, `max_mark_label`,
  `max_mark_text`).
- The sets live in `marks.db` in the state dir, out of the vault and of
  git; a note renamed through gosidian takes its sets along, deleted loses
  them.
- A set keeps the SHA-256 of the note it was made on: once the note
  changed, the web UI says the set is of an earlier version, and still
  places its marks where their sections and paragraphs are. A mark whose
  place is gone is counted.
- The cut view lists the sets, each to hide or show; the note window's
  «Cut» has a dot while the note has some.

```yaml
  - id: mark
    use: note.mark
    with:
      path: "{{ trigger.note }}"
      set: cut
      marks:
        - {base: "{{ cut.chunks.0.meta.base }}", color: red, label: no description}
        - {section: "Setup > Requirements", block: 2, color: orange, label: long}
```

## Revisions as git history

A document that comes in revisions (V1, V2, V3…, each replacing the one
before) keeps one note, at the same path, and its revisions in the vault's
git history: one commit per revision, and a tag that names it
(IMP-201). Git sync commits after `commit_debounce` (30 s), so revisions
written one after another would end in one commit; **`vault.commit`**
commits at once:

````markdown
---
title: Revision
type: pipeline
---

```pipeline
trigger: {on: manual}
nodes:
  - id: convert
    use: docling.convert
    with: {file: "{{ trigger.file }}"}
    timeout: 14m
  - id: write
    use: note.write
    with:
      path: "manuals/{{ trigger.codice }}"
      title: "{{ convert.title }}"
      body: "{{ convert.markdown }}"
      frontmatter: {codice: "{{ trigger.codice }}", revisione: "{{ trigger.revisione }}"}
  - id: commit
    use: vault.commit
    with:
      message: "{{ trigger.codice }}: revision {{ trigger.revisione }}"
      tag: "doc/{{ trigger.codice }}/{{ trigger.revisione }}"
```
````

Started with `{"file": "IN/manuals/manual-A-en-V2.docx", "codice":
"manual-A-en", "revisione": "V2"}`, it writes the revision over the note
and makes the commit `manual-A-en: revision V2`, tagged
`doc/manual-A-en/V2`. Then `git show doc/manual-A-en/V2:team/manuals/manual-A-en.md`
gives that revision back, `git tag -l 'doc/manual-A-en/*'` lists them, and
the note's history shows one entry per revision.

- **The commit** is git sync's: everything pending in the vault, not only
  what the run wrote, with git sync's author and the same files left out
  (the projects with `skip_git_sync`, the IN folders). The commit pipelines
  start on it as on any commit, without the files their own pipeline wrote
  last. With nothing pending (a debounced commit took the run's writes a
  moment before), `committed` is false and the tag goes on HEAD, the
  commit that holds them.
- **The tag** needs the project's `pipeline_tags`. It is annotated, with
  the message. Besides git's rules for a ref name (no space, `..`, `~^:?*[\`,
  part starting with `.` or ending with `.lock`, leading `-`…; at most 200
  bytes), it must have at least one `/`, and its first part must be
  neither `v` and a digit (`v2.71.3` is a release tag, should the vault and
  the code ever share a remote) nor one of git's own names (`refs`,
  `heads`, `tags`, `remotes`, `origin`, `HEAD`). The tags are the vault's,
  shared by every project: two projects that name theirs alike collide.
- **A tag never moves**: the same tag already on the same commit is fine
  (a step tried again), on another commit the step fails. A revision
  committed again needs a new name.
- **The push**: with `GOSIDIAN_GIT_PUSH`, the branch and the tag go at
  once. A failed push does not fail the step: `pushed` is false,
  `push_error` says why, and git sync's next push takes the tag along (it
  pushes with `--follow-tags`).
- **Without git sync** (`GOSIDIAN_GIT_ENABLED` off) the node fails: «this
  server keeps no git history».
- **In order**: start the revisions one after the other, each waiting for
  the one before (`memory_pipeline_run` with `wait`, or `"wait"` in the
  start's body). Runs started together, an `each` without `wait`, end in
  any order: the tags still name the right revisions, but the history no
  longer reads V1, V2, V3.

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
  and, for an arrival, the file it came from (`trigger.file`): an
  attachment as `source: '[[<file>]]'`, a file of the IN folder as its
  path, as text, in `in_file` (a file of the IN folder is no note, and a
  wikilink to it would never resolve).
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
(in seconds, at most `[pipelines] max_sync_wait`, 60 by default) returns
when it ended or waits for a call, with the steps and the pipeline's `answer`. `{action: "resume", run,
data}` hands data to a waiting step, `{action: "cancel", run}` stops it.
`memory_pipelines({project, path?, run?})` reads pipelines, runs and steps.
`memory_bootstrap` says `capabilities.pipelines: true` where they run.

**From the web UI** — Menu → Runs: the pipelines and their runs, live.
Who writes in the project starts a pipeline (with a JSON input), resumes a
run that waits and cancels one; the pipeline window does the same beside
the drawing ([above](#editing-in-the-web-ui)).

**From a service** — a run token starts one pipeline whose trigger is
`external`, `chat` or `manual`, and does nothing else:

```bash
docker exec gosidian gosidian pipeline token create \
  --pipeline team/pipelines/arrivals.md --name scanner
curl -X POST https://vault.example.com/api/v1/pipelines/team/pipelines/arrivals.md/runs \
  -H "Authorization: Bearer gosidian_run_…" -H "Idempotency-Key: scan-0042" \
  -d '{"input": {"file": "team/attachments/1f3c….pdf"}, "callback_url": "https://scanner.example.com/done"}'
```

The answer is `202` with the run (`200` and the same run for a key
already used). The token is shown once; `gosidian pipeline token list`
and `revoke <id>` manage them. A token starts at most `[pipelines]
run_token_rate` runs a minute, 60 by default: a chat's widget starts one
per message of everyone who chats.

**Waiting for the answer** — with `"wait": 30` in the body, or `?wait=30`,
the start waits up to that many seconds (at most `[pipelines]
max_sync_wait`) for the run to end: `200` with the run and `answer`, the
output of the node the pipeline names as its answer (none when it names
none, or the node did not end done). A run that stops at a `wait.call`
comes back as it waits; one that waits only for the runs it started
(`pipeline.run`) is still at work, and the start waits on. One that did
not end in time comes back `202`, as without `wait`, and
`GET /api/v1/runs/<id>` follows it.

**An asynchronous job** — a node hands `{{ run.resume_url }}` to a
service and a `wait.call` node waits for it: the service POSTs its result
there (any JSON) without an account, the key in the address works for
that run only and while it waits. The result is the `wait.call` node's
output. A wait that nobody resumes before its timeout fails.

**A callback** — `callback_url` (in `http_allowlist`) is POSTed the run's
summary when it ends: `{run, pipeline, project, status, error, started,
finished, steps: {name: status}, answer}` (`answer` when the pipeline
names one).

### One pipeline starting another

A job many pipelines need, such as describing the images of an article,
lives in a pipeline of its own, and each pipeline that imports documents
ends by starting it with `pipeline.run`:

```pipeline
nodes:
  - id: import
    use: in.import
    with: {file: "{{ trigger.file }}", dest: documents}
  - id: describe
    use: pipeline.run
    with:
      pipeline: pipelines/describe-images
      input: {note: "{{ import.path }}", domain: "the product manual"}
```

- **The run it starts** belongs to the same project, has the trigger
  `pipeline`, acts as the starting pipeline in the audit, and reads `input`
  as its `trigger`. It is keyed by the step that started it: a step tried
  again finds the same run, and `key` tells apart the runs one node starts
  of the same pipeline.
- **With `wait`** (the default) the step waits, as `wait.call` does, until
  that run ends, and gives its outcome: the status of its nodes and their
  outputs (`{{ describe.outputs.<node> }}`) when they fit in a step's
  output. When that run fails or is canceled the step fails, with the same
  outcome for the nodes after it that continue on error. A run that ends,
  is canceled or fails while it waits (its wait expired) cancels the run it
  waited for.
- **Without `wait`** the step gives the run it started and goes on: in an
  `each`, one run per element, each with its own step timeout and its own
  `max_llm_tokens`. An `each` that waits is refused.
- **A chain** of runs starting each other stops at four below the first
  (`[pipelines] max_run_depth`): the fifth fails. A pipeline that starts
  itself does not run forever.
- **One run starts at most 1000 runs** (`[pipelines] max_run_children`),
  in an `each` too: the element past it fails the step, and the runs it
  started stay. Without it, an `each` over thousands of notes in a
  pipeline that starts itself would start them at every level.

To work again on notes a pipeline did not make, `note.list` gives the
notes of a folder, a page at a time:

```pipeline
nodes:
  - id: articles
    use: note.list
    with: {folder: "{{ trigger.folder }}", where: [{field: type, op: ne, value: image}]}
  - id: describe
    use: pipeline.run
    each: "{{ articles.notes }}"
    with: {pipeline: pipelines/describe-images, input: {note: "{{ item.path }}"}, wait: false}
```

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
- Start **only pipelines of its own project** (`pipeline.run`), in a chain
  at most four runs deep, at most 1000 from one run (both settings).
- Commit the vault as git sync does (`vault.commit`), and tag a commit
  only with the project's `pipeline_tags`; a tag never moves.
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
