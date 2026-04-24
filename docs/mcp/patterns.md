# Agent patterns

Canonical workflows for agents working against a gosidian vault.
Following these keeps retrieval predictable and write safety tight.

## Session opening (bootstrap → discover → read)

1. **Bootstrap**: `memory_bootstrap(project)` in one call to get the
   hot state + active plans + skills + recent notes + top tags + a
   `missing[]` list of expected-but-absent canonical files.
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

When passing the baton between specialised agents:

1. Source agent: `memory_create_handoff(from_agent, to_agent,
   summary, pending_items)`. Creates
   `<project>/handoffs/YYYYMMDD-<slug>.md` with frontmatter
   `type:handoff, status:pending`.
2. Target agent: `memory_pending_handoffs(for_agent=<name>)` on
   session open.
3. Target agent closes the handoff with `memory_update` setting
   `status:done` once the work lands.

This replaces ad-hoc "read the latest plan to figure out what's left"
with a structured, queryable artifact.
