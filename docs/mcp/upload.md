# Upload flow

**The decision tree is one line: to save a file, call `memory_ingest`.**

`memory_ingest` is the single front door (ADR-018): it routes by
extension — `.csv` → table note, image → media note, `.md`/`.html` →
the note itself (body read server-side, no tokens through the model
context), anything else → plain attachment — and accepts the file from
whichever channel is cheapest in your deployment. Everything below it
converges on the same code path (`internal/attach.Store`): identical
10 MiB cap, identical extension allowlist, identical content-addressed
filename (`<sha256[:16]>.<ext>`), identical magic-bytes verification
(rejects MIME spoofs).

## memory_ingest — sources, cheapest first

| Source | When | Cost |
|---|---|---|
| `bridge_filename` | Co-located deploys: stage the file in the bridge dir (its path is in bootstrap `capabilities.attachments.bridge_dir`), pass the basename; the server reads and **consumes** it | ~zero tokens |
| `source_path` | Server-resolved absolute path inside the vault, the bridge dir, or an allowed upload root (`GOSIDIAN_MCP_ALLOWED_UPLOAD_ROOTS`) | ~zero tokens |
| `transfer: "http"` | **Remote agents with a shell.** No source in the call: the response carries a **single-use upload URL** (TTL 5 min, no bearer — the ticket is the credential). POST the file there and the server executes the parked intent | 1 tool call + 1 curl |
| `url` | The file is already served somewhere the server can reach (CI artifact, internal screenshot service). Gated by the `ingest_url_allowlist` prefix allowlist (`GOSIDIAN_INGEST_URL_ALLOWLIST`), which also gates every redirect hop; empty = disabled | ~zero tokens |
| `attachment` | The bytes are already in the vault (e.g. from a previous `/upload` POST): promote them into a table/media note without re-uploading | ~zero tokens |
| `data` (base64) | Last resort, small files only: ~1 token per character through the model context | expensive |

Force the result kind with `as: table|media|note|attachment` when the
extension routing is not what you mean. In auto mode a table/media route
whose vault flag is off degrades to a plain attachment with a warning.
Note ingestion (`.md`/`.html`) supports `overwrite: true` with `if_match`
CAS.

### The ticket flow (`transfer: "http"`)

```jsonc
// 1. Declare the intent — no bytes yet
{ "name": "memory_ingest", "arguments": {
    "project": "Work", "transfer": "http",
    "title": "Q3 audit", "caption": "Export dal gestionale." } }
// → { "ticket": "…", "endpoint": "/mcp/ingest/<ticket>",
//     "expires": "…", "method": "POST", "field": "file", "single_use": true }
```

```bash
# 2. POST the bytes — same host as your MCP /sse URL, no Authorization header
curl -sf -F "file=@audit-q3.csv" "https://host/mcp/ingest/<ticket>"
# → the server executes the intent and answers like the tool would:
#   { "path": "Work/q3-audit.md", "kind": "table", "columns": [...], "rows": 812 }
```

The ticket is bound to the token that minted it (scope, audit, and
limits apply as if the bytes came through the MCP call) and is
**consumed by the first redemption attempt, success or not** — on
failure mint a new one. Redemption statuses: `200` executed, `404`
unknown/already consumed, `410` expired, `422` the intent failed
(e.g. invalid CSV), `413` over 10 MiB.

Agents behind an SSH tunnel that forwards only the web port (e.g.
`ssh -L 58080:server:8080`) reach every path through the same forward.
This is the deployment shape solved by the
[single-port mode](client-setup.md).

## The lower-level paths

The dedicated tools remain for explicit workflows (and for the web UI):

| Strada | Quando preferirla |
|---|---|
| **HTTP upload (`/upload`)** | Raw multipart upload authenticated by your **MCP bearer token**. The path mirrors your `/sse` endpoint (`/sse` → `/upload`); returns `{path, url, mime, kind, size}`. Pass the returned `path` to `memory_ingest` (or the note creators) as `attachment`. |
| **MCP `memory_upload_attachment`** | Single-step: upload + return a ready-to-splice markdown embed. |
| **MCP `memory_upload_resource`** | Two-step stage-then-attach: upload first, decide note placement later. |
| REST `/api/v1/upload` | Web-UI editor path (drag-and-drop). Authenticated by a **SPA** token (from login), not the MCP token. |

## HTTP upload endpoint

POST the bytes over HTTP, authenticated by the **same bearer token**
used for the SSE stream. No base64 through the model context, no login,
no shared filesystem. (Prefer the `memory_ingest` ticket flow above when
you also want the note created in the same round trip.)

**The path mirrors your `/sse` endpoint** — replace `/sse` with `/upload`:

| Your MCP `/sse` URL | Upload endpoint |
|---|---|
| `https://host/mcp/sse` (single-port web) | `https://host/mcp/upload` |
| `http://host:8765/sse` (legacy listener) | `http://host:8765/upload` |

