# Changelog

All notable changes to gosidian are documented here. The format loosely
follows [Keep a Changelog](https://keepachangelog.com/); dates are
`YYYY-MM-DD`; versions follow [SemVer](https://semver.org/).

This file is the single source for per-release notes — each GitHub Release
pulls its body from the matching section below. There are no separate
`RELEASE_NOTES_*` files.

## [2.23.0] — 2026-07-29 — "per-project tag vocabulary"

### Added
- **Per-project lint tag vocabulary.** A project can extend the closed tag
  vocabulary checked by `memory_lint` by declaring `tag_vocabulary:` in the
  frontmatter of its `memory/conventions.md` — exact tags (`anagrafica`) or
  namespace wildcards (`cm:*`), capped at 64 entries. The declaration is
  gated by the new per-project `use_tag_vocabulary` flag (toggle in the
  Projects view, `use_tag_vocabulary` on `PUT /api/v1/projects/{name}`,
  persisted in `.gosidian/projects.json`); off by default, the declaration
  is inert. Agents maintain the vocabulary with regular note edits — no new
  tool, auditable through the vault's git history.
- `memory_bootstrap` surfaces the effective declaration in a
  `tag_vocabulary` block (`{enabled, note, extra}`), so a project's
  extended vocabulary stays visible at session start instead of acting
  silently. Operational directives bumped to **v10** documenting the
  mechanism ("for living domain taxonomies, not for dodging a typo").

### Changed
- The `topic:` namespace is now **open**: any well-formed `topic:<area>`
  value (non-empty, no whitespace, no extra colon) passes the
  `frontmatter-tag-unknown` rule, aligning the linter with the directives
  contract. `type:` and `status:` stay closed — they are lifecycle
  vocabularies the tooling depends on.
- The instance-wide `[lint.frontmatter_tag_vocabulary] extra_allowed`
  config now accepts `ns:*` namespace wildcards too, and the rule's fix
  hint teaches the per-project mechanism.
- Dependency maintenance: `mark3labs/mcp-go` 0.56.0→0.57.0,
  `prometheus/client_golang` 1.23.2→1.24.1.

### Security
- `dompurify` 3.4.11→3.4.12 (clears the open npm advisory, low severity).
- npm override `brace-expansion ^5.0.8` clears GHSA-mh99-v99m-4gvg across
  the dev-only eslint chain without the breaking `eslint@10` upgrade —
  `npm audit` now reports 0 vulnerabilities including devDependencies.

### Notes
- No migration needed: the feature is opt-in per project; without the
  `use_tag_vocabulary` flag lint behaviour is unchanged (except the now
  open `topic:` namespace, which only removes warnings).

## [2.22.0] — 2026-07-21 — "graph filters in URL + semantic 3D z"

### Added
- **Graph filters survive reloads**: the graph window URL token grows a
  compact param form (project/tag/focus/depth/min-degree/limit) next to
  the legacy bare ego-focus path, and the view pushes filter changes
  back into the window so the URL always reflects what you see. Window
  titles follow the project filter (`Graph · <project>`). Existing ego
  ("direct links") URLs are unchanged.
- **3D z-axis modes**: in 3D, a `Z: free | groups | recency` selector
  pins the third dimension to one plane per color group or to note
  recency (newer floats up), or leaves it to the simulation (default).
  Powered by a new additive `mtime` field on `/api/v1/graph` nodes.
- **2D level-of-detail for dense graphs**: weight-1 links outside the
  hovered neighbourhood hide below 0.5x zoom, and high-degree nodes get
  an earlier label fade-in so hubs are readable at mid zoom.

### Notes
- `mtime` is additive (omitted on synthesized cross-project endpoints);
  no API breaking changes and no migration.
- A WebGPU renderer spike was evaluated and closed as no-go for now:
  the `three.webgpu` build alone is ~1.8x the classic three size and
  `3d-force-graph` pins the classic WebGLRenderer, for no measurable
  gain at typical vault scale.

## [2.21.1] — 2026-07-21 — "dev lockfile hygiene"

### Security
- Dev-toolchain lockfile bumps flagged by Dependabot right after the
  2.21.0 scan: `js-yaml` 4.2.0→4.3.0 (GHSA-52cp-r559-cp3m, quadratic
  CPU via YAML merge-key chains) and the remaining `brace-expansion`
  instances. `npm audit` is back to zero vulnerabilities.

### Notes
- Lockfile-only: no runtime dependency or code changes, no migration.
  The published image is identical in behavior to 2.21.0.

## [2.21.0] — 2026-07-21 — "graph 2D/3D"

### Added
- **Optional 3D graph mode**: every graph window now has a `2D | 3D`
  toggle. The 3D renderer (three.js WebGL + d3-force-3d) mirrors the 2D
  feature set — click-to-open, hover lights up the 1-hop neighbourhood,
  node size by degree and colors by group — with an orbit camera and
  tooltip titles. 2D stays the default; the choice persists per browser
  profile. The three.js runtime ships as a separate lazy chunk loaded
  only on the first switch to 3D.

### Changed
- **2D graph rebuilt on a live force simulation** (`force-graph`,
  replacing Cytoscape + fcose static layouts): nodes are draggable and
  collision-separated, labels fade in with zoom instead of stacking into
  a wall of text, node colors group by project (or by top-level folder
  inside a single project), and refetches reuse previous positions so
  tweaking a filter no longer re-scrambles the layout.
- Dependency maintenance: `axios` 1.16→1.18.1, `go-ldap` 3.4.13→3.4.14,
  `modernc.org/sqlite` 1.53→1.54 (pulls `modernc.org/libc` 1.74.1).

### Security
- `axios` 1.18.1 resolves one high and four medium npm advisories
  (Node HTTP adapter proxy inheritance, form serializer `maxDepth`
  bypass, HTTP/2 `maxBodyLength` bypass, `NO_PROXY` 0.0.0.0 bypass,
  prototype-pollution auth injection).
- The api/v1 slow-handler log now also strips newlines from the escaped
  request path — redundant at runtime on top of `EscapedPath()`, but it
  is the sanitizer CodeQL's `go/log-injection` taint model recognizes,
  closing the remaining code-scanning alert.

### Removed
- `cytoscape` and `cytoscape-fcose` (superseded by `force-graph`).

### Notes
- No migration. Open Dependabot PRs for axios/go-ldap/sqlite auto-close
  on the next dependency rescan.

## [2.20.1] — 2026-07-15 — "hygiene"

### Security
- **Log injection hardening** (CodeQL `go/log-injection`): the api/v1
  slow-handler warning now logs `r.URL.EscapedPath()` instead of the
  decoded path — a client-chosen `%0A` stays percent-encoded instead of
  forging log lines in the text log format. The JSON production format
  was already escaping; this closes the text-format gap flagged by code
  scanning.

### Changed
- Dependency maintenance: `mark3labs/mcp-go` 0.55→0.56 (avoids full
  tool-filter scans on call hot paths — the API behind the core/full
  token profiles), `yuin/goldmark` 1.7.4→1.8.4, `golang.org/x/crypto`
  0.53→0.54, `golang.org/x/term` 0.44→0.45, `boombuler/barcode`
  →1.1.0, plus transitive `x/sys` 0.47 and `x/text` 0.40.

### Notes
- No functional changes and no migration. This release supersedes the
  open Dependabot PRs, which auto-close on the next dependency rescan.

## [2.20.0] — 2026-07-13 — "maintenance digest"

### Added
- **Maintenance digest at bootstrap**: `memory_bootstrap` now serves a
  per-project `maintenance` block — `hot_size` / `hot_oversize` (vs the
  lint hot-oversize threshold) / `hot_age_days`, `log_size`,
  `broken_links` (unresolved wikilinks leaving the project's notes) and
  `stale_count` (notes untouched for 90+ days, excluding
  `status:done` / `status:archived`), plus an `attention` flag that
  fires on the two failure modes that actually happen in the wild: a
  hot file growing unbounded and broken wikilinks accumulating
  silently. Numbers only, computed live from indexed queries and two
  file stats — never a content scan; `memory_lint` stays on-demand and
  the digest tells the agent *when* to run it. Best-effort: a lookup
  error degrades to an absent block, never a failed bootstrap.
- Operational directives bumped to **v9**: the end-of-task workflow
  gains step 0 — when the bootstrap served `maintenance.attention:
  true`, propose the relevant grooming (compact the hot file, repair
  the reported links) before closing. `stale_count` is context, not an
  obligation.

### Fixed
- **Attachment embeds are no longer "broken wikilinks"**: the
  broken-wikilink lint rule now resolves `![[<file>.<ext>]]` targets
  through the same attachment resolution the renderer uses, so working
  image embeds stop being flagged (a real vault reported ~138 phantom
  entries); a genuinely missing embed is still reported, with
  attachment-specific wording. The maintenance digest's `broken_links`
  counter excludes attachment-extension targets for the same reason.

### Notes
- No migration required and nothing to configure: the digest reuses the
  existing lint hot-oversize threshold (default 16 KiB,
  `[lint] hot_oversize_bytes` to override); the 90-day stale cutoff is
  fixed until real-world soak says otherwise.

## [2.19.0] — 2026-07-12 — "one-door ingestion"

### Added
- **`memory_ingest` — the single front door for saving files** (57th MCP
  tool). One call routes any file by extension: `.csv` → table note,
  image → media note, `.md`/`.html` → the note itself (body read
  server-side, no tokens through the model context), anything else →
  plain attachment. Sources, cheapest first: `bridge_filename` (staged in
  the bridge dir), `source_path` (inside an allowed upload root), `url`,
  an already-uploaded `attachment`, or base64 `data` as the last resort.
  Force a kind with `as`; note ingestion supports `overwrite:true` with
  `if_match` CAS. In auto mode a table/media route whose vault flag is
  off degrades to a plain attachment with a teaching warning.
- **Single-use upload tickets** (`transfer: "http"`): the tool response
  carries a one-time upload URL (TTL 5 min, no bearer needed — the
  unguessable ticket is the credential, bound to the minting token so
  scope, audit and limits apply unchanged). POST the file there
  (multipart, field `file`) and the server executes the parked intent
  and answers like the tool would. Any redemption attempt consumes the
  ticket. Remote agents save a file with one tool call plus one `curl`.
- **Fetch-by-URL ingestion**: `memory_ingest` can download the file
  itself (CI artifacts, internal services). Gated by the new
  `mcp.ingest_url_allowlist` / `GOSIDIAN_INGEST_URL_ALLOWLIST` prefix
  allowlist — the SSRF boundary: it gates the initial URL and every
  redirect hop, empty means disabled. 10 MiB cap, 20 s timeout.
- Bootstrap `capabilities.attachments` now surfaces `bridge_dir`,
  `allowed_upload_roots` and `ingest_url_enabled`, so a co-located agent
  learns the cheap staging path up front instead of after a wasteful
  base64 upload.

### Changed
- **`core` tool profile**: `memory_ingest` replaces the legacy upload
  matrix (`memory_upload_attachment`, `memory_upload_resource` and the
  dynamic media/table-creator admissions) — workers see one file door;
  the dedicated tools remain in the `full` profile for explicit
  stage-then-attach workflows.
- **Errors now teach the way out**: the `source_path` rejection explains
  the allowed-roots model and the full channel hierarchy (a rejection
  never meant "filesystem not shared"); the no-source error mentions the
  HTTP and ticket channels; "… notes are disabled" points at the live
  per-project toggle UI; the note-size cap error routes to table/media
  notes and `memory_ingest`. `memory_create`/`update`/`append` declare
  the 1 MiB cap in their descriptions.
- Operational directives bumped to **v8**: file-saving guidance now
  leads with `memory_ingest` (sources cheapest-first, bridge dir from
  capabilities, url + ticket channels).
- Docs: `docs/mcp/upload.md` rewritten around the one-line decision tree
  ("to save a file, call `memory_ingest`"), with the ticket flow and
  redemption status codes documented.

### Notes
- No migration required. Existing tokens keep their profile; `full`
  tokens see the legacy tools unchanged. The `url` source ships disabled
  until an allowlist is configured.

## [2.18.0] — 2026-07-07 — "Anchors round 2: harness overrides + bootstrap delta"

MINOR release rounding off the agent-anchors feature (v2.13.0) with the
four sharp edges found while adopting it at scale. All changes are additive
and backward compatible: existing anchors keep their `meta_version`
byte-for-byte (guarded by a pinned golden test), so **no anchor file is
rewritten** after upgrading. No migration required.

### Added
- **`harness.tools: all`** on a `type:agent` note renders its anchor
  *without* a `tools:` line, which the CLI interprets as "inherit the full
  default toolset" — no more enumerating (and drifting) 15+ tool names per
  role. The explicit list form and the least-privilege default preset are
  unchanged.
- **`harness.materialize: false`** keeps a role vault-only: the note stays
  canonical and listable (e.g. an orchestrator that must not be spawnable
  as a subagent) but is excluded from the bootstrap anchors set; an
  already-materialised anchor becomes an orphan and the normal reconcile
  flow removes it.
- **`known_anchor_metas` on `memory_bootstrap`.** Repeat bootstraps of an
  anchor-enabled project no longer pay the full anchors block: pass the
  `canonical → meta_version` map from the previous round and matching items
  come back as `{path, canonical, meta_version, unchanged: true}` with no
  content — the anchors counterpart of `known_etags`. Operational
  directives bumped to **v7** to document it.
- **`adopt_into_existing` on `memory_promote_agent`.** Promoting a foreign
  local agent file whose canonical `type:agent` note already exists (a role
  created before anchors) no longer dead-ends: the existing body is never
  touched, a `harness:` block is inserted only if missing, and the foreign
  body comes back in `foreign_body_for_review` with a fold-check
  instruction. Without the flag, the exists-error now explains exactly
  this.

### Changed
- The anchors reconcile directive covers the two new cases: `unchanged`
  items ("leave the file as is; re-bootstrap without the param if it went
  missing") and foreign files whose canonical already exists (suggesting
  `adopt_into_existing`).
- `docs/mcp/agent-anchors.md` documents the `harness:` frontmatter block,
  the bootstrap delta and the adopt flow.

## [2.17.1] — 2026-07-06 — "Fragment wikilinks join the graph"

PATCH release. One index-level fix with a visible payoff on any vault that
uses `[[note#heading]]` cross-references. No migration required — the
startup rescan re-resolves existing links automatically.

### Fixed
- **`[[note#heading]]` wikilinks now resolve in the index.** The link
  resolver never stripped the `#fragment`, so every fragment link stayed
  unresolved: invisible to **backlinks, graph, hubs and path** queries, and
  flagged as a broken wikilink by `memory_lint` (in the reference vault,
  230 of 249 "broken" warnings were this false positive). The web UI
  renderer was unaffected — it strips the fragment independently — which is
  why the links worked in preview and the bug stayed latent. Fragments are
  presentation-level (Obsidian semantics): the path part resolves to the
  note, and a pure `[[#heading]]` self-link records no cross-note edge.
  After upgrading, the first startup scan repairs the index and the graph
  gains the previously-missing edges.

## [2.17.0] — 2026-07-06 — "Token economy: read guard + tool profiles"

MINOR release focused on the token cost of agent sessions: an oversize guard
on the most-called read tool, an opt-in worker tool profile that cuts the
per-session schema surface by ~60%, slimmer tool descriptions and a
lighter bootstrap. All changes are additive and backward compatible; every
existing token and client keeps its current behaviour.

### Added
- **Per-token tool profiles.** A token created with `--tool-profile core`
  (CLI) or `tool_profile: "core"` (`POST /api/v1/admin/tokens`) sees only
  the worker subset of the MCP catalogue — session start, note CRUD,
  targeted reads, search/list basics, both upload tools, the full handoff
  lifecycle and `memory_wait_changes` (~21 tools instead of 56, cutting a
  sub-agent's per-session schema cost by ~60%). The media/table note
  creators are admitted dynamically only when their vault flag is on, and
  `memory_self_improve` only for opted-in tokens. The profile is an
  **access-control boundary**: a tool outside it is absent from
  `tools/list` *and* answers `tool not found` when called by name.
  Default is `full` — existing tokens are untouched. Profile shown in
  `gosidian token list` and `memory_self_stats`.
- **`memory_get` oversize guard.** A note body over 24 KiB now comes back
  truncated — frontmatter + heading outline (capped at 80 headings:
  first 20 + the most recent) + the first 4 KiB chunk, with
  `truncated: true`, the full `size` and a hint pointing at
  `memory_get_section`. `raw: true` bypasses the guard; `max_bytes` caps
  explicitly even below the threshold. The `etag` always stamps the full
  note, so `if_match` optimistic locking is unchanged. Rationale: an
  append-only log can reach hundreds of KiB — one unguarded read of a
  431 KiB file used to cost ~110k tokens; it now returns ~12 KiB.

### Changed
- **Bootstrap `mode` defaults to auto**: an oversize `hot.md` is served in
  lite shape (frontmatter + outline, flagged `auto_lite: true`); explicit
  `full`/`lite` behave as before. The `anchors` block no longer ships its
  reconcile directive when the desired set is empty.
- **Operational directives v6**: full prose tightening (−24%, same rules);
  the token-economy section documents the read guard and auto-lite.
- **Slimmer tool descriptions** on the six heaviest schemas (bootstrap,
  media/table note creators, init_agent, both upload tools) — 40-50%
  shorter, with details moved to the docs.

### Notes
- No migration required. `tool_profile` is empty (= `full`) on every
  existing token; assign `core` deliberately to sub-agent tokens.
- Agents that read large notes through `memory_get` should follow the
  truncated response's hint (`memory_get_section`, or `raw: true` when the
  whole body is really needed).

## [2.16.0] — 2026-07-06 — "CSV table notes + capability discovery"

MINOR release. Two discoverability-first features: long tabular data (audit
reports, exports) becomes a first-class **CSV table note** rendered as a
paginated table in the web UI, and agents now learn the instance's content
formats at session start from a new **`capabilities` block** served by
`memory_bootstrap`. Everything stays a plain markdown note; all changes are
additive and backward compatible, new features are off by default with no
migration required. 56 MCP tools total.

### Added
- **CSV table notes** (ADR-016, opt-in via `[vault] table_notes` /
  `GOSIDIAN_VAULT_TABLE_NOTES`, default off). A table note is a normal `.md`
  whose frontmatter declares `type: table` + a `media:` pointer to a `.csv`
  attachment — it participates in tags, links, backlinks, graph and full-text
  search like any note, and the SPA renders the CSV as a **paginated table**
  (500 rows/page, text-only cells, download link, comma/semicolon/tab
  delimiter auto-detection). Column headers and the row count are inlined
  into the body so they land in search; cell values are intentionally not
  indexed — the caption is the retrievable text. A broken pointer degrades to
  a placeholder, never an error.
- **`memory_create_table_note`** MCP tool (56th): uploads (or references) the
  CSV and creates the pointing note in one atomic call; rejects unparsable
  CSV before writing the note; accepts `attachment` (already-uploaded path,
  cheapest), `bridge_filename`, `source_path` or base64 `data`.
- **`capabilities` block in `memory_bootstrap`**: reports whether
  `html_notes`, `media_notes` and `table_notes` are enabled on this instance,
  plus the attachment surface (size cap, allowed extensions, the HTTP
  `/upload` endpoint hint and the upload tool names) — so agents discover
  content formats at session start instead of inside individual tool schemas.
- **Per-project flag toggles in the projects admin UI**: `use_anchors` and
  `use_globals` can now be flipped from the web UI (owner/admin only)
  instead of editing `.gosidian/projects.json`.

### Changed
- **Operational directives v5** (served by `memory_bootstrap`): new «note
  formats & attachments» section — markdown stays the declared default,
  `.html` is reserved for intrinsically-HTML content, binaries go through
  the upload tools (never base64 for large files), long tabular data goes to
  a linked table note; the ingest table routes each artifact type
  accordingly, cross-referencing the live `capabilities` block.
- `memory_create` now documents native `.html` notes in its description
  (gated by `html_notes`); `memory_bootstrap`'s description documents the
  `capabilities` block.
- Lint frontmatter vocabulary gains `type:table`.

### Fixed
- Trashing a project now collects notes of **every** recognised extension
  for index cleanup — previously `.html` notes in a trashed project left
  stale entries in search/graph/backlinks.
- The markdown-preview wikilink resolver now resolves extension-less links
  against every note extension, so `[[links]]` to `.html` notes render as
  live links instead of dangling ones.

### Notes
- `table_notes` is **off by default**; enabling it requires no migration.
  With the flag off, a `.md` carrying `type: table` renders as ordinary
  markdown and API responses carry no `kind`/`media` overlay.
- Clients connected via MCP must **reconnect** to see the new
  `memory_create_table_note` tool (tool lists are snapshotted at session
  start).

## [2.15.0] — 2026-07-06 — "Agent orchestration bus"

MINOR release. gosidian can now serve as the coordination bus between an
orchestrator agent and its sub-agents: handoffs gained a real lifecycle with an
atomic claim, tokens can span multiple projects, and a long-poll change feed
replaces poll loops. Everything stays a plain markdown note — no queues, no
message tables — and every change is additive and backward compatible.
55 MCP tools total.

### Added
- **Handoff lifecycle** (`pending → claimed → done | rejected`).
  `memory_claim_handoff` atomically takes a pending handoff in charge under a
  per-note lock — when several agents race for the same handoff exactly one
  wins, the others get an "already claimed by …" error, so work is never done
  twice. `memory_complete_handoff` closes it as `done` or `rejected` (claiming
  token or an admin only) with an optional `## Outcome` section.
  `memory_pending_handoffs` gains a `status` filter (`pending` default,
  `claimed`/`done`/`rejected`/`all`), an optional `for_agent`, and exposes the
  lifecycle fields per entry.
- **Server-stamped identity on handoffs.** `created_by`, `claimed_by` and
  `completed_by` carry the calling token's identity (`name@id`), stamped
  server-side — they are not tool parameters and cannot be forged.
  `from_agent`/`to_agent` stay declarative role slugs: declaration and identity
  live side by side.
- **Multi-project tokens.** `gosidian token create --project a,b` scopes one
  token to several projects — the natural shape for an orchestrator
  coordinating N agent projects without an over-privileged admin token. The
  REST API accepts `projects: [...]` on `POST /api/v1/admin/tokens`;
  `memory_self_stats` reports the list. Where a single-project token is
  silently defaulted, a multi-project token must name the project explicitly —
  an omitted argument never widens a query.
- **`memory_wait_changes` long-poll change feed.** Park one call (up to 55s)
  and wake as soon as a note changes inside the token's scope, instead of
  burning tokens polling `memory_recent`/`memory_pending_handoffs`. A short
  replay ring bridges the gap between two consecutive calls via the returned
  `cursor`; `resync: true` signals the cursor fell out of the window (reconcile
  with `memory_recent`). One wait per MCP session.
- **Slim repeat bootstraps.** `memory_bootstrap` accepts
  `known_directives_version` (matching version omits the directives block),
  `known_etags` (unchanged hot/README/instruction files come back
  `unchanged: true` with no body) and `mode: "lite"` (hot.md body replaced by
  frontmatter + heading outline). A respawned sub-agent pays a few hundred
  bytes instead of the full payload when nothing changed.
- **Bounded bulk reads.** `memory_batch_get` gains `mode=outline|frontmatter`
  (skip bodies entirely) and `max_bytes_per_note` (truncate long ones, flagged
  `truncated: true`); every entry now carries its `etag`.
- **`hot-oversize` lint rule** (default on, warning): a `hot.md` past 16 KiB is
  inlined into every bootstrap payload and dominates session-start cost.
  Threshold configurable via `[lint] hot_oversize_bytes`.
- **Agent anchors documentation.** The v2.13.0 anchors feature gets a real page
  ([docs/mcp/agent-anchors.md](docs/mcp/agent-anchors.md)): the model, the
  three gating switches, the reconcile flow and `memory_promote_agent` — it was
  previously only mentioned in this changelog.

### Fixed
- **`if_match` is now a true compare-and-swap.** The load → check → write
  sequence in every MCP and HTTP note handler runs under a per-note lock; two
  concurrent writers passing the etag check in the same window can no longer
  clobber each other (TOCTOU). Also serializes concurrent `memory_ask` calls
  (no duplicate OQ ids) and same-second handoff creations (suffix probing
  instead of overwriting).
- **Project-scoped tokens can create handoffs.** `memory_create_handoff`
  authorized against an empty path, which always rejected scoped tokens — only
  admin tokens could ever hand off. Latent since the tool shipped.
- **`memory_append` and the handoff tools now publish change events** on the
  internal hub (note/tree topics), so SPA subscribers and `memory_wait_changes`
  waiters wake on them like they already did for create/update/delete.

### Changed
- Operational directives bumped to **v3**: handoff vocabulary and the
  claim-before-work rule, plus the token-economy guidance (slim bootstraps,
  bounded bulk reads, hot-oversize grooming). Served fresh by
  `memory_bootstrap` — projects pick it up automatically.
- Docs refreshed throughout: orchestration tool group, handoff + change-feed
  patterns, multi-project token semantics, configuration reference
  (`GOSIDIAN_ANCHORS_ENABLED`, `lint.hot_oversize_bytes`).

### Notes
- Fully backward compatible: existing `tokens.json` files load unchanged
  (legacy single-project records keep working; new multi-project tokens store
  the first project in the legacy field, so older binaries see a narrower —
  never wider — scope). Single-project tokens behave exactly as before.
- After upgrading, MCP clients connected during the restart should reconnect
  to refresh their tool catalog (3 new tools).

## [2.14.0] — 2026-07-01 — "Plancia view mode: strip ↔ tabs"

MINOR release. The window manager (plancia) can now switch, at runtime, between
the niri-style horizontal **strip** and a **tabs** layout: a horizontally
scrollable tab bar — each tab showing the window title and a direct close
button — over a single full-width pane that mounts only the focused window (kept
alive), so many open windows stay cheap. The toggle lives in the top bar and the
choice is remembered across reloads. Strip mode is unchanged and stays the
default, so existing sessions are unaffected until a user opts in.

### Added
- **Tabs view mode.** A segmented toggle in the top bar switches the plancia
  between `strip` (the tiling strip) and `tabs` (a scrollable tab bar over one
  focused pane). In tabs mode each tab shows its window's title and a close
  button that closes the window directly — no need to minimise it first — and the
  tab bar scrolls horizontally when there are many windows, keeping the active
  tab in view. The chosen mode is persisted (`localStorage` `gosidian.ui`).
- New `plancia.viewStrip` / `plancia.viewTabs` / `plancia.viewToggle` UI strings
  across all five locale catalogs.

### Changed
- **plancia dependency bumped to `0.3.0`**, which adds the `v-model:view-mode`
  layout, a scrollable closable tab bar, and `KeepAlive`-bounded mounting for the
  tabs pane (only the focused window is mounted). In tabs mode, hosting many
  windows goes from O(N) mounted subtrees to O(1) — the key lever for large
  workspaces.

### Notes
- Additive and backward-compatible: the default view mode is `strip`, so the UI
  looks and behaves exactly as before until a user switches to tabs. No migration.

## [2.13.0] — 2026-06-30 — "Agent anchors: referenced agents from the vault"

MINOR release. A new opt-in subsystem lets your vault `type:agent` notes become
spawnable CLI subagents without copying them: at session bootstrap gosidian
returns a set of thin local "anchors" (e.g. `.claude/agents/<slug>.md`) whose
body simply pulls the canonical role from the vault at spawn time via
`memory_get`. One source of truth, no duplication. Fully additive and
**off by default** — with the master switch unset, bootstrap behaves exactly as
before, so existing deployments need no migration.

### Added
- **Agent anchors (referenced agents).** `memory_bootstrap` accepts an optional
  `profile` (default `claude`) and, when enabled, returns an `anchors` block: the
  desired set of local anchor files (each with its content, a `meta_version`
  fingerprint, and the canonical vault path it pulls from) plus a `reconcile`
  directive. The server never writes outside the vault — it computes the desired
  set; the agent materialises and reconciles the files in its own working
  directory using each file's `gosidian:anchor` marker. `memory_init_agent` gains
  the same `anchors` output.
- **`memory_promote_agent` tool.** Adopts a foreign, hand-written local agent
  file (one without a `gosidian:anchor` marker) into a canonical vault
  `type:agent` note — preserving its body and capturing its name/tools into a
  `harness:` frontmatter block — and returns the anchor to replace it with. A
  one-shot migration path for pre-existing subagent files.
- **Per-project `use_anchors` flag** (off by default) and the
  **`GOSIDIAN_ANCHORS_ENABLED`** master switch (off by default): two-axis opt-in
  gating that mirrors the shared global-projects feature.

### Notes
- Off by default on every axis (master switch AND per-project `use_anchors` AND
  profile support). With the master switch unset the bootstrap payload is
  unchanged — no migration, no behaviour change.
- Anchors are a local projection of the vault (the single source of truth); treat
  them as generated files (e.g. gitignore `.claude/agents/`).

## [2.12.0] — 2026-06-25 — "Auth/admin hardening + plancia window-manager library"

MINOR release. Three authentication and administration features — TOTP QR
enrolment with server-side enforcement, direct user creation from the web UI,
and per-project sharing — land alongside an internal refactor: the tiling
window-manager is now the standalone open-source
[`plancia`](https://github.com/daniele-chiappa/plancia) library, consumed from
npm. Additive and non-breaking — the new sharing model is opt-in (default
unchanged) and the global TOTP policy is untouched, so existing deployments
need no migration.

### Added
- **TOTP enrolment QR code + server-side enforcement.** The enrolment screen now
  renders a real scannable QR (inline pure-Go SVG; the CLI prints an ASCII QR)
  instead of a bare secret/URI. When the global TOTP policy is `required`,
  enrolment is now enforced server-side: requests from a non-enrolled account are
  gated (`403 auth.enrollment_required`) and the web UI shows a forced enrolment
  interstitial — even mid-session.
- **Admin: create a user directly from the web UI.** Owners can create a member
  or guest from Admin → Users (`POST /admin/users`) with a client-side password
  generator (Web Crypto); duplicate usernames return `409`. The initial TOTP
  policy for the new user is selectable.
- **Per-project membership ACL (opt-in sharing).** A new per-user, per-project
  membership list (`read` / `write`) lets owners share individual private
  projects without making them public. The role stays the ceiling — a guest never
  writes, even with a `write` membership; membership only narrows scope. Gated by
  a global `member_scope` toggle (`all` | `members`) that **defaults to `all`**,
  so behaviour is unchanged until you opt in. Member management is owner-only.

### Changed
- **The tiling window-manager is now the external `plancia` library.** The
  in-tree window-manager has been extracted into a standalone, zero-runtime-deps
  npm package (`plancia`) and is consumed as a normal dependency — same UI,
  cleaner boundary. `plancia@0.2.0` adds window drag-resize (draggable right edge,
  double-click resets) and percentage width presets.

### Security
- **dompurify 3.4.11** (was 3.4.10) — fixes GHSA-cmwh-pvxp-8882 (permanent
  `ALLOWED_ATTR` pollution via `setConfig()`), on the HTML-note sanitisation
  path.
- Dependency maintenance: `mark3labs/mcp-go` 0.55.0, `modernc.org/sqlite` 1.53.0.

### Notes
- Non-breaking by default: `member_scope` defaults to `all` (no per-project
  gating until you enable it) and the global TOTP mode is unchanged. No data
  migration is required.

## [2.11.0] — 2026-06-17 — "Read-only anonymous access (open mode)"

MINOR release. An opt-in read-only mode lets you serve a gosidian instance to
anonymous visitors — a public showcase or read-only wiki — without giving up
authentication for editing. Off by default; existing deployments are unchanged.

### Added
- **`GOSIDIAN_OPEN_MODE=readonly`.** When set, token-less web requests are
  served as an anonymous guest instead of being rejected: read-only, and limited
  to projects you have marked **public** (private projects stay hidden). The
  existing role model does the enforcing — guests cannot write, admin routes stay
  owner-only, and the MCP API is unaffected (still token-only). The web UI
  detects the mode and renders the read-only guest view with a **Login** button
  (so an owner can still sign in and edit) instead of forcing the login page.
  Live updates (SSE) stay token-only, so anonymous sessions never receive events.
  Anyone who can reach the server can read public projects, so enable it
  deliberately.

### Fixed
- The CLI (`gosidian user disable`) and the internal docs no longer claim a web
  UI "open mode" that the v2 SPA did not actually implement. With no users and
  the default config the UI requires `gosidian user setup` to provision an owner
  (or the new `GOSIDIAN_OPEN_MODE=readonly`).

## [2.10.0] — 2026-06-17 — "Try it in Codespaces + interactive HTML notes"

MINOR release. A one-click GitHub Codespaces demo so anyone can try gosidian in
the browser with zero setup, plus a fix that finally lets HTML notes run their
own inline JavaScript. Additive — existing deployments need no migration.

### Added
- **One-click demo in GitHub Codespaces.** An "Open in GitHub Codespaces" badge
  in the README launches a private, throwaway instance that builds gosidian from
  source, seeds a small English demo vault (backlinks, graph view, full-text
  search, an `.html` note and a media note), and opens the web UI on port 8080.
  A `demo` / `gosidian-demo` account is provisioned automatically so the UI is
  usable immediately. Lives under `.devcontainer/` and is excluded from the
  Docker image build context.

### Fixed
- **HTML-note inline scripts now run under the strict CSP.** HTML notes render
  in a sandboxed `srcdoc` iframe, which inherits the SPA shell's
  `script-src 'self'` — that intersected away the iframe's own `'unsafe-inline'`,
  so a note's inline `<script>` was silently blocked and the "JavaScript runs"
  promise of HTML notes did not hold. The shell now emits a per-request CSP
  nonce (`script-src 'self' 'nonce-…'`) and exposes it via a
  `<meta name="csp-nonce">` tag; the SPA stamps that nonce onto the note's
  `<script>` tags so they execute. Notes still run in an opaque origin
  (`sandbox="allow-scripts"`, no `allow-same-origin`) with the iframe's own
  `default-src 'none'` blocking the network, so the shell's execution surface is
  unchanged — interactivity is restored without weakening isolation.

### Security
- The SPA shell CSP is now per-request: a fresh 128-bit base64url nonce on every
  `index.html` response (already `Cache-Control: no-store`), generated from the
  system CSPRNG and fail-closed on error. The nonce is consumed only by the
  sandboxed HTML-note iframe; the shell itself ships no inline scripts.

## [2.9.1] — 2026-06-16 — "i18n fix for the v2.9.0 UI"

PATCH release. The UI components added in v2.9.0 shipped with hardcoded strings
instead of going through the i18n catalogs, so the note-creation form, the
media-note placeholder, the tree `+` button and the note-header icon buttons
ignored the active locale (an English UI showed Italian, and an Italian
operator saw stray English). No behaviour or API change; no migration.

### Fixed
- **Localized the v2.9.0 UI strings.** `NoteCreateView`, `MediaPreview`, the
  tree folder `+` button and the `NoteView` header icons (Print / Download /
  Copy / History) now read from the i18n catalogs (new `note`, `note_create`,
  `media` namespaces + `tree.new_note_here`) via `t()` instead of hardcoded
  text. Strings ship for `it`/`en`; other locales fall back to `en` through the
  catalog's normal fallback chain.

### Changed
- Refreshed the README demo GIF (now rendered entirely in English) and
  consolidated the recording storyboard into `web/scripts/record-demo.mjs`,
  parametrized via `DEMO_LOCALE` / `DEMO_PRESET`.

## [2.9.0] — 2026-06-16 — "Image media notes, manual creation & cheap asset ingestion"

MINOR release. Images become first-class, addressable notes; the web UI gets
back manual note creation; and getting images into a vault no longer means
pushing base64 through an agent's context. Everything new is off-by-default or
additive — existing deployments need no migration.

Design principle throughout: **the stored note (and what an LLM reads over MCP)
keeps a lightweight image *reference*** — `type: image` + `media:`, or
`![[image]]` — so agents read text, not bytes (token savings). Base64 lives
only in the web-UI presentation layer (HTML render, downloads).

### Added
- **Image media notes (ADR-013).** A markdown note whose frontmatter declares
  `type: image` + `media: <attachment>` is resolved as a first-class image
  note: the body is the searchable caption/transcript, while the read paths
  (REST `GET /notes` and MCP `memory_get`) expose a structured `kind` + `media`
  overlay. The note stays plain `.md`, so tags, links, backlinks, graph and FTS
  all work unchanged. Off by default — enable with `[vault] media_notes`
  (`GOSIDIAN_VAULT_MEDIA_NOTES`). New MCP tool `memory_create_media_note`
  (upload + note in one call), and a dedicated image render in the web UI.
- **Manual note creation in the web UI.** A `+` on each tree folder opens a
  unified creation form — Markdown or Image — restoring manual creation that
  was never ported in the v2.0 SPA migration.
- **Cheap image/asset ingestion** (so illustrated docs are authorable without
  base64-through-context):
  - **`POST /mcp/upload`** — a multipart HTTP upload authenticated by the same
    MCP bearer token as the SSE stream (path mirrors your `/sse` URL with
    `/sse`→`/upload`). Bytes travel over HTTP, never the model context; returns
    `{path, url, mime, kind, size}`.
  - **Bridge directory** — set `[mcp] bridge_dir` (`GOSIDIAN_MCP_BRIDGE_DIR`)
    to a shared staging dir; agents stage a file and pass `bridge_filename`,
    the server reads and consumes it. For co-located deployments.
  - `memory_create_media_note` and the upload tools gain an `attachment` /
    `bridge_filename` path so a previously-uploaded image is referenced, not
    re-sent.
- **Obsidian image embeds.** `![[image]]` (and `![[image|alt]]`) now render as
  inline images in markdown notes, resolving an attachment or a media note's
  image.
- **Images render in HTML notes.** A single-file HTML note that references
  vault images by URL now shows them: the images are fetched and inlined as
  `data:` URIs at render time (cached immutably), within the existing
  sandboxed-iframe CSP — the stored note is untouched.
- **Self-contained download.** `GET /notes/<path>?inline` returns the note with
  its image references embedded as `data:` URIs (markdown `![[...]]` / `![](...)`
  and HTML `<img>`), so a downloaded `.md`/`.html` is portable. The stored note
  keeps the reference.

### Changed
- Upload tool descriptions now lead with the cheap ingestion paths; a base64
  upload over ~128 KiB returns a `hint` redirecting to the HTTP/bridge path.

### Fixed
- HTML notes authored with a bare markdown `--- … ---` frontmatter block
  (instead of the HTML-comment-wrapped form) are now tolerated: their
  title/tags are indexed and the block no longer renders as visible text.

### Notes
- `media_notes`, `bridge_dir` and the `?inline` overlays are all
  off/lightweight by default; no migration. MCP reads and stored notes always
  keep the lightweight image reference.

## [2.8.0] — 2026-06-16 — "Vite 8 + Vue toolchain upgrade"

MINOR release. A full build-toolchain upgrade with no behaviour change for end
users — the SPA looks and works the same. Existing deployments need no
migration.

### Changed
- **Build toolchain upgraded to Vite 8** (bundler is now Rolldown), together
  with `@vitejs/plugin-vue` 5→6, Vitest 3→4, TypeScript 5.5→5.9, vue-tsc 2→3,
  `@vue/tsconfig` 0.5→0.9, `@intlify/unplugin-vue-i18n` 4→11 and **vue-i18n
  9→11**. The i18n catalogs are still AOT-precompiled, so the SPA keeps running
  under the strict `script-src 'self'` CSP with no runtime `eval`.
- `rollupOptions.output.manualChunks` migrated to the function form (Rolldown
  no longer accepts the object form); the graph/editor code-split is unchanged.
- Bundle output is marginally smaller with Rolldown.

### Security
- `npm audit` is clean (0 vulnerabilities): patched pre-existing DOMPurify,
  `form-data` (high-severity CRLF injection) and `js-yaml` advisories, and the
  esbuild advisory (`GHSA-gv7w-rqvm-qjhr`) stays pinned out via an `overrides`
  entry (esbuild is now only a build-time transitive of the i18n plugin).

### Internal
- The browser canary now also boots the **authenticated** app shell under the
  production CSP (via an injected auth state, no seeded user), not just the
  login page — closing the gap that let a vue-i18n runtime-compiler regression
  reach a real browser during this upgrade.

## [2.7.2] — 2026-06-15 — "dependency maintenance"

PATCH release. Routine dependency maintenance and a transitive security fix.
No behaviour changes; existing deployments need no migration.

### Security
- **esbuild pinned to `^0.28.1`** via an npm `overrides` entry, resolving
  `GHSA-gv7w-rqvm-qjhr` (missing binary integrity verification). The project
  uses esbuild only as a build-time tool (via Vite) and does not ship it in the
  runtime image, so real-world exposure was low — patched as hygiene. The fix
  stays on Vite 6; the Vite 6→8 major upgrade is intentionally deferred.

### Changed
- Bumped `golang.org/x/crypto` 0.52→0.53 and `golang.org/x/term` 0.43→0.44
  (plus transitive `x/sys`, `x/text`, `x/net`).
- Updated the Alpine runtime base image from 3.20 to 3.24.

## [2.7.1] — 2026-06-15 — "git-sync backup health + note download"

PATCH release. A degraded git-sync backup no longer marks the container
unhealthy, and a Download button is added to the note header. Purely additive
plus a health-reporting fix; existing deployments need no migration.

### Added
- **Download original file** — a Download button in the note header saves the
  note's raw source file (`.md` or `.html`) as-is, fully client-side (no server
  round-trip).

### Fixed
- **A degraded git-sync backup no longer fails container health.** When the
  git-sync backup is degraded — for example a push blocked by remote divergence
  or local repository corruption — the container is no longer reported
  `unhealthy`: `/healthz` readiness is now gated on the index (core) alone,
  while backup degradation stays observable in the `git_sync.healthy` field and
  the Prometheus gauge `gosidian_gitsync_status`. git-sync additionally
  classifies local repository corruption (`bad object` / empty object file) and
  surfaces an actionable error in place of a raw git message.

## [2.7.0] — 2026-06-13 — "print to PDF + unified frontmatter detection"

MINOR release. Adds a Print / Save-as-PDF action for Markdown notes in the web
UI, and unifies frontmatter detection across the indexer, linter and MCP tools
so they can no longer disagree on a note — fixing cases where MCP tools ignored
the frontmatter of `.html` notes. Purely additive; existing deployments need no
migration.

### Added

- **Print / Save as PDF for Markdown notes.** A Print button in the note header
  (view mode) prints just that note through the browser's native print /
  Save-as-PDF dialog. A scoped `@media print` stylesheet shows only the rendered
  note — hiding the rest of the plancia and the browser's own header/footer
  chrome — so a single, clean page reaches the printout. HTML notes are not
  printable yet (the browser clips the sandboxed iframe to a single page).

### Fixed

- **Frontmatter detection unified across subsystems.** A single path-aware
  parser primitive now backs the indexer, the linter and every MCP tool, so they
  can never disagree on whether a note has frontmatter or what its tags are. This
  fixes several MCP tools (`memory_todos`, `memory_importance`,
  `memory_refresh_hot`, `memory_handoff`, and the discovery tools) that
  previously read no frontmatter from `.html` notes — silently skipping their
  tags, status, and plan metadata. A cross-subsystem consistency test guards
  against the two detection paths drifting again.

## [2.6.0] — 2026-06-12 — "graph analytics + HTML notes"

MINOR release. Two additive feature tracks: graph-analytics MCP tools over the
wiki-link graph gosidian already maintains, and support for single-file `.html`
documents as first-class notes rendered in a sandboxed iframe (opt-in, off by
default). Purely additive; existing deployments need no migration.

### Added

- **Graph analytics MCP tools.** `memory_hubs` ranks the most-connected notes by
  undirected wiki-link degree (the inverse of orphan notes — where the graph
  concentrates). `memory_path` returns the shortest path between two notes over
  the wiki-link graph. Both scope to a single project or run vault-wide.
- **`unlinked-mentions` lint rule (opt-in).** Flags prose that names another
  note's title or basename without linking it, surfacing missing
  `[[wiki-links]]`. It is advisory and higher-noise on a dense vault, so it is
  excluded from the default lint run — name it explicitly in `rules` to enable it.
- **First-class single-file HTML notes (opt-in, off by default).** With
  `[vault] html_notes` enabled (env `GOSIDIAN_VAULT_HTML_NOTES`), a `.html` file
  (HTML + inline CSS/JS) is indexed like a markdown note: frontmatter (a
  `--- YAML ---` block wrapped in a leading HTML comment), tags, wiki-links and a
  full-text body all participate in the graph, backlinks and search. The web UI
  renders such notes in a sandboxed `<iframe sandbox="allow-scripts">` with no
  same-origin access and an injected `Content-Security-Policy: default-src 'none'`,
  so author-supplied scripts run but cannot reach the viewer's session, the API,
  or the network.

### Fixed

- **Lint frontmatter rules now understand HTML notes.** The `frontmatter-missing`
  and `frontmatter-tag-unknown` checks read an HTML note's comment-wrapped
  frontmatter (dispatching on file type the same way the indexer does), so a
  well-formed `.html` note is no longer reported as missing its frontmatter.
- **`memory_lint` reports the rules it actually ran.** The response `rules` field
  now reflects the requested rule set (e.g. an explicit opt-in rule) instead of
  always echoing the default list.

### Notes

- All changes are additive and backward-compatible. HTML notes are **off by
  default**: enable `html_notes` consciously after reviewing the sandbox/CSP
  security model, since it lets one user author markup that another user renders.
  No migration is required.

## [2.5.0] — 2026-06-10 — "self-improve opt-in toggle"

MINOR release. Operators can now toggle a token's self-improvement opt-in
after the token was created — from the CLI, the admin API, or the admin SPA —
instead of recreating the token or hand-editing the store. Bootstrap also stops
flagging the agent instruction file as missing when it correctly lives outside
the vault. Purely additive; existing deployments need no migration.

### Added

- **Toggle self-improve opt-in on existing tokens.** A new `gosidian token
  opt-in --id <id> [--off]` CLI subcommand (with `--all`), a
  `PATCH /api/v1/admin/tokens/{id}` endpoint, and a "Self-improve" toggle in the
  admin Tokens view let owners enrol or withdraw an already-issued MCP token
  from the self-improvement loop. Previously the flag could only be set at
  token-creation time. The token API view now includes `self_improve_opt_in`,
  and token updates are recorded with a new `token_update` audit action.

### Changed

- **`memory_bootstrap` no longer reports the instruction file as missing.** In
  the stub model the agent instruction file lives in the agent's working
  directory, outside the vault, so its vault-absence is expected. Bootstrap now
  sets `agent_md.expected_external` instead of listing `AGENTS.md` in `missing`
  (which is reserved for genuinely absent vault scaffold such as `hot.md` /
  `README.md`). This supersedes the v2.4.0 behavior where `AGENTS.md` was always
  added to `missing`.

### Notes

- All changes are additive and backward-compatible: existing tokens keep their
  current opt-in state, and the new API / bootstrap fields are additive — a
  client that ignored them keeps working. No migration is needed.

## [2.4.0] — 2026-06-10 — "directives versioning"

MINOR release. The agent-facing memory protocol gains versioned operational
directives served at session start, a thin versioned instruction-file stub,
and agent-agnostic instruction-file detection. Purely additive — existing
deployments need no migration; the new response fields are additive and the
self-healing directives upgrade older projects automatically.

### Added

- **Bootstrap-served directives.** `memory_bootstrap` now returns
  `directives_block` (the full operational directives — vault folder map,
  ingest rules, plan/skill conventions, end-of-task workflow, tag vocabulary)
  plus `directives_version`. Projects read the rules fresh every session
  instead of duplicating them in their instruction file, so they can no longer
  drift out of date.
- **Versioned instruction-file stub.** `memory_init_agent` now emits a thin
  stub (session-bootstrap rule + local-specifics) carrying a
  `<!-- gosidian:stub v=N -->` marker, and `memory_bootstrap` reports
  `stub_version` so an agent knows when its (rarely changing) stub should be
  regenerated. The full directives live in `directives_block`, not in the file.
- **Self-healing of pre-stub projects.** The served directives instruct a
  project whose instruction file predates the stub system to convert itself
  once at bootstrap — existing projects upgrade with no manual rollout.

### Changed

- **Agent-agnostic instruction-file detection.** Bootstrap no longer assumes
  `CLAUDE.md`: it probes `AGENTS.md`, `CLAUDE.md`, `.cursor/rules.mdc`,
  `CONVENTIONS.md` and reports the detected file as `agent_md` (replacing the
  former `CLAUDE.md`-specific `claude_md` field). `missing` lists `AGENTS.md`
  when none is present.
- **Single source of truth for conventions.** The scaffold templates
  (`karpathy-wiki` prompt + index READMEs) and the non-Claude init prompts
  (Cursor / Codex / Aider / generic) no longer restate the tag vocabulary,
  plan format, or ingest rules — they point at the bootstrap `directives_block`.

### Notes

- New MCP response fields are additive; a client that ignored them keeps
  working. The `claude_md` → `agent_md` rename only affects callers that read
  that informational field directly.

## [2.3.2] — 2026-06-09 — "security patch follow-up"

PATCH release. Completes the note-titles allocation hardening started in
v2.3.1. No user-facing or API changes; existing deployments need no
migration.

### Security

- **CodeQL `go/uncontrolled-allocation-size` (HIGH), second occurrence.**
  v2.3.1 capped the first of the two result-slice allocations in the
  note-titles autocomplete endpoint to a constant, but the second one (on
  the search branch) was left request-derived, so the alert stayed open.
  Both allocations now use the constant cap; neither depends on the
  request-supplied `limit`. As before, the value was already clamped to
  `[1, 50]`, so there was no practical exploit.

## [2.3.1] — 2026-06-09 — "security patch"

PATCH release. Dependency and static-analysis security fixes, a dev-tooling
upgrade, and dead-code / release-notes housekeeping. No user-facing or API
changes; existing deployments need no migration.

### Security

- **CodeQL `go/uncontrolled-allocation-size` (HIGH).** The note-titles
  autocomplete endpoint pre-allocated its result slice with a
  request-supplied `limit`. The value was already clamped to `[1, 50]`, so
  there was no real exploit, but the allocation now uses a constant cap so
  its size never depends on unvalidated input.
- **`go-ntlmssp` 0.1.0 → 0.1.1.** Closes a medium advisory (NTLM challenges
  could panic on malformed payloads); indirect dependency of the LDAP
  client.

### Changed

- **Dev tooling: Vite 5 → 6, Vitest 2 → 3.** Closes the critical Vitest
  advisory (UI-server arbitrary file read/exec) and the dev-only
  Vite/esbuild advisories — `npm audit` is now clean. No bundle behaviour
  change for end users; the runtime SPA stack (Vue 3.5, vue-i18n 9 AOT,
  Tailwind 3.4) is unchanged.

### Removed

- Dead code: three unbundled leftover SPA views from the plancia refactor
  (`HomeView`, `PlaceholderView`, `ConflictDialog`) and two unused Go
  test/router helpers.
- The per-version `RELEASE_NOTES_*.md` files — release notes are now
  consolidated into this single CHANGELOG (see the note above).

## [2.3.0] — 2026-06-08 — "plancia"

MINOR release. The web UI becomes a tiling window manager, and two
opt-in features land — all backward compatible and off by default.

### Added

- **Plancia window manager.** A niri-style scrollable tiling workspace:
  notes, the graph, search, and config forms open as resizable windows
  side by side instead of one page at a time. Windows resize in steps
  (small / medium / full), minimize to a scrollable footer, and a
  per-window "direct links" button opens the one-hop ego-graph of a note.
  Open windows + focus are encoded in the URL (shareable, reload-safe),
  with a `localStorage` fallback. The app menu moved into a collapsible
  sidebar section; the command palette (⌘K) and wikilinks open windows.
- **Global projects** (opt-in, off by default). Shared `global` (public)
  and `global-private` projects whose skills, agents, and scaffold
  templates any project can reuse, with local-overrides-global merge.
  Gated by `GOSIDIAN_GLOBAL_ENABLED` and a per-project `use_globals`
  flag. See `docs/vault/global-projects.md`.
- **Self-improvement loop** (experimental, off by default). Agents record
  structured usage-friction insights via the `memory_self_improve` MCP
  tool, kept in a private `insights` project for the owner to triage.
  Opt-in per token. See `docs/mcp/self-improvement.md`.
- Two new MCP tools — `memory_self_improve` and `memory_global_check`
  (48 tools total).

### Changed

- A note now opens as a **single window with an in-place View/Edit
  toggle** — editing no longer navigates to a separate page. The editor
  (CodeMirror) mounts lazily and is hidden for read-only users.
- **Documentation corrected**: the web UI has been a Vue 3 SPA since
  v2.0; the README, web-UI overview, FAQ, and architecture pages that
  still described the old HTMX stack are now accurate.
- `modernc.org/sqlite` updated to 1.52.0.

### Notes

- Fully backward compatible. Global projects and the self-improvement
  loop are **off by default**; existing deployments need no migration.
  Deep-link routes (`/notes/<path>`, `/graph`, …) still work — they open
  the matching window in the plancia.

## [2.2.0] — 2026-06-08 — "auth & roles"

MINOR release. Adds role-based access control, two-factor (TOTP), and
LDAP / Active Directory login on top of the existing multi-user web
login. Fully backward compatible: TOTP defaults to `off` and LDAP to
disabled, so existing single-owner / member setups are unaffected and
need no migration.

### Added

- **Role-based access control.** Three roles — **owner**, **member**,
  **guest** — enforced by a centralized, fail-closed authorization layer
  (`internal/authz`). A read the role may not perform returns **404**
  (the resource's existence is hidden); a forbidden write returns
  **403**. An unrecognized role degrades to public-read only.
- **Public / private projects.** Each project carries a `public` flag
  (default **private**). Public projects are readable by guests; private
  ones are visible only to owners and members. Guests are filtered
  consistently across the sidebar, search, tags, note titles, and the
  graph.
- **TOTP two-factor authentication.** A global mode — `off` /
  `optional` / `required` — plus a per-user override (inherit / enabled
  / disabled). Self-service enrolment with confirm-before-activate;
  `off` is a lockout-proof master switch. Configurable via
  `webauth.totp_mode` / `GOSIDIAN_TOTP_MODE`.
- **LDAP / Active Directory login.** Search-then-bind against an
  external directory; the first successful login auto-provisions a local
  **guest** account (no password stored). A local username always
  shadows LDAP. LDAPS and StartTLS are supported, with a configurable
  user filter (OpenLDAP `(uid=%s)`, Active Directory
  `(sAMAccountName=%s)`). New `[ldap]` config block and `GOSIDIAN_LDAP_*`
  environment variables.
- **Docs**: a new [Authentication & roles](docs/web-ui/authentication.md)
  page, plus a disposable LDAP test harness under `deploy/ldap-test/`.

### Changed

- **Graph view** now honours per-role visibility and opens on the most
  recently edited project the user can see, instead of rendering the
  entire vault at once.
- **`modernc.org/sqlite`** 1.51.0 → 1.52.0.

### Security

- Guests can never hold MCP tokens — token creation is owner/member-only
  and demoting a user to guest cascade-revokes their tokens — so the
  read-only boundary holds across both the web UI and MCP, with no
  MCP-layer changes required.

### Notes

- LDAP is validated end-to-end against OpenLDAP over plain LDAP, LDAPS,
  and StartTLS. The Active Directory path is configuration-only on the
  same code; validate it against your domain controller.

## [2.1.2] — 2026-06-08 — "security bundle"

PATCH bundle. Closes six open Dependabot PRs (#23–#28) and resolves
six Dependabot security alerts. The only runtime-facing fix is axios
in the SPA bundle; the Go bumps are routine hygiene; the dev-only
vitest critical is deferred (see below).

### Security

- **`axios`** 1.15.2 → 1.16.0 — runtime dependency, ships in the SPA
  bundle. Patches three advisories: ReDoS via cookie-name injection
  (high), allocation of resources without limits / DoS (high), and a
  proxy-authorization header injection via prototype pollution (low).
  Closes #25.
- **`js-cookie`** 3.0.5 → 3.0.8 — transitive, dev-only (via
  `js-beautify`). Patches per-instance prototype hijack in `assign()`
  (high). Closes #23.

### Changed

- **`github.com/mark3labs/mcp-go`** 0.52.0 → 0.54.1 (closes #28).
- **`modernc.org/sqlite`** 1.50.0 → 1.51.0 (closes #27).
- **`golang.org/x/crypto`** 0.51.0 → 0.52.0 (closes #24).
- Transitive via `go mod tidy`: `golang.org/x/sys` 0.44 → 0.45,
  `modernc.org/libc` 1.72.0 → 1.72.3.

### Deferred

- **`vitest`** 2.1.9 → 4.1.0 (#26) is *not* merged. The advisory
  (Vitest UI server arbitrary file read/exec, **critical**) is patched
  only in 4.1.0, which requires `vite ^6 || ^7 || ^8` — pulling in the
  full Vite 5 → 8 major upgrade already tracked for the v2.2.x cycle
  (incremental 5 → 6 → 7 → 8 with runtime SPA validation per step).
  vitest is a **dev-only** test runner; the vulnerable surface
  (`vitest --ui`) is never built into the image nor exposed in
  production. The alert remains `dismissed: tolerable_risk` until the
  Vite upgrade lands. #26 is closed without merging.

### Notes

- Unlike v2.1.0 / v2.1.1, the SPA `dist/` **is** rebuilt here to pick
  up axios 1.16.0 — this is a security release, not a Go-only PATCH.
  Vitest 16/16 green, `npm audit --omit=dev` = 0 vulnerabilities
  (runtime), `go test -race ./...` green across all packages.

## [2.1.1] — 2026-05-11 — "deps cleanup #2"

PATCH bundle. Closes 4 open Dependabot Go-module PRs and overrides
the Node 22 → 26 (Current) proposal with Node 22 → 24 (LTS) for
stability long-term. No runtime behaviour change — the SPA bundle is
unaffected (only Go deps + Docker base modified).

### Changed

- **`github.com/fsnotify/fsnotify`** 1.10.0 → 1.10.1 (closes #17).
- **`golang.org/x/term`** 0.42.0 → 0.43.0 (closes #18).
- **`golang.org/x/crypto`** 0.50.0 → 0.51.0 (closes #19).
- **`github.com/mark3labs/mcp-go`** 0.50.0 → 0.52.0 (closes #20).
  Includes upstream fix mark3labs/mcp-go#830
  *"setTools may resulted in an empty tools"* (v0.51.0) — defensive
  improvement to the pattern gosidian uses in `internal/mcp/tools.go`.
- **`Dockerfile`** builder stage: `node:22-alpine` → `node:24-alpine`
  (overrides Dependabot PR #16 which proposed `node:26-alpine`).
  Node 24 is the current LTS line (support through October 2027),
  Node 26 is the Current release with a shorter support window. The
  override prioritises stability over latest.
- Transitive bumps via `go mod tidy`: `golang.org/x/sys` 0.43 → 0.44,
  `golang.org/x/text` 0.36 → 0.37.

### Deferred

- **PR #15 vite 5.4.21 → 8.0.11 + esbuild removal + vitest major**
  is closed without merging. The jump spans 3 major versions of
  Vite (5 → 6 → 7 → 8) and removes esbuild as a direct dependency.
  CI build is green but the runtime SPA behaviour under strict CSP +
  plugin compatibility (@vitejs/plugin-vue, @intlify/unplugin-vue-i18n,
  Tailwind 3.4) was not validated. A dedicated upgrade plan with
  incremental 5 → 6 → 7 → 8 steps and runtime testing per step is
  tracked for the v2.2.x cycle. The two related Dependabot alerts
  (vite GHSA-4w7w-66w2-5vf9 medium, esbuild GHSA-67mh-4wv8-2f99
  medium) remain `dismissed: tolerable_risk` — dev-only attack
  surface, GitHub-hosted CI runner is the only consumer of `vite
  dev`.

### Notes

- **No behaviour change for end users**. The SPA `dist/` bundle is
  not rebuilt by this PATCH — the embedded assets match v2.1.0
  byte-for-byte. Only the Go binary and the builder image base
  change.
- `go vet ./...`, `go test ./...` 16/16 packages green
- `npm audit --omit=dev` = 0 vulnerabilities (unchanged from v2.0.1)
- Vitest 16/16 green

## [2.1.0] — 2026-05-08 — "lint vocabulary extension"

MINOR. Extends the `frontmatter-tag-unknown` lint rule with a
config-driven allow-list, so vaults can document their structural
tag patterns without weakening the rule for everyone. No behaviour
change for vaults that do not configure it.

### Added

- **`[lint.frontmatter_tag_vocabulary] extra_allowed`** in
  `<vault>/.gosidian/config.toml` — additive allow-list for the
  closed vocabulary checked by the `frontmatter-tag-unknown` rule.
  Format: `<namespace>:<value>` or bare token. Built-in namespaces
  (type/topic/status/pinned/project-name) are always honoured;
  the extension is purely additive — a vault never weakens its own
  discipline by setting this. Malformed entries (empty,
  leading/trailing colon, internal whitespace, double colon) are
  skipped silently at load time so a typo in the config does not
  crash the lint. See `docs/configuration.md#lint-vocabulary-extension`.
- New chainable setter `Linter.WithExtraAllowedTags(extra)` in the
  `internal/lint` package; `isKnownTag` is now a method so each
  Linter instance carries its own per-vault vocabulary extension.
- New `Server.SetLintExtraAllowedTags()` setter in the MCP package;
  `memory_lint` wires the per-vault config into each run.
- Three new unit tests covering the extension behaviour: extras
  silence configured tags, malformed entries are skipped silently,
  extras don't mask other unknowns.

### Changed

- `cmd/gosidian/main.go` reads `cfg.Lint.FrontmatterTagVocabulary.ExtraAllowed`
  at startup and passes it to the MCP server. Vaults without a
  `[lint]` section get the same behaviour as before.

### Notes

- **Backward-compatible**. Vaults without `[lint.frontmatter_tag_vocabulary]`
  see the built-in vocabulary unchanged. No migration, no schema
  delta, no runtime impact for existing deploys.
- Use case: a vault that legitimately uses tags outside the built-in
  namespaces (e.g. `status:reference` for reference notes that
  aren't snapshot/draft/done/archived, `topic:agent-template` for
  template-folder index notes) can document those tags in
  `.gosidian/config.toml` instead of accumulating warnings on every
  `memory_lint` run.

## [2.0.1] — 2026-05-08 — "deps cleanup"

PATCH bundle. Closes the two open Dependabot Go-module PRs and three
high+critical Dependabot npm advisories on dev dependencies. No
runtime behaviour change — the SPA bundle output is byte-identical
to v2.0.0.

### Changed

- **`github.com/mark3labs/mcp-go`** 0.47.1 → 0.50.0. Closes #12.
- **`github.com/fsnotify/fsnotify`** 1.7.0 → 1.10.0. Closes #13.
- **`web/` devDependencies**:
  - `happy-dom` 15.0.0 → 20.9.0 — closes GHSA-37j7-fg3j-429f
    (critical, VM Context Escape RCE), GHSA-w4gp-fjgq-3q4g (high,
    fetch credentials sourced from page origin), GHSA-6q6h-j7hj-3r64
    (high, ECMAScript module compiler unsanitised export names).
  - `playwright` + `@playwright/test` 1.47.0 → 1.59.1 — closes
    GHSA-7mvr-c777-76hp (high, browsers downloaded without integrity
    verification).
- **Go toolchain directive** auto-bumped 1.25.0 → 1.25.5 (side-effect
  of `go get`). Build target still pinned to `golang:1.25-alpine` in
  `Dockerfile`.

### Notes

- **Vite 5 → 6** (GHSA-4w7w-66w2-5vf9 medium, path traversal in dev
  server `.map` handling) and **esbuild 0.21 → 0.25**
  (GHSA-67mh-4wv8-2f99 medium, dev-server CSRF) **deliberately
  deferred**: dev-only attack paths (production build pipeline never
  exposes either dev server), GitHub-hosted CI runner is the only
  consumer, and Vite 6 is a major upgrade with a cascading plugin
  refresh cost (Vue plugin, intlify, Tailwind). Tracked for the v2.1
  cycle when the broader bundle modernisation lands.
- `vite build` produces a byte-identical SPA `dist/` post-bump — the
  upgraded dev tooling does not change the bundle. End users see no
  behavioural delta vs v2.0.0.
- Vitest unit suite 16/16 green with happy-dom 20. `go test ./...`
  green across all 16 packages. `npm audit --omit=dev` =
  0 vulnerabilities.

## [2.0.0] — 2026-05-08 — "SPA cutover + REST API v1"

**MAJOR.** The HTMX-rendered web UI is gone; gosidian now ships a
Vue 3 single-page application backed by a versioned REST API at
`/api/v1/*`. End-user URLs (`/notes/<path>`, `/projects/<slug>`,
`/graph`, `/search`, ...) keep working unchanged thanks to
Vue Router history mode + the SPA shell catch-all. The MCP transport
at `/mcp/sse` and the file upload pipeline are unchanged.

This is breaking for callers that hit legacy HTMX endpoints
directly. See `docs/migration-v2.md` and the **Migration from v1.x**
section below.

### Added

- **Vue 3 SPA** (`web/`) — production-grade single-page application
  served as `go:embed all:dist` from the Go binary. Stack: Vue 3.5 +
  TypeScript 5.5 (strict) + Vite 5 + Pinia 2 + vue-router 4 +
  Tailwind 3.4. AppShell + TopBar + sidebar tree, 4-mode editor
  (preview / split / stacked / edit-only) on CodeMirror 6, optimistic
  locking with conflict dialog (ETag + `If-Match` → 412),
  DOMPurify-sanitised markdown, axios + EventSource SSE, Pinia state
  with `pinia-plugin-persistedstate`. Lighthouse 100/100/100 on
  Login / Note / Graph.
- **REST API v1** at `/api/v1/*` — versioned, stable, fully
  documented. New `internal/api/v1/` package replaces the per-page
  Go handlers under `internal/server/handlers_*`. Endpoints cover
  the full v1.x surface plus the SPA-specific shapes (see
  `docs/migration-v2.md` for the full v1.x → v2.0 route mapping).
  Bearer-token authentication, rate-limited, JSON error envelope.
- **Strict Content-Security-Policy** attached to the SPA shell:
  `default-src 'self'`, `script-src 'self'` (no `unsafe-eval`,
  no `unsafe-inline`), `frame-ancestors 'none'`, `object-src 'none'`.
  Defence in depth: `X-Content-Type-Options nosniff`,
  `X-Frame-Options DENY`,
  `Referrer-Policy strict-origin-when-cross-origin`, minimal
  `Permissions-Policy`.
- **Theme presets** (4) — Catppuccin Mocha (default dark),
  Catppuccin Latte (light), Tokyo Night (dark blue),
  Solarized Light. Switchable at runtime via `/settings`. Each
  theme uses semantic CSS variable tokens; the Cytoscape graph
  resolves them to comma-form `rgb()`.
- **i18n** (5 languages: IT, EN, ES, FR, DE) — AOT-precompiled at
  build time via `@intlify/unplugin-vue-i18n` so vue-i18n's
  runtime message compiler never reaches `new Function()`. The
  SPA honours the server's `default_lang` from
  `GET /api/v1/version` on first boot.
- **Graph view** rewrite — Cytoscape 3.30 + fcose layout, three
  searchable comboboxes (Project / Tag / Focus) with sensible
  default sort orders (mtime desc, count desc, recent edits desc).
  Hover bring-forward highlights the focused node and dims the
  complement; full title shown on hover.
- **Playwright canary** (chromium + firefox) — regression guard
  for the runtime-eval / CSP-blocked-script class of incidents.
- **`docs/migration-v2.md`** — single source of truth for the
  upgrade path from v1.x, including the full v1.x → v2.0 route
  mapping table.

### Changed

- **Frontend bundle** is now `go:embed`ed from the SPA Vite build
  (`internal/server/web/dist/*`). Single binary, no separate static
  asset deployment. SPA shell catch-all serves `/notes/<path>`-style
  URLs to the Vue Router.
- **Login flow** — JWT-style bearer token (`gsp_<base64url>`)
  returned by `POST /api/v1/login`, persisted in the SPA under
  `localStorage["gosidian.auth"]` via Pinia persistedstate. The old
  cookie-session HTMX flow is gone.
- **Graph endpoint** payload includes server-side `tag`, `focus`,
  `depth`, `min_degree`, `limit` filters; the client now sends
  these as query params instead of computing on the fly.

### Removed

- **HTMX UI**: 21 Go HTML templates under
  `internal/server/templates/`, the per-page Go handlers
  (`internal/server/handlers_*.go`, `internal/server/render.go`),
  and the ~1 KLOC of custom JS / icons / CSS under
  `internal/server/static/{js,css,icons}/`. The new SPA + REST
  equivalent at `/api/v1/*` covers the same surface.
- **`gosidian_session` cookie auth** — replaced by Bearer tokens.
- **`GOSIDIAN_SPA_MODE` env var** — was a feature flag during the
  v2-spa development branch. With the cutover complete, the SPA is
  the only frontend and the flag is gone. Operators who set it
  explicitly can drop it from their env files.

### Fixed

- **Cytoscape theme rendering** — Cytoscape doesn't accept the
  `rgb(R G B)` space-separated form some Tailwind builds emit.
  The theme resolver now emits comma-form `rgb(R, G, B)` for graph
  styling. Fixes the blank/black graph nodes some users saw on
  initial paint after a theme switch.
- **i18n CSP failure** — vue-i18n's default runtime compiler used
  `new Function()`, which strict CSP blocks. Switched to AOT
  precompile via `@intlify/unplugin-vue-i18n`; the runtime carries
  no eval surface.
- **UI store hydration race** — `pinia-plugin-persistedstate`
  re-hydrated after `main.ts` had already read the store, causing
  themes to flash to the default on first paint. Hydration now
  happens inside `App.vue setup`.

### Security

- **`script-src 'self'`** strictly enforced. No `unsafe-eval`,
  no `unsafe-inline`. Verified end-to-end via Playwright canary
  in chromium + firefox at every CI run.
- **CSP defence in depth**: 4 additional headers
  (`X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`,
  `Permissions-Policy`).
- **Bearer-token rotation** on every login; server-side validation
  on every `/api/v1/*` request.

### Migration from v1.x

If you were on v1.1.0 or earlier:

1. **End-user URLs** keep working. Bookmarks survive.
2. **Legacy HTMX endpoints** (`/api/preview`, `/api/render`,
   `/api/tree`, `/api/backlinks`, `/api/note-excerpt`,
   `/api/command-palette`, `/api/attach`, `/api/upload`,
   `/api/i18n`) are gone (404). Migrate clients to the
   namespaced equivalents under `/api/v1/*`. See
   `docs/migration-v2.md` for the full mapping.
3. **Login flow** is JWT REST POST instead of cookie session.
   Bookmarklets / scripts that POSTed to `/login` should target
   `POST /api/v1/login` and use the returned `token` as
   `Authorization: Bearer <token>`.
4. **`GOSIDIAN_SPA_MODE`** — drop from env / compose. Removed.
5. **Database / vault format** — no migration. SQLite vault
   format is unchanged.
6. **MCP transport at `/mcp/sse`** — unchanged. MCP clients keep
   working without reconfiguration.
7. **Containers** — pull `ghcr.io/daniele-chiappa/gosidian:v2.0.0`,
   recreate the container, no volume changes.

### Notes

- Internal release counterpart: aggregates private `v2.0.0-beta`
  (Phase 0–8 cutover, 2026-05-01) plus 10 post-beta commits
  stabilising the SPA in production. Production deployment date:
  2026-05-05. Soak: 7 days on the beta tag, 3 days on prod, zero
  incident reports.
- New `docs/demo.gif` recorded against the v2.0 SPA via Playwright
  on a synthetic vault, replacing the v1.0 capture.

## [1.1.0] — 2026-04-29 — "Agent workflow + single-port"

Two-bundle MINOR. MCP transport now co-locates with the web UI on a
single port; two new MCP tools land for agent adoption and decoupled
file staging; the upload pipeline gets magic-bytes verification.

### Added

- **`memory_init_agent` MCP tool** — replaces `/init`-style scaffolding
  for Claude Code, Cursor, Codex, Aider, generic. Two modes
  (augment / from-scratch) selected by `existing_content`. New
  package `internal/initprompt/` with renderer + per-profile
  prompts + a single shared `gosidian_block.tmpl.md`.
- **`memory_upload_resource` MCP tool** — pre-uploader twin of
  `memory_upload_attachment`. Same storage and validation, returns
  the resource handle (path, url, mime, kind, size, hash) without
  an embed markdown — agents stage files first, attach later.
- **MCP single-port mode**: SSE transport mounted on the web port at
  `/mcp/sse`. A single SSH tunnel forwarding port 8080 reaches both
  web UI and MCP transport. New client URL:
  `http://<host>:8080/mcp/sse`. The legacy standalone listener
  (`--mcp-addr` / `GOSIDIAN_MCP_ADDR`) is now opt-in (deprecated).
- **`docs/mcp/upload.md`** — single reference for the three
  equivalent upload paths: REST `POST /api/upload`, MCP
  `memory_upload_attachment`, MCP `memory_upload_resource`. Decision
  tree, request/response contracts, MIME tolerance per extension,
  unified error catalogue mapping the same validation failures to
  REST status codes and MCP error results.
- **`docs/examples/docker-compose.image.yml`** — annotated template
  for pull-from-GHCR deploys (no Go toolchain, no `docker login`
  required for the public image, single-port mode by default).

### Changed

- **`/api/upload` co-located with MCP** on the web port. Agents
  behind an SSH tunnel forwarding only 8080 can now reach both the
  REST upload endpoint and the MCP transport through the same
  forward, removing a class of remote-deployment confusion.
- **SQLite engine bumped to 3.53.0** via `modernc.org/sqlite`
  1.32.0 → 1.50.0. Pure correctness margin from upstream fixes
  (64-bit RowID ABI, `Deserialize` memory leak,
  `commitHookTrampoline` signature) — gosidian doesn't use the
  affected APIs but the pull-through is still net-positive.
  Includes new `ColumnInfo` API and sqlite-vec v0.1.9.

### Fixed

- **MCP `/mcp/sse` returned `500 Streaming unsupported`** when
  reached via the shared web mux. Root cause: the metrics middleware
  wrapped the response writer in a struct that did not propagate
  `Flush()` through Go's interface promotion; the SSE handler's
  `w.(http.Flusher)` assertion failed and the stream never opened.
  Fix: the wrapper now forwards `Flush()` and exposes `Unwrap()`
  for `http.NewResponseController` compatibility.
- **`source_path` upload errors hint at the `data` parameter for
  remote setups**. Users running gosidian behind an SSH tunnel hit
  a cryptic "not inside any allowed upload root" — `source_path`
  is resolved server-side, so a client-side path will never match
  the allow-list. Both that error and the file-not-found branch
  now point the caller at base64 `data` as the correct alternative
  for cross-host uploads. Same hint added to the MCP tool
  descriptions so callers see it in the schema.

### Security

- **Magic-bytes verification on uploads** rejects MIME-spoofed
  payloads. `attach.VerifyMIME` inspects the first 512 bytes with
  `http.DetectContentType` and confirms the detected MIME family
  matches the declared extension, with per-extension tolerance
  (SVG/text, DOCX/XLSX as zip containers, PDF/ZIP exact). Catches
  the classic "JS-as-PNG", "HTML-as-PDF", "plain-text-as-ZIP"
  spoof shapes the extension allowlist alone could not stop.

### Deprecated

- `--mcp-addr` flag and `GOSIDIAN_MCP_ADDR` env var. Will be removed
  in a future major release once `/mcp/sse` has been the default
  for at least two minor versions. Existing deployments that keep
  the env var set continue to work and expose both endpoints; new
  deployments should drop the env var and migrate clients to
  `/mcp/sse`.

## [1.0.1] — 2026-04-25 — "Security hardening"

Bundle of security findings raised by the first CodeQL run on the
public repo. No behaviour change for callers using the documented
APIs; only inputs that would have escaped the intended root via
traversal, absolute paths, or null bytes are now rejected up-front.

### Security

- **`internal/trash`**: every public entry point (`DiscardNote`,
  `DiscardProject`, `Restore`, `Purge`) now calls a new `validateName`
  helper before any `filepath.Join`. The guard rejects empty input,
  null bytes, absolute paths (`/foo`, `\foo`), and any `..` component
  on either separator. Defends against path traversal via user-supplied
  ids/names reaching the trash directory or vault root. Closes 10
  CodeQL `go/path-injection` alerts (CWE-22).
- **`internal/server/handlers_login.go`** (`safeNext`): redirect
  validation now also rejects `?next=/\evil.com` (backslash
  protocol-relative URL), in addition to the already-existing reject
  for `?next=//evil.com`. Closes `go/bad-redirect-check` alert.
- **`internal/server/handlers_i18n.go`**: `lang` cookie now sets
  `Secure: true` when the request is over TLS (matches the pattern
  already used by the webauth session cookie). Closes
  `go/cookie-secure-not-set` alert.
- **`internal/audit/audit.go`** (`Log.Write`): `f.Close()` is now
  observed via a deferred closure that promotes a close-time error
  to the function's return value (named return). Prevents silent
  data loss on a failed flush. Closes
  `go/unhandled-writable-file-close` alert.

### Internal

- New regression test `TestBin_RejectsPathTraversal` in
  `internal/trash/trash_test.go` covers eight bad-input shapes
  (empty, `..`, `../etc/passwd`, `foo/../../etc`, absolute
  Linux/Windows paths, `..\windows`, null byte) across all four
  trash entry points.
- `validateName` is intentionally strict: callers that need looser
  semantics (e.g. allowing forward-slash separators in legitimate
  rel paths) sanitize before reaching the trash module.

### Notes

- Zero schema changes, zero breaking changes for third-party users.
- 8 additional CodeQL alerts on `internal/vault/vault.go` were
  dismissed as false positives (path-injection guarded by the
  existing `sanitizeProjectName` helper, which CodeQL does not
  recognize as a sanitizer).

## [1.0.0] — 2026-04-25 — "Initial public release"

First public release of gosidian: a self-hosted, Obsidian-compatible
markdown vault with a first-class MCP server so AI agents get the same
memory you do. Nine internal iterations (v1.0.0 → v1.10.0, ~1 week of
intensive work) stabilised the surface; this release opens the doors.

### Highlights

- **44 MCP tools** covering the full read / write / search /
  discovery / workflow surface — every tool honours ETag optimistic
  locking and token-scoped project filtering.
- **HTMX web UI** (editor, full-text search, graph view, command
  palette, settings, admin) running on the same binary as the MCP
  server.
- **Multi-project vault layout**: `<project>/<note>.md`, with
  cross-project wiki-links, scoped tokens, and a single SQLite FTS5
  index across the whole tree.
- **Agent-first design**: `memory_bootstrap(project)` returns the
  session-start context in one call; ingest patterns (ADR, plan,
  skill, agent, docs) are pre-baked conventions, not free-form.

### Features

**MCP server (44 tools)**

- Core CRUD: `memory_get`, `memory_get_section`, `memory_get_outline`,
  `memory_get_frontmatter`, `memory_create`, `memory_update`,
  `memory_edit`, `memory_append`, `memory_delete`, `memory_batch_get`
- Search & discovery: `memory_search` (cross-project filter),
  `memory_list_notes`, `memory_list_projects`, `memory_list_tags`,
  `memory_notes_by_tag`, `memory_notes_by_importance`, `memory_recent`,
  `memory_backlinks`, `memory_outlinks` (cross-project flag),
  `memory_stale`, `memory_pinned`, `memory_plans`, `memory_skills`
- Workflow: `memory_bootstrap`, `memory_create_handoff`,
  `memory_pending_handoffs`, `memory_compact` (dry-run), `memory_self_stats`,
  `memory_refresh_hot`, `memory_todos`, `memory_lint`, `memory_ask`
- Scaffold: `memory_project_scaffold` (multi-template),
  `memory_list_bootstrap_templates`, `memory_create_project`,
  `memory_delete_project`, `memory_rename_project`, `memory_rename_note`,
  `memory_move_note`
- Attachments: `memory_upload_attachment`, `memory_list_attachments`,
  `memory_delete_attachment`, `memory_attachment_info`
- Audit: `memory_audit_tail`

**Web UI (HTMX)**

- Editor with live preview, full-text search, graph view (Cytoscape
  vendored — no CDN), command palette, theme editor, multi-project
  navigation, settings page.
- Three admin-level theme presets: Midnight Luxury (default), Light
  Clean, High Contrast WCAG-AAA. Custom 5-colour palette supported.
- Language selector in the topbar: IT (complete), EN (reference),
  ES / FR / DE (scaffolding stubs with EN fallback).
- 22 Lucide SVG icons inline, theme-aware via `currentColor`.

**Vault**

- Obsidian-compatible: YAML frontmatter, wiki-links (`[[note]]`,
  `[[project/note|alias]]`), cross-project links.
- Closed tag vocabulary: `type:*`, `status:*`, `topic:*`, `pinned`,
  plus numeric `importance: 1..5`.
- LRU cache for hot-file reads, ETag optimistic locking on all writes,
  synchronous index update after every write.
- Attachments: 6 image types + 7 document types; upload via HTTP or
  MCP base64 / source-path modes.

**Auth & multi-user**

- Web login with `owner` / `member` roles, invite-only signup,
  configurable session TTL + rate-limit window + failure cap.
- MCP bearer tokens scoped by project + scope (`read` / `write` /
  `admin`); token management via web `/admin/tokens`.
- Single-account files auto-migrate to multi-user schema on first
  start.

**Git sync**

- Auto-commit / push of the vault to a Gitea / GitHub remote.
- Graceful fail at boot: if git is unreachable, gosidian starts in
  local-only mode and logs a warning (never fatal).
- Configurable debounce, branch, author, token via env vars.

**Bootstrap templates**

- Three presets ship embedded and are seeded into
  `<vault>/.gosidian/templates/` on first start:
  `karpathy-wiki` (full layout, default), `minimal`, `team`.
- User-editable after seeding; `cp -r` a template, edit, use
  immediately — no rebuild.
- `_template.toml` meta with `name`, `description`, `prompt`,
  `[[variables]]` (required / default / auto=date / auto=project).

**Observability**

- Prometheus metrics on `/metrics` (request counter, latency
  histogram, payload bytes).
- Structured `slog` middleware for MCP calls + HTTP handlers.
- Append-only audit log with per-user filter
  (`memory_audit_tail` tool, `/audit` web page).
- `/healthz` returns `{status, version, vault, notes, git_sync}`.

**Docs**

- `docs/` tree with 19 reference files organised in four areas
  (`mcp/`, `web-ui/`, `vault/`, plus top-level getting-started,
  configuration, deployment, architecture, development, faq).
- 125-line landing README with tagline, 3-command quick start,
  audience blurbs, and a documentation index.
- `CONTRIBUTING.md`, `SECURITY.md`, `CODE_OF_CONDUCT.md`,
  `PROJECT-STORY.md`, `LICENSE` (MIT).

### Internal

- Single Go binary, multi-stage Docker image (`alpine:3.20` with
  `git` + `ca-certificates`, ~45 MB compressed).
- SQLite via `modernc.org/sqlite` — pure-Go, CGO-disabled, works
  out of the box on Alpine.
- Embedded HTML / CSS / JS + 22 SVG icons via `//go:embed`.
- Full test suite with `-race` detector; tests ship inside the
  repo with `testdata/` fixtures.
- CI pipeline: `go vet` + `go test -race -count=1` + `go build` +
  build & push Docker image to `ghcr.io/daniele-chiappa/gosidian`.

### Notes

- **Public versioning is independent from prior internal versioning.**
  See "Prior internal development" below. v1.0.0 public = snapshot of
  v1.10.0 internal, rebased as "first public release".
- Initial distribution: binary / Docker image on GHCR + source. Docker
  Hub mirroring is reactive (added if concrete demand emerges).
- Zero breaking changes on the MCP tool schemas across the 44 tools —
  SemVer applies from v1.0.0 forward for public consumers.

---

## Prior internal development

gosidian was developed as an internal tool through nine versions before
being published. The internal CHANGELOG is retained below for context
on how the codebase evolved. **Public versioning restarts at v1.0.0**;
the internal `v1.x` line is not part of the public release stream and
does not map onto future public releases one-to-one.

- **v1.10.0** (2026-04-23) — UI polish bundle: Lucide icon set (22
  SVG), three theme presets (Midnight Luxury / Light Clean / High
  Contrast), language selector (IT + EN complete; ES/FR/DE stubs),
  docs split (README 587 → 125 lines, 19 files under `docs/`), removal
  of the "Today" daily-note handler.
- **v1.9.0** (2026-04-22) — Four new MCP tools for agent workflow:
  `memory_todos` (GFM checkbox extraction with plan-status
  enrichment), `memory_lint` (5 baseline rules: broken-wikilink,
  orphan-note, frontmatter-missing, frontmatter-tag-unknown,
  status-incoherent), `memory_ask` (structured OQ-NNN append),
  `memory_search` gained `projects[]` cross-project filter.
- **v1.8.0** (2026-04-22) — Bootstrap templates system.
  `memory_project_scaffold` reads from
  `<vault>/.gosidian/templates/<name>/`; three presets ship embedded
  (`karpathy-wiki`, `minimal`, `team`) and are seeded idempotently.
  New tool `memory_list_bootstrap_templates`; scaffold extended with
  `template` + `variables` parameters.
- **v1.7.2** (2026-04-22) — `SECURITY.md` pointed to a platform-level
  vulnerability reporting flow (GitHub Security Advisories in the
  public version).
- **v1.7.1** (2026-04-22) — Full README rewrite in English with
  installation, configuration, CLI, MCP client wiring, vault layout,
  i18n, development, backup/DR sections. Added `CONTRIBUTING.md`,
  `SECURITY.md`, `CHANGELOG.md`.
- **v1.7.0** (2026-04-22) — Environment-variable coverage extended
  (trash / theme / webauth / vault cache / i18n default). Per-project
  tag filtering. `internal/i18n` package with embedded JSON
  catalogues; `Accept-Language` for MCP clients. `PROJECT-STORY.md`,
  MIT `LICENSE` added.
- **v1.6.0** (2026-04-22) — `memory_project_scaffold` (first
  iteration), `memory_refresh_hot`, `/api/graph?include_cross_project`.
  `auth.Store` hot-reloads `tokens.json` via mtime check — no server
  restart needed after a CLI `token create`. Cytoscape + layout libs
  vendored.
- **v1.5.0** (2026-04-22) — Agent-to-agent handoffs
  (`memory_create_handoff` + `memory_pending_handoffs`).
  `memory_compact` with dry-run. `memory_self_stats` for
  auto-throttling. `memory_outlinks` cross-project flag.
- **v1.4.0** (2026-04-22) — Multi-user web login with owner / member
  roles, invite-only signup, session eviction on user disable.
  `auth.Token.OwnerUserID` with cascade revoke. Per-user `audit.Entry`.
  `auth.json` schema migration v1 → v2 on load.
- **v1.3.0** (2026-04-22) — Frontmatter `importance: 1..5` +
  dedicated `memory_notes_by_importance`. `memory_search` gained
  `include_outline` + `include_frontmatter` flags.
- **v1.2.0** (2026-04-22) — `memory_bootstrap` (single-call
  session-start aggregate). Structured discovery: `memory_plans`,
  `memory_skills`, `memory_pinned`, `memory_stale`. `pinned` tag
  convention.
- **v1.1.0** (2026-04-21) — `/admin/tokens` web page for creating,
  listing, revoking MCP tokens without dropping to the shell.
- **v1.0.0** (internal, 2026-04-21) — First internal tagged release.
  HTTP web UI with HTMX sidebar / editor / search / graph /
  themes; MCP server with 17 tools; ETag optimistic locking; LRU
  vault cache; attachments; audit log; Prometheus metrics;
  structured slog; gitsync with graceful fail; multi-project vault
  layout. SQLite FTS5 as primary datastore.

[1.0.0]: #
