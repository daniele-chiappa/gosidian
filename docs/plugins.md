# Plugins

A plugin adds node types to the [pipelines](pipelines.md): converting
documents, reading pictures, computing embeddings, talking to a service.
It is an **MCP server** reached over Streamable HTTP, written in any
language and run beside gosidian, usually as a container (IMP-168). The
server calls it as an MCP client; the plugin never sees the vault, only
the files a pipeline hands it.

A plugin's nodes are named `<plugin>.<node>` (`docling.convert`) and run
only once the owner has **accepted the manifest** the plugin serves. A
plugin that later serves another manifest (a new version, other
permissions) stops running until its new manifest is accepted too.

## Registering a plugin

```toml
[[pipelines.plugins]]
name = "docling"                          # its node types: docling.<node>
url = "http://gosidian-docling:8000/mcp"  # the MCP endpoint
token_secret = "docling-token"            # gosidian secret set docling-token
timeout = "10m"                           # default: the step timeout
files_url = "http://gosidian:8080"        # where it fetches files; default [pipelines] files_url
```

`GOSIDIAN_PIPELINES_PLUGINS="docling=http://gosidian-docling:8000/mcp,…"`
registers plugins from the environment too (without a token: a token
needs `token_secret` in the file). The plugins exist only while
`[pipelines] enabled` is on.

Then read what the plugin asks for, and accept it:

```bash
docker exec gosidian gosidian plugin show docling
# Plugin:      docling 0.1.0
# SHA:         3f9c2a71d0e4…
# Reads files: yes, the files a pipeline hands it
# Calls:       none (declared: the plugin's container enforces it)
# Nodes:
#   docling.convert (tool convert), files in file
docker exec gosidian gosidian plugin accept docling 3f9c2a71d0e4
docker exec gosidian gosidian plugin list
```

`accept` takes the first characters of the sha `show` printed, so you
accept the manifest you read. The accepted manifests live in
`<state-dir>/plugins.json`; `gosidian plugin forget <name>` drops one.
The server checks every plugin once a minute: the Runs window and
`memory_pipelines` show each one's status and node types.

| Status | Meaning |
|---|---|
| `ok` | up, serving the accepted manifest: its nodes run |
| `unaccepted` | up, its manifest not accepted yet |
| `changed` | up, serving another manifest than the accepted one: its nodes fail until you accept the new one |
| `down` | unreachable, a wrong token, or no valid manifest |
| `unknown` | not checked yet |

## Writing a plugin

A plugin serves two kinds of tool: `gosidian_manifest`, and the tools
behind its nodes.

### The manifest

`gosidian_manifest` takes no arguments and returns the manifest, as
structured content or as JSON text:

```json
{
  "name": "docling",
  "version": "0.1.0",
  "gosidian": 1,
  "description": "Documents to markdown with docling",
  "nodes": [
    {
      "name": "convert",
      "tool": "convert",
      "description": "A document to markdown; its pictures come back as files",
      "files": ["file"],
      "input_schema": {"type": "object", "required": ["file"], "properties": {"file": {"type": "string"}}},
      "output_schema": {"type": "object", "properties": {"markdown": {"type": "string"}}}
    }
  ],
  "permissions": {"files": true, "network": []}
}
```

| Field | |
|---|---|
| `name` | the name it is registered with: lower-case letters, digits and `-` |
| `version` | any string; a new one asks for a new accept |
| `gosidian` | the manifest format: `1` |
| `nodes[].name` | the node's name: lower-case letters, digits and `_` (at most 32 nodes) |
| `nodes[].tool` | the MCP tool that runs it |
| `nodes[].files` | the settings that name a vault file: the tool gets the file's content in their place |
| `nodes[].file_refs` | `true`: the tool gets where to fetch each file instead of its content (below) |
| `nodes[].input_schema`, `output_schema` | JSON Schemas of the settings (as a pipeline writes them) and of the output, for the reader and the editor |
| `permissions.files` | the plugin reads the files a pipeline hands it (needed by `files`) |
| `permissions.network` | the hosts the plugin itself calls; empty for none. Declared: whoever runs its container enforces it |
| `sources` | the archives it reads, which the owner binds to IN folders (below): `name`, `kind`, `description`, `changes`, `move`. A manifest with sources may have no nodes |
| `permissions.source_hosts` | the hosts the URLs of its sources' files may point at, besides the plugin's own: gosidian reads no file from another (a name or an address, with a port or not) |

