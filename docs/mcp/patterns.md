# Agent patterns

Canonical workflows for agents working against a gosidian vault.
Following these keeps retrieval predictable and write safety tight.

## Session opening (bootstrap → discover → read)

1. **Bootstrap**: `memory_bootstrap(project)` in one call to get the
   hot state + active plans + skills + recent notes + top tags + a
   `missing[]` list of expected-but-absent canonical files.
   **Repeat bootstraps should be slim**: pass
   `known_directives_version` (from the previous payload) to omit the
   directives block, `known_etags={path: etag}` so unchanged
   hot/README/instruction files come back as `unchanged:true` with no
   body, and `mode="lite"` to get the `hot.md` frontmatter + outline
   instead of its full body (pull sections on demand with
   `memory_get_section`); with `mode` unset an oversize `hot.md` goes
   lite automatically (`auto_lite:true`). A frequently-respawned sub-agent pays a few
   hundred bytes instead of the full payload when nothing changed.
2. **Discover**: `memory_plans(project, status="in-progress")` or
   `memory_notes_by_tag(tag, project)` for typed retrieval.
   `memory_todos(project, only_open=true)` to enumerate pending
   checkboxes across all active plans.
3. **Read**: `memory_get_frontmatter` or `memory_get_outline` for
   cheap triage; `memory_get` or `memory_get_section` for the body.
   `memory_search("…", projects=["a","b"])` for cross-project FTS5.

## Writes (every call is ETag-aware)

1. `memory_get*` → note `etag` in the response.
2. `memory_update(path, content, if_match=etag)` /
   `memory_edit(path, old, new, if_match=etag)` /
   `memory_append(path, content, if_match=etag)`.
3. On etag mismatch, reload the note and retry — the server never
   overwrites silently.
4. For quick open-question capture, `memory_ask(project, question)`
   appends a structured `### OQ-NNN` block without formatting it by
   hand.

## Session closing (self-check)

- `memory_lint(project)` before closing. A healthy project returns
  zero `severity:error` issues. `warning` and `info` are guidance,
  not blocking.
- `memory_compact(path, keep_last_n, archive_summary)` when a
  log-shaped note grows unwieldy.
- `memory_refresh_hot(project)` if the project uses the opt-in
  `<!-- auto:recent-decisions -->` markers in its `hot.md`.

## Hooks: bootstrap and capture without discipline

The opening and closing above depend on the agent remembering to do
them. Claude Code hooks make the two ends automatic:
[`contrib/claude-code/`](../../contrib/claude-code/README.md) ships one
script that, registered in `.claude/settings.json`, injects
`<project>/hot.md` at `SessionStart` — whole when it is small, with its
etag in the bootstrap reminder so the bootstrap does not repeat it,
otherwise its **Current focus** — (and this session's checkpoint after a
resume or a compaction), writes a
**checkpoint digest** to `<project>/sessions/<date>-<id>.md` at
`PreCompact` and the **session digest** at `SessionEnd` — first prompt,
turns, tools, files touched, last assistant message, no LLM involved.

The hooks talk HTTP with a bearer token, no MCP session: `GET
/mcp/download?path=` to read and `POST /mcp/append?path=` to write,
the latter running the same pipeline as `memory_append` (per-note lock,
`If-Match`, size and rate limits, index, audit, live events). The
bootstrap advertises both under `capabilities.attachments`.

The digests are a safety net in a low-importance note, not curated
memory: the agent still runs `memory_bootstrap`, keeps `hot.md` and
`log.md` and promotes what matters. `memory_stale` and the maintenance
digest sweep old session notes.

## Bootstrap a new project

```text
# 1. discover templates + read their intended-use prompts
memory_list_bootstrap_templates()

# 2. create the top-level folder (admin token required)
memory_create_project(name="newproject")

# 3. populate the layout from the chosen template
memory_project_scaffold(project="newproject", template="karpathy-wiki")

# 4. verify: bootstrap reports what's there and what's still missing
memory_bootstrap(project="newproject")
```

`memory_project_scaffold` is **idempotent**: re-running it never
overwrites existing files. The response splits output into
`created: [...]` and `skipped: [...]`. Variable substitution covers
`{{PROJECT}}` and `{{TODAY}}` plus any custom variable declared in
the template's `_template.toml`.

The three built-in templates are described in
[Vault format](../vault/format.md#bootstrap-templates).

## Agent-to-agent handoff

When passing the baton between specialised agents, the handoff is a
note with a server-enforced lifecycle: `pending → claimed → done |
rejected`.

1. Source agent: `memory_create_handoff(project, from_agent,
   to_agent, summary, pending_items?)`. Creates
   `<project>/handoffs/YYYYMMDD-<slug>.md` with frontmatter
   `type:handoff, status:pending` and a server-stamped `created_by`
   (token identity — `from_agent`/`to_agent` are declarative role
   slugs, `*_by` fields are who actually held the credentials).
2. Target agent: `memory_pending_handoffs(project,
   for_agent=<name>)` on session open.
3. **Claim before working**: `memory_claim_handoff(path)`. The flip
   `pending → claimed` is atomic under a per-note lock — when several
   workers race for the same handoff exactly one wins and the others
   get an "already claimed by <who>" error, so work is never done
   twice.
4. When the work lands (or is refused):
   `memory_complete_handoff(path, outcome="done"|"rejected",
   note?)`. Only the claiming token — or an admin token, the escape
   hatch for dead agents — can complete; the optional note is
   appended as an `## Outcome` section.
5. An orchestrator monitors work in flight with
   `memory_pending_handoffs(project, status="claimed")` (or
   `"done"`, `"rejected"`, `"all"`).

This replaces ad-hoc "read the latest plan to figure out what's left"
with a structured, queryable artifact — and, with claim/complete, a
minimal multi-agent task queue where every note stays plain markdown.

## Waiting for changes (instead of polling)

A worker or orchestrator that would otherwise poll
`memory_pending_handoffs` / `memory_recent` in a loop should park on
the change feed:

```text
# 1. establish the cursor (first call returns immediately on timeout)
memory_wait_changes(timeout_s=1)                  → {cursor: N}

# 2. loop: block until something changes in scope (max 55s per call)
memory_wait_changes(cursor=N, timeout_s=55)       → {events: [...], cursor: M}
#    ...react to events (e.g. a new note under <project>/handoffs/),
#    then call again with cursor=M — the short replay ring bridges
#    the gap between two calls, nothing is lost.

# 3. if the response carries resync=true the cursor fell out of the
#    replay window: reconcile with memory_recent, then resume.
```

One wait per MCP session: call it back-to-back, not in parallel. A
multi-project token (see
[Authentication](authentication.md#per-project-scoping)) waits on all
of its projects at once — the natural shape for an orchestrator
supervising several agent projects.
