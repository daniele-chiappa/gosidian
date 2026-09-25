# MCP tool catalogue

**57 tools** cover the full retrieval → write → workflow →
orchestration → self-check cycle for agent memory.

Consult each tool's `description` via your client's `tools/list` call
for precise schemas; the groupings below are the conceptual map.

## Triage / bootstrap

- `memory_bootstrap(project, known_directives_version?, known_etags?,
  mode?)` — single-call session start: hot state + active plans +
  skills + recent notes + top tags. The optional knobs slim repeat
  bootstraps down to a fraction of the tokens: a matching
  `known_directives_version` omits the directives block, `known_etags`
  (path → etag from the previous call) returns unchanged files as
  `unchanged:true` with no body, and `mode="lite"` replaces the
  `hot.md` body with its frontmatter + heading outline. `mode` defaults
  to **auto**: an oversize `hot.md` is served lite automatically
  (flagged `auto_lite:true`); pass `mode="full"` to force the body.
  The payload also carries a `maintenance` digest (hot.md size/age,
  broken wikilinks, stale-note count — indexed queries only): when its
  `attention` flag is true, the directives ask the agent to propose the
  relevant grooming at end of task
- `memory_recent(project)` — notes recently modified
- `memory_pinned(project)` — notes tagged `pinned`
- `memory_stale(project, older_than, exclude_closed?)` — unmodified long
  enough to review/archive; each note carries `closed` (tagged
  `status:done` / `status:archived`) and `exclude_closed: true` drops
  them, which is exactly what the bootstrap `maintenance.stale_count`
  counts
- `memory_plans(project, status)` — typed plan retrieval by status
- `memory_skills(project, trigger_phrase?)` — reusable procedures
- `memory_notes_by_importance(project, min_level)` — filter by the
  `importance: 1..5` frontmatter scalar
- `memory_todos(project, filter?)` — extract `- [ ]` checkboxes
  across notes, with `only_open` / `plan_status` / `path_prefix`

## Read

- `memory_search(query, projects?=[...], include_outline?,
  include_frontmatter?)` — FTS5 with optional enrichment and
  cross-project scope
- `memory_get(path, raw?, max_bytes?)`, `memory_get_section(path, heading)`,
  `memory_get_frontmatter(path)`, `memory_get_outline(path)` — full
  body vs cheap triage variants. `memory_get` has an **oversize
  guard**: a body over 24 KiB comes back truncated (frontmatter +
  outline + first chunk, `truncated:true`, full `size`, a hint) so an
  append-only log can't flood the caller's context; `raw:true`
  bypasses it, `max_bytes` caps even below the threshold. The `etag`
  always stamps the full note, so `if_match` works unchanged
- `memory_batch_get(paths, mode?, max_bytes_per_note?)` — one
  round-trip for multiple notes; `mode=outline|frontmatter` skips
  bodies entirely, `max_bytes_per_note` truncates long ones (flagged
  `truncated:true`), and every entry carries its `etag`
- `memory_list_notes(project)`, `memory_list_projects()`,
  `memory_list_tags(project?)`
- `memory_notes_by_tag(tag, project?)`
- `memory_backlinks(path)`, `memory_outlinks(path,
  include_cross_project?)`

## Graph (read-only, scope-aware)

- `memory_hubs(project?, limit?)` — most-connected notes ("god nodes")
  ranked by undirected wikilink degree, descending; the inverse signal
  of orphan notes. `project` scopes to one top-level folder (degree
  then counts only intra-project links); empty = vault-wide. `limit`
  defaults to 20, max 100. Scoped tokens are forced to their project.
- `memory_path(from, to, max_depth?)` — shortest path between two notes
  over the undirected wikilink graph (resolved links only); returns the
  ordered note paths inclusive of both endpoints, or `found:false` when
  unconnected. `max_depth` defaults to 6, max 20. Both endpoints must
  be inside the token's scope.

## Write (all support optional `if_match` ETag)

- `memory_create(path, content)`
- `memory_update(path, content)` — full overwrite
- `memory_append(path, content)`
- `memory_edit(path, old_string, new_string, replace_all?)` — surgical
  in-place edit
- `memory_delete(path)`
- `memory_rename_note(from, to)`, `memory_move_note(from, to_project)`
- `memory_ask(project, question, urgency?, context?)` — append a
  structured `### OQ-NNN` block to `<project>/docs/open-questions.md`
  without formatting it by hand

## Admin

- `memory_create_project(name)`
- `memory_delete_project(name)`
- `memory_rename_project(from, to)`
- `memory_global_check(project)` — owner-only: report which
  `global-private` notes a project references, for private→public
  promotion (see [Global projects](../vault/global-projects.md))

## Attachments

- `memory_ingest(project, bridge_filename|source_path|url|attachment|data, transfer?, as?, note_path?, title?, caption?, overwrite?, if_match?)` —
  the single front door for "store this file": routes by extension —
  `.csv` → table note, image → media note, `.md`/`.html` → the note itself
  (body read server-side, no tokens through the context), anything else →
  plain attachment. `as` forces a kind; in auto mode a table/media route
  whose vault flag is off degrades to a plain attachment with a warning.
  `url` makes the server fetch the file itself (gated by the
  `ingest_url_allowlist` prefix allowlist, `GOSIDIAN_INGEST_URL_ALLOWLIST`;
  the allowlist also gates every redirect hop). `transfer: "http"` mints a
  **single-use upload ticket** instead: POST the bytes (multipart, field
  `file`, no bearer — the ticket is the credential, TTL 5 min) to the
  returned `/ingest/<ticket>` endpoint and the server executes the parked
  intent on receipt. The dedicated tools below remain for explicit workflows
