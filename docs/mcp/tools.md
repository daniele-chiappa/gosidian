# MCP tool catalogue

As of v1.0.0 (v1.9 internal), **44 tools** cover the full
retrieval → write → workflow → self-check cycle for agent memory.

Consult each tool's `description` via your client's `tools/list` call
for precise schemas; the groupings below are the conceptual map.

## Triage / bootstrap

- `memory_bootstrap(project)` — single-call session start: hot state +
  active plans + skills + recent notes + top tags
- `memory_recent(project)` — notes recently modified
- `memory_pinned(project)` — notes tagged `pinned`
- `memory_stale(project, older_than)` — unmodified long enough to
  review/archive
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
- `memory_get(path)`, `memory_get_section(path, heading)`,
  `memory_get_frontmatter(path)`, `memory_get_outline(path)` — full
  body vs cheap triage variants
- `memory_batch_get(paths)` — one round-trip for multiple notes
- `memory_list_notes(project)`, `memory_list_projects()`,
  `memory_list_tags(project?)`
- `memory_notes_by_tag(tag, project?)`
- `memory_backlinks(path)`, `memory_outlinks(path,
  include_cross_project?)`

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

## Attachments

- `memory_upload_attachment(note_path, content_b64|source_path,
  filename)`
- `memory_list_attachments(note_path)`
- `memory_delete_attachment(attachment_path)`
- `memory_attachment_info(attachment_path)`

## Workflow

- `memory_create_handoff(from_agent, to_agent, summary, pending_items)`
- `memory_pending_handoffs(for_agent)`
- `memory_compact(path, keep_last_n, archive_summary, dry_run?)` —
  shrink log-shaped notes safely
- `memory_self_stats()` — token identity + rate-limit snapshot (for
  auto-throttling)
- `memory_project_scaffold(project, template?, variables?)` — idempotent
  Karpathy-Wiki-Stack bootstrap
- `memory_refresh_hot(project)` — regenerate the "Recent decisions"
  section of `hot.md` between opt-in markers
- `memory_list_bootstrap_templates()` — discover available templates
  + their intended-use prompts

## Self-check

- `memory_lint(project, rules?, min_severity?)` — structural vault
  hygiene: `broken-wikilink`, `orphan-note`, `frontmatter-missing`,
  `frontmatter-tag-unknown`, `status-incoherent`. Zero
  `severity:error` on a coherent vault.

## Audit

- `memory_audit_tail(filters?)` — stream the audit log
- `memory_pending_handoffs(for_agent)` — see Workflow

## Invariants

- **ETag optimistic locking** is uniform across every write tool: pass
  `if_match=<etag>` from the matching `memory_get*` to refuse a
  concurrent modification.
- **Scoped tokens** restrict every call transparently: a token scoped
  to `project=foo` can only read/write `foo/`, and `memory_search`
  with `projects=[...]` silently intersects with its scope.
- **Cross-project** is opt-in — `memory_search projects=["a","b"]`
  and `memory_outlinks include_cross_project=true` — so default
  behaviour stays tightly scoped.