The sha that `accept` takes is the SHA-256 of the manifest as gosidian
reads it: a field it does not know, or the spacing of a schema, changes
nothing.

### A node's call

When a pipeline runs `docling.convert`, gosidian:

1. opens a session with the plugin (bearer token from `token_secret`, no
   redirects followed) and reads its manifest again: one other than the
   accepted one fails the node;
2. calls the node's tool with the node's settings (`with`, expressions
   resolved) as arguments. Each setting listed in `files` names a vault
   file of the pipeline's project, an attachment or a file of its
   [IN folder](in.md), and is replaced by
   `{"name": "1f3c….pdf", "mime": "application/pdf", "data": "<base64>"}`
   (20 MiB at most), or, for a node with `file_refs`, by
   `{"name": "fattura.pdf", "mime": "application/pdf", "size": 1048576, "sha256": "…", "url": "…/api/v1/run-files/<ticket>"}`;
3. takes the tool's **structured content** (else its text, read as JSON,
   else `{"text": …}`) as the node's output;
4. stores every **file** in the result as an attachment of the project,
   as the pipeline: an embedded resource with a blob
   (`file:///fig-1.png`, its name from the URI) or an image. The output
   gains `files`: `[{name, path, mime, markdown}]`, and `skipped` for the
   files it refused (an extension attachments do not take, more than 200
   files, more than 100 MiB together). These files start no ingest
   pipeline.

A tool result with `isError` fails the node with its text: a retry
policy on the node (`retry`) applies, as to any node. A call lasts at
most the node's `timeout` (5 minutes unless the pipeline sets one) and
the plugin's `timeout`, and never longer than the step timeout: a slow
conversion wants `timeout: 14m` on its node.

A node's output is at most 1 MiB, as any node's: a plugin that makes a
long text should keep it in its output once, not twice.

### Files by reference

A node with `file_refs` fetches each file it is handed with a plain
`GET` of its `url`, without a token: the ticket in the address is the key,
bound to the run and to the file, valid while the step may run (the step
timeout) and gone when the run ends. Ranges and retries work. The plugin
should check the size and the SHA-256 it was given. The URL starts with
the plugin's `files_url`, else `[pipelines] files_url`
(`GOSIDIAN_PIPELINES_FILES_URL`), else the public URL: on the network of
a compose file, `http://gosidian:8080` lets the plugin reach gosidian
directly, not through a proxy that caps the size of an answer. The
plugin then calls that address, which its manifest declares in
`permissions.network`.

### Sources

A plugin may offer **sources**: archives it reads (a Nextcloud folder, an
S3 bucket, a Drive folder), set up in the plugin with their credentials,
which never leave it. The owner binds one to a project's
[IN folder](in.md#sources), where it shows as a folder:

```bash
gosidian plugin bind storage fatture archive --as nextcloud --write
gosidian plugin list        # the plugins, then the bound sources
gosidian plugin unbind archive nextcloud
```

A source in the manifest:

```json
"sources": [{"name": "fatture", "kind": "webdav", "description": "Nextcloud, the folder Fatture", "changes": true, "move": true}],
"permissions": {"files": false, "network": ["nc.lan"], "source_hosts": ["nc.lan"]}
```

and four tools, each taking the source's name in `source`; paths are
relative to the source's root, with `/`, plain, no hidden segment (gosidian
leaves out the others, but for the batch marker `.done`):