```bash
# $UPLOAD = your /sse URL with /sse -> /upload
curl -X POST "$UPLOAD?project=Work" \
  -H "Authorization: Bearer $MCP_TOKEN" \
  -F "file=@diagram.png"
```

```json
{
  "path": "Work/attachments/3a7b9c4d2e1f5a6b.png",
  "url": "/vault-files/Work/attachments/3a7b9c4d2e1f5a6b.png",
  "mime": "image/png", "kind": "image", "size": 84213,
  "hash": "3a7b9c4d2e1f5a6b"
}
```

- Mounted on the single web port next to `/mcp/sse` — one SSH tunnel
  forwards both. `$BASE` is the same origin you point the MCP client at.
- Enforces the token's **write scope** and **project scope** (the
  `?project=` is intersected with a scoped token's project).
- Same `internal/attach.Store` pipeline: 10 MiB cap, extension
  allowlist, magic-bytes MIME verification.
- **Compose with media notes**: POST the image → take the returned
  `path` → `memory_create_media_note({attachment: path, caption: …})`
  (or `memory_create_table_note` for a CSV).
  The image lives once; agents read only the caption (ADR-013).

## REST `/api/upload`

### Contract

| Field | Value |
|---|---|
| Method | `POST` |
| Path | `/api/upload` |
| Content-Type | `multipart/form-data` |
| Auth | Web session cookie when web auth is enabled (`GOSIDIAN_LOGIN_*`); none otherwise. **No bearer token** — REST is browser-shaped, MCP-side bearer auth is enforced at the SSE handshake on `/mcp/sse`. |
| Size cap | 10 MiB (`attach.MaxBytes`) |

### Query params

| Param | Default | Notes |
|---|---|---|
| `project` | — | **Required.** Vault project name. Empty → `400 project query param is required`. |
| `kind` | `auto` | Informational hint — one of `image`, `document`, `auto`. Echoed back as `kind` in the response. Other values → `400 kind must be one of: image, document, auto`. |

### Body

Single multipart field:

- `file` — the binary payload. The multipart `filename` header
  determines the extension, which must be in the allowlist:
  - **Images**: `.png .jpg .jpeg .gif .webp .svg`
  - **Documents**: `.pdf .csv .json .txt .zip .docx .xlsx`

### Example

```bash
curl -i -X POST \
  -F "file=@/path/to/report.pdf" \
  "http://localhost:8080/api/upload?project=Work&kind=document"
```

Success (`200 OK`, `Content-Type: application/json`):

```json
{
  "path": "Work/attachments/3a7b9c4d2e1f5a6b.pdf",
  "url": "/vault-files/Work/attachments/3a7b9c4d2e1f5a6b.pdf",
  "mime": "application/pdf",
  "kind": "document",
  "size": 124589,
  "original_filename": "report.pdf",
  "hash": "3a7b9c4d2e1f5a6b"
}
```

### Response fields

- `path` — vault-relative location (`<project>/attachments/<hash>.<ext>`).
- `url` — relative URL served by gosidian under `/vault-files/...` with
  one-year immutable (private) cache. Intentionally relative so it resolves
  correctly against whatever host:port the caller used (`localhost`
  via tunnel, `127.0.0.1` direct, public hostname behind reverse proxy).
  Since v2.24.3 the URL requires the same authentication as the notes API:
  the browser sends the session cookie the SPA login sets, and any other
  client passes `Authorization: Bearer <token>` — a SPA session token or an
  MCP token with the `read` scope whose project scope covers the path.
  Anonymous requests get 401; a principal that cannot see the project
  gets 404.
- `mime` — canonical MIME from the allowlist (not the
  client-declared `Content-Type`, which is ignored).
- `kind` — `image` if the extension is in the image group, `document`
  otherwise. Reflects the allowlist, not the `?kind=` hint.
- `size` — bytes read from the multipart body.
- `original_filename` — preserved for use as link text in markdown
  references.
- `hash` — first 16 hex chars of the payload SHA-256. **Upload is
  idempotent by content**: the same bytes always produce the same path,
  so retries do not duplicate.

## MCP `memory_upload_attachment`

Single-step upload returning a ready-to-splice markdown embed.

```jsonc
{
  "name": "memory_upload_attachment",
  "arguments": {
    "project": "Work",
    "filename": "report.pdf",
    "data": "JVBERi0xLjQK..."   // base64 of file bytes
    // OR: "source_path": "/mnt/uploads/report.pdf"
  }
}
```

Returns:

```json
{
  "path": "Work/attachments/3a7b9c4d2e1f5a6b.pdf",
  "markdown": "[report.pdf](/vault-files/Work/attachments/3a7b9c4d2e1f5a6b.pdf)"
}
```

`markdown` is the embed in canonical form — image notation
`![](url)` for images, link notation `[name](url)` for documents — so
the caller can splice it directly into a `memory_edit` or
`memory_append` body.