- `memory_upload_attachment(project, data|source_path, filename)` —
  single-step upload returning a ready-to-splice markdown embed
- `memory_upload_resource(project, data|source_path, filename, kind?)` —
  pre-uploader for the "stage, then attach" pattern; returns the
  resource handle without an embed
- `memory_list_attachments(project?)`
- `memory_delete_attachment(path)`
- `memory_attachment_info(path)`
- `memory_create_media_note(project, data|source_path, filename, caption?, title?, path?)` —
  image media note (ADR-013): uploads the image **and** creates the `.md`
  with `type: image` + `media:` + the caption, atomically. Requires
  `media_notes` enabled; images only
- `memory_create_table_note(project, attachment|data|source_path|bridge_filename, caption?, title?, path?)` —
  CSV table note (ADR-016): uploads (or references) the CSV **and** creates
  the `.md` with `type: table` + `media:` + the caption, atomically; column
  headers + row count are inlined into the body for search. Requires
  `table_notes` enabled; `.csv` only

The single-step / pre-uploader split, the equivalent REST endpoint
`POST /api/upload`, and the full error catalogue live in
[Upload flow](upload.md). The read-side twin — `GET /mcp/download?path=`
serving a note's raw bytes with the same bearer token, so a large note
reaches the agent's disk without crossing the model context — is
documented there too, as is `POST /mcp/append?path=`, the append-only
write for scripts that hold a bearer but no MCP session (the Claude Code
hooks in `contrib/claude-code/`); it runs the same pipeline as
`memory_append`.

## Orchestration (agent-to-agent handoffs + change feed)

The handoff lifecycle is `pending → claimed → done | rejected`;
`created_by` / `claimed_by` / `completed_by` are stamped server-side
from the caller's token identity and cannot be forged, while
`from_agent` / `to_agent` stay declarative role slugs. See
[Agent patterns](patterns.md#agent-to-agent-handoff) for the flow.

- `memory_create_handoff(project, from_agent, to_agent, summary,
  pending_items?)` — create a `status:pending` handoff note under
  `<project>/handoffs/`
- `memory_pending_handoffs(project, for_agent?, status?)` — list
  handoffs by lifecycle status (`pending` default, or
  `claimed`/`done`/`rejected`/`all` to monitor work in flight)
- `memory_claim_handoff(path)` — atomically flip `pending → claimed`
  under a per-note lock: when several agents race for the same
  handoff exactly one wins, the others get an "already claimed" error
- `memory_complete_handoff(path, outcome, note?)` — close a claimed
  handoff as `done` or `rejected` (claimer or admin token only),
  optionally appending an `## Outcome` section
- `memory_wait_changes(project?, cursor?, timeout_s?)` — long-poll for
  note changes inside the token's scope instead of polling in a loop:
  blocks up to `timeout_s` (max 55s), returns as soon as something
  changes, and resumes gap-free from the returned `cursor`.
  `resync:true` means the cursor fell out of the short replay window —
  reconcile with `memory_recent`

## Workflow

- `memory_compact(path, keep_last_n, archive_summary, dry_run?)` —
  shrink log-shaped notes safely
- `memory_self_stats()` — token identity (including the multi-project
  scope list) + rate-limit snapshot (for auto-throttling)
- `memory_project_scaffold(project, template?, variables?)` — idempotent
  Karpathy-Wiki-Stack bootstrap
- `memory_init_agent(project, existing_content?)` — produce the
  init-prompt payload to (re)generate the thin agent `gosidian_block`
  stub in the instruction file (CLAUDE.md / AGENTS.md / …). Augment mode
  when `existing_content` is supplied (merge preserving sections),
  from-scratch otherwise. Read-only: the agent materialises the file.
- `memory_refresh_hot(project)` — regenerate the "Recent decisions"
  section of `hot.md` between opt-in markers
- `memory_list_bootstrap_templates()` — discover available templates
  + their intended-use prompts

## Self-check

- `memory_lint(project, rules?, min_severity?)` — structural vault
  hygiene: `broken-wikilink`, `orphan-note`, `frontmatter-missing`,
  `frontmatter-tag-unknown`, `status-incoherent`, `hot-oversize`
  (a `hot.md` past 16 KiB dominates every bootstrap payload —
  threshold configurable via `[lint] hot_oversize_bytes`). Zero
  `severity:error` on a coherent vault.
- `memory_self_improve(category, title, friction, confidence, …)` —
  *experimental, opt-in, off by default*: record a structured insight
  about gosidian's own ergonomics (see
  [Self-improvement loop](self-improvement.md))

## Audit

- `memory_audit_tail(filters?)` — stream the audit log
- `memory_pending_handoffs(project, for_agent?, status?)` — see
  Orchestration

## Invariants

- **ETag optimistic locking** is uniform across every write tool: pass
  `if_match=<etag>` from the matching `memory_get*` to refuse a
  concurrent modification. The whole load → check → write sequence
  runs under a per-note lock, so `if_match` is a true
  compare-and-swap: among N concurrent writers carrying the same etag
  exactly one wins.
- **Scoped tokens** restrict every call transparently: a token scoped
  to one or more projects (`--project foo` or `--project foo,bar`)
  can only read/write inside them, and `memory_search` with
  `projects=[...]` silently intersects with the scope. Multi-project
  tokens must name the project explicitly where a single-project
  token would be defaulted — an omitted argument never widens a
  query.
- **Cross-project** is opt-in — `memory_search projects=["a","b"]`
  and `memory_outlinks include_cross_project=true` — so default
  behaviour stays tightly scoped.