| Tool | Arguments | Result |
|---|---|---|
| `gosidian_source_list` | `cursor` (`""` at first), `limit` | `files: [{path, size, modified, etag, sha256?}]`, `next` (the cursor of the next page; `""` at the last) |
| `gosidian_source_changes` | `cursor` | `changes: [{path, size, modified, etag, sha256?, deleted?}]`, `cursor`, `more?`, `reset?`; with `cursor` `""`, the cursor of now. Only with `changes` |
| `gosidian_source_open` | `path` | `url`, `size`, `etag`: where to `GET` the file, ranges too, for a while |
| `gosidian_source_move` | `from`, `to` | `path`, `size`, `etag`: the file, or the folder, moved inside the source. Only with `move` |

gosidian checks a bound source every minute (`--every`, at least 20
seconds): its changes from the cursor of the last check when it gives them,
else, and once an hour all the same, its list in full, a page at a time.
`reset: true` asks for the list. A file is new when its content changed:
another SHA-256 when both have one, else another etag or size. An etag
should stay the same when a file only moves.

The bytes of a file stay in the source until a node, a download or a
plugin with `file_refs` reads it: gosidian then asks `gosidian_source_open`
for a URL and reads it in ranges, streaming what it reads, with nothing on
its disk. A plugin node fetches a source's file through its run's ticket,
as any file: it calls gosidian, never the source. The URL must be http(s),
without credentials, on the plugin's host or a host of `source_hosts`;
gosidian follows no redirect, and an answer of `401`, `403`, `404` or `410`
asks for another URL once. For S3, `open` can give the archive's presigned
URL; for the others, an address of the plugin that streams the file.

gosidian moves a source's files only with a binding's `--write`, only
inside the source (a target outside it is read inside it), and never
deletes them.

### What gosidian does not do for a plugin

- It does not run it: the plugin is a container or a service its owner
  starts, with the network it needs, nothing more.
- It does not pass secrets: a plugin that calls a paid service keeps its
  own key, a source its credentials.
- It does not let it read the vault: the plugin gets the files a node
  names, in the pipeline's project, or a ticket for each, and gives back
  files and data.

## The docling plugin

`gosidian-docling` (repo `gosidian-plugins`, folder `docling/`) converts
PDF, DOCX, XLSX, PPTX, HTML and images to markdown with
[docling](https://github.com/docling-project/docling), on the CPU, its
models inside the image. Since 0.2.0 it takes its file by reference, any
size up to 512 MiB. Its node `docling.convert` takes `file`, `ocr`
(default true), `pictures` (default true), `max_pages` and
`images_scale`, and returns `markdown` (a picture is `![caption](fig-N.png)`),
`title`, `pages`, `tables` and `pictures` (`name`, `page`, `caption`);
its pictures come back as files.

A pipeline that turns each PDF or DOCX arriving in the project into a
note, its pictures described by a vision model (a describe that fails
leaves the pictures without a caption, `on_error: continue`):

````markdown
---
title: Arrivals
type: pipeline
---

```pipeline
trigger: {on: ingest, kinds: [pdf, docx]}
nodes:
  - id: convert
    use: docling.convert
    with: {file: "{{ trigger.file }}"}
    timeout: 14m
    retry: {attempts: 2, backoff: 1m}
  - id: describe
    use: llm.describe
    each: "{{ convert.files }}"
    with:
      image: "{{ item.path }}"
      prompt: "Describe this figure of a document in two sentences, in Italian."
      max_tokens: 300
    on_error: continue
  - id: compose
    use: starlark
    with:
      input: {markdown: "{{ convert.markdown }}", files: "{{ convert.files }}", captions: "{{ describe }}"}
      code: |
        md = input["markdown"]
        captions = input["captions"] or []
        for i, f in enumerate(input["files"] or []):
            caption = ""
            if i < len(captions) and captions[i]:
                caption = captions[i].get("text", "")
            link = "](/vault-files/" + f["path"] + ")"
            if caption:
                link += "\n\n> " + caption
            md = md.replace("](" + f["name"] + ")", link)
        output = md
  - id: save
    use: note.write
    with:
      path: "documents/{{ trigger.stem }}"
      title: "{{ convert.title }}"
      body: "{{ compose }}"
```
````