Use `data` (base64) for cross-host setups (SSH tunnel, separate
container without shared volume); use `source_path` only when gosidian
and the agent share a filesystem (local install, mounted volume).
`source_path` is validated against
`GOSIDIAN_MCP_ALLOWED_UPLOAD_ROOTS` (vault root always allowed).

### Cheap ingestion: the bridge dir

`data` (base64) routes the file's bytes **through the model context**
(~1 token/char), which makes large images and illustrated notes
impractical to author. When `GOSIDIAN_MCP_BRIDGE_DIR` is set to a
directory shared with the agent's host, stage the file there and pass
**`bridge_filename`** (its basename) to any upload tool or to
`memory_create_media_note`:

```jsonc
{
  "name": "memory_create_media_note",
  "arguments": {
    "project": "Work",
    "bridge_filename": "screenshot.png",   // staged in the bridge dir
    "caption": "Login screen after the redesign"
  }
}
```

The server reads the staged file directly (near-zero token cost), runs
the same magic-byte/MIME validation and 10 MiB cap, and **consumes**
(deletes) the staged copy on success. The bridge dir is automatically an
allowed `source_path` root and its path is surfaced in bootstrap
`capabilities.attachments.bridge_dir`; a base64 upload larger than
~128 KiB returns a `hint` redirecting here. Pair it with image **media
notes** (ADR-013): upload once, then reference the media note by link —
agents read only the caption, never the bytes.

## MCP `memory_upload_resource`

Pre-uploader for the "stage, then attach" pattern.

```jsonc
{
  "name": "memory_upload_resource",
  "arguments": {
    "project": "Work",       // required
    "kind": "document",      // optional hint, default auto
    "filename": "report.pdf",
    "data": "JVBERi0xLjQK..."
  }
}
```

Returns the resource handle, no embed markdown:

```json
{
  "path": "Work/attachments/3a7b9c4d2e1f5a6b.pdf",
  "url": "/vault-files/Work/attachments/3a7b9c4d2e1f5a6b.pdf",
  "mime": "application/pdf",
  "kind": "document",
  "size": 124589,
  "filename": "3a7b9c4d2e1f5a6b.pdf",
  "hash": "3a7b9c4d2e1f5a6b"
}
```

Identical storage and validation to `memory_upload_attachment` — the
difference is purely that the caller decides when and how to embed the
file. Typical pattern: upload N resources, then call `memory_edit` once
with all the embeds composed by hand.

## Errors

All three paths share the same validation pipeline; only the error
shape differs (HTTP `text/plain` body for REST, MCP error result for
the tools).

| Cause | REST status / body | MCP error |
|---|---|---|
| Missing `project` | `400 Bad Request` `project query param is required` | `project must not be empty` (resource only — attachment allows empty) |
| Bad `kind` value | `400 Bad Request` `kind must be one of: image, document, auto` | `kind must be one of: image, document, auto` |
| Unparseable multipart | `400 Bad Request` `bad multipart: <details>` | n/a — MCP encodes args as JSON |
| Missing `file` field / no source | `400 Bad Request` `missing file field: ...` | `provide one of: bridge_filename (staged file), source_path (server path), or data (base64). …` (the error teaches the HTTP and ticket channels) |
| Wrong method | `405 Method Not Allowed` `method not allowed` | n/a |
| File too large (> 10 MiB) | `413 Request Entity Too Large` `file too large (max 10 MiB)` | same message |
| Extension not allowlisted | `415 Unsupported Media Type` `unsupported file type: .exe` | same message |
| Magic-bytes mismatch | `400 Bad Request` `MIME mismatch: declared extension ".png" expects image/png, content detected as text/plain; charset=utf-8` | same message |
| Disk / vault I/O failure | `500 Internal Server Error` `save: <details>` | `<details>` |

The magic-bytes check (added in v1.11) inspects the first 512 bytes
with `http.DetectContentType` and rejects payloads whose detected MIME
family does not match the declared extension. SVG is treated as a
text/XML format, DOCX/XLSX as zip containers — see
`internal/attach/attach.go:VerifyMIME` for the per-extension tolerance
rules.

## Layout on disk

After a successful upload (any path), the file lives at:

```
<vault>/<project>/attachments/<hash>.<ext>
```

For uploads with no project (REST omitting `?project=` is rejected;
the MCP `memory_upload_attachment` allows it):

```
<vault>/attachments/<hash>.<ext>
```

Two callers uploading the same bytes produce the same hash → the same
file → no duplication. To delete an attachment, use
`memory_delete_attachment` (does **not** rewrite notes that reference
it — check `memory_attachment_info` first to find references).

## See also

- [Tool catalogue](tools.md#attachments) — full tool list with
  signatures
- [Client setup](client-setup.md) — the single-port endpoint that
  serves both `/api/upload` and `/mcp/sse` on the web port
- [Authentication](authentication.md) — bearer token scoping for the
  MCP tools (REST does not use bearer auth)
