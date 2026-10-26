# Upgrading

To upgrade, pull the new image and restart the container. Most releases need
nothing else; only the releases listed below need your attention. You can skip
releases, as long as you read every entry between your version and the new
one, newest first, and do what each one says. MCP clients keep the tool list
they read when they connected, so reconnect them after an upgrade to see new
tools and arguments. The full notes of every release are in `CHANGELOG.md`.

## 2.116.0 — The checks of a team's tasks

- A team made with 2.115.0 keeps its work pipeline
  (`pipelines/<team>-work.md`): it runs the checks also when the agent
  pushed nothing, and a failed check fails the run instead of giving the
  work back. Change its three nodes as `memory_create_team` now writes
  them (the rest of each node stays):

  ```yaml
  - id: checks
    use: exec.run
    when: "{{ agent.next.checks }}"
    on_error: continue
    with:
      fail_on_error: false        # added to repo and script
  - id: publish
    use: preview.publish
    when: "{{ agent.next.preview }}"
    unless: "{{ checks.exit_code }}"
    on_error: continue
  - id: look
    use: preview.check
    when: "{{ publish.id }}"
    on_error: continue
  ```

- A role that writes in the vault must write: a task whose agent wrote no
  note in the role's `writes` goes back. Give the tasks that only read
  (a review) to a role without `writes`.

## 2.110.0 — Uploads into IN for agents

- Nothing to do. `memory_in_upload` is a new tool: reconnect the MCP
  clients to see it. Its pieces go to `/mcp/in-upload/`, under the same
  64 MiB as those of the web UI.
- `[in.mcp]` caps the agents' uploads; no cap by default, the disk reserve
  of `[in]` holds.

## 2.109.0 — Uploads into IN

- The IN window uploads files, folders and zips. Each piece is a request
  of up to 64 MiB: a proxy in front of gosidian must let it through
  (`client_max_body_size 64m` for nginx; the default of
  nginx-proxy-manager is larger, Cloudflare takes 100 MB).
- An upload keeps 5 GiB free on the IN folders' disk (`[in]
  reserve_bytes`, `GOSIDIAN_IN_RESERVE_BYTES`; `0` turns it off). Set the
  other caps of `[in]` if your users should not fill it.
- A project with `track_in_git` now leaves `IN/.uploads/` out of git.

## 2.108.0 — Compose presets

- Nothing changes for an install that sets none of the new variables.
- A variable named `GOSIDIAN_PLUGIN_<NAME>_URL` now registers a plugin:
  check your environment for one you did not mean as that.
  `GOSIDIAN_PLUGIN_<NAME>_TOKEN`, when set, replaces the plugin's
  `token_secret`.
- `deploy/presets/` is new: a base compose file and the `rag` and
  `storage` purposes, configured from one `.env`; see
  `docs/deployment.md`. The project template `rag` comes with it.

## 2.107.1 — The database skill, revised

- `global/skills/design-project-databases` is revised. A copy seeded by
  2.107.0 is never overwritten: delete it before the restart (or after,
  and restart again) to get the new one.

## 2.107.0 — Databases: directives, a skill, a lint rule

- `memory_lint` runs a new default rule, `field-tag-mismatch`: a
  `status:*` tag that disagrees with the note's `status` field, and on a
  database's rows a `field:value` tag that disagrees with that select
  field. Expect new warnings where a tag was left behind; fix the tag, or
  drop it, since queries read the field.
- With global projects on, the first start seeds
  `global/skills/design-project-databases`. An edited copy is never
  overwritten.
- The directives are at v26: agents read them at their next
  `memory_bootstrap`, nothing to regenerate.

## 2.106.1 — Staging folders, push variables, reserved field names

- The bridge dir and the upload roots set inside the vault now stay out of
  git sync: the managed `.gitignore` names them, and a commit that already
  holds them drops them from git's index (the files stay on disk, the history
  keeps what was pushed). An upload root set on a visible folder, a project,
  leaves the backup too; the log names each folder it leaves out at start.
- A push with a token keeps the `GIT_CONFIG_COUNT` / `GIT_CONFIG_KEY_n` /
  `GIT_CONFIG_VALUE_n` variables you set: the token takes the next free index.
- `this`, `this.path` and `this.name` in a view are the note's own even when
  its frontmatter has a `path` or `name` field. Lint and the writes warn
  about a database field named `path`, `name`, `modified`, `links`,
  `created_by`, `modified_by`, `created_at` or `modified_at`: rename it
  (`folder`, say) in the schema and the rows.

## 2.104.0 — Git maintenance at night

- With git sync on, `git gc` now runs once a day at `03:30` in the server's
  time zone (UTC unless the container sets `TZ`), while no commit runs,
  instead of git running it right after a commit. Set `[git] maintenance`
  (`GOSIDIAN_GIT_MAINTENANCE`) to another time, to `auto` for git's own
  maintenance as before, or to `off`.
- A value of `[git] maintenance` that is not a time, `auto` or `off` stops
  git sync at start, as a bad branch does.
- gosidian writes `gc.auto = 0` and `maintenance.auto = false` into the vault
  repository; `auto` takes back only those values.
- At start gosidian writes a managed block into the repository's
  `info/attributes` that turns off delta search for png, jpg, jpeg, gif, webp,
  pdf, zip, docx and xlsx. Lines outside its markers stay, and the file does
  not reach the remote; see `docs/configuration.md`.

## 2.103.1 — Batched disk syncs for pipeline steps

- The new `[vault] durability` (`GOSIDIAN_VAULT_DURABILITY`) defaults to
  `batch`: a single write (MCP, web UI, API) is still synced before it
  returns, but the notes and attachments of a pipeline step are synced
  together when the step ends. A power loss in the middle of a step can lose
  what it wrote, and the step runs again.
- Set `always` for the behaviour of every earlier release, or `none` to never
  sync. The log says the mode at start (`vault durability: batch`).
- The first start builds a new index on frontmatter keys and values; there is
  no re-index. 2.103.0 was never released.

## 2.102.1 — New index for frontmatter sorts

- The first start builds a new index on note, key and value (under a second
  for a 400 MB index); there is no re-index.

## 2.102.0 — Note times follow `TZ`

- If the container sets `TZ`, the times and dates gosidian writes into notes
  now use that zone, as RFC 3339 with the offset: handoff names and their
  `created`, `claimed_at` and `completed_at`, `{{TODAY}}`, snapshots,
  `memory_ask` dates, insights, `{{ run.created }}` and the names of downloaded
  zips. Without `TZ` the container stays in UTC, as before.
- The server's own records and the API's answers about them (audit, tokens,
  accounts, runs, `modified`) stay in UTC, and notes written before keep their
  names. The log says the zone at start (`note times: …`).
- Date-times with different offsets now compare by instant in
  `memory_query`, views, rollups and automations; one without an offset
  counts in the server's zone. There is no re-index.

## 2.98.0 — Downloads bring their files

- A zip of a folder, a project or the vault now carries the files its notes
  show or link, and rewrites those references as paths relative to the note;
  files from outside the zip go into `attachments/` at its top. The stored
  notes do not change.
- Downloading a single note (`GET /api/v1/notes/{path}?export=zip`, the web
  UI's download) gives a zip with the note and its files when it shows any.
  It used to be a markdown file with its images inlined as `data:` URIs.

## 2.96.0 — Typed frontmatter in pipelines

- Fix pipelines that compare a frontmatter value read by `note.read` or
  `note.list` with `"true"`, or call a string method on a number: values now
  come with their types (a bool is `true`, a number a number, a map a map).
  Compare with `True`, or `in [True, "true"]` to work on both versions.
- `title`, `tags`, an unquoted `[[wikilink]]`, dates and codes with leading
  zeros (`007`) still come as text. `note.write` now writes a whole number as
  one (`1792237963`, not `1.792237963e+09`).

## 2.95.0 — Push follows tags

- git sync now pushes with `--follow-tags`: the annotated tags of the branch
  go to the remote, including any annotated tag made by hand in the vault's
  repository.

## 2.94.0 — Full re-index

- The first start re-indexes the whole vault, because the index gains two
  columns: about 85 s for 22,000 notes, answering 503 "starting" meanwhile.
  Later starts are as quick as before.
- For chat bots, images in place and bots back at once after their plugin
  restarts need `gosidian-chat` 0.4.0; 0.3.0 keeps working, its bots coming
  back within five minutes.

## 2.90.0 — New caps on runs and graphs

- A run starts at most 1000 runs with `pipeline.run`, in an `each` too
  (`[pipelines] max_run_children`).
- `GET /api/v1/graph` gives at most `[web] graph_max_nodes` nodes, whatever
  limit is asked; it had no maximum.
- The caps that were constants in the code are now settings with
  `GOSIDIAN_*` variables, their defaults the old values; see
  `docs/configuration.md`.

## 2.89.0 — Graph without a limit

- `GET /api/v1/graph` asked without a limit keeps the 2000 most linked nodes,
  the focus among them, and `stats.total` says how many there were; it sent
  them all.
- A plugin's graph that the plugin answers with an error is a 422 with code
  `plugin.refused` and the plugin's message.

## 2.86.0 — Plugin name `memory` refused

- A plugin may not be named `memory`, since its tools would be `memory_*`,
  gosidian's own. Register such a plugin under another name.

## 2.81.0 — No symbolic links under attachments

- `/vault-files/` no longer follows a symbolic link under an `attachments/`
  folder: an attachment opens only with no link along its path. Replace
  linked attachments with real files.

## 2.76.0 — Re-index for diagram links

- The first start re-indexes the vault, since the links of a diagram's nodes
  are now links of the note: seconds for a vault of a few thousand notes.

## 2.72.2 — Health details, OAuth issuer, vault cache

- `/healthz` without credentials now gives only the status, the version, the
  MCP tool count and whether git sync is enabled and healthy. The vault path,
  the note count and git sync's last error need an owner's web session, or an
  MCP token of the owner (or of no account) without a project list; update
  monitoring that reads them.
- An OAuth issuer with a path (`https://host/gosidian`) stops the server at
  start: gosidian must be served at the root of its host.
- `cache_size = 0` (`GOSIDIAN_VAULT_CACHE_SIZE=0`) now turns the vault's load
  cache off; it used to be set back to 128.
- `gosidian mirror sync` writes its index to `<project>/.gosidian-index.md`
  instead of `<project>/_index.md`; the next sync removes the old generated
  index.
- The example compose files (`docs/examples/docker-compose.image.yml`,
  `deploy/docker-compose.yml`) now run the container read-only, with a tmpfs
  on `/tmp`, no capabilities and no new privileges, and the one in `deploy/`
  binds the loopback. Check your setup before you copy them;
  `docs/deployment.md` explains both.
- A base or a canvas that is a symbolic link, or sits in a linked folder, is
  no longer read. `memory_delete_project` and `memory_rename_project` refuse
  a project hidden from MCP.
- If the log says a request carried `X-Forwarded-For` while no trusted proxy
  is set, list your proxy in `[webauth] trusted_proxies`: otherwise every
  client behind it shares one rate-limit bucket.
- `gosidian healthcheck` now asks the port of `GOSIDIAN_ADDR`, so a server
  moved off `:8080` passes the container's healthcheck. 2.72.1 was never
  released.

## 2.71.4 — Note HTML, invites and the editor

- The HTML inside a markdown note keeps fewer styles: `<style>` and forms are
  dropped (a task list keeps its checkboxes), the `style` attribute keeps only
  text color, background color, weight, slant, alignment and decoration, and
  `class` keeps only the classes the renderer writes. Notes that relied on
  other styles render differently.
- An unknown, used or expired invite answers 400 with the error code
  `auth.invite_invalid`, where it was `validation.invalid_format`.
- Links in a preview no longer navigate the whole page: notes open in a
  window, other addresses in a new tab. Leaving the page with unsaved edits
  now asks first, and a date typed in a database field saves on Enter or when
  the field is left.

## 2.71.3 — Two-factor mode, git sync, re-index

- The first start re-indexes the vault (a few seconds per thousand notes),
  because tags and code blocks are read differently.
- Check the log for a `totp_mode` warning after the upgrade: an unknown value
  now reads `required`, and `Required` or `require` are understood. Until now
  both turned two-factor off without a word.
- From this release up to 2.106.0, with a git push token, the push replaced
  your own `GIT_CONFIG_COUNT` / `GIT_CONFIG_KEY_n` / `GIT_CONFIG_VALUE_n`
  variables (for instance a `safe.directory` for a vault owned by another
  uid), so every push failed with "detected dubious ownership" while commits
  went on. On 2.71.3–2.106.0, set `safe.directory` in the system or global git
  config instead of the environment; 2.106.1 fixes it, the token taking the
  next free index.
- A project with git sync off, and a state directory inside the vault, now
  leave git's index at the next commit, so they stop going to the remote. The
  files stay on disk, and the history keeps what was already pushed.
- The git branch set from Settings must be a valid branch name.
- An account named like a reserved project (`insights`, `global`,
  `global-private`) gets no personal project; `gosidian user add` applies the
  same rule.
- The views of an Obsidian base include the subfolders of its folder, and a
  base without a folder filter reads the whole project. Short lowercase words
  of letters a to f (`#cafe`, `#feed`, `#add`, `#bad`) are tags again.
- The owner's own two-factor can no longer be removed from Admin → Users with
  the session alone (403): use Settings, which asks for the password. A
  password reset from Admin → Users also closes the account's OAuth grants.
- A package import that overwrites notes goes through the shrink guard of
  `memory_update`: pass `allow_shrink` to confirm, or nothing is written.
  `import-vault` refuses a destination that is the source, inside it or
  around it.
- The legacy `public: false` of `PUT /api/v1/projects/{name}` now takes a
  public project down to internal and leaves a private one private.

## 2.71.2 — Sources, state directory, two-factor, trash

- A `source_path` inside the vault must now be readable by the token: in its
  scope, outside hidden folders, the state directory and the projects hidden
  from MCP. A staging folder inside the vault, a hidden (dot) folder included,
  is a source only when it is the bridge dir (`GOSIDIAN_MCP_BRIDGE_DIR`) or
  listed in `GOSIDIAN_MCP_ALLOWED_UPLOAD_ROOTS`.
- From 2.106.1 the refusal says so, and git sync leaves the bridge dir and
  the upload roots out of the repository; before 2.106.1 their content was
  committed and pushed.
- A state directory inside the vault under a visible name (`<vault>/state`) is
  now hidden like a dot folder and named in the managed `.gitignore`; a commit
  that held it drops it at the next sync. If it was ever pushed, rotate the
  tokens it holds, the push token included.
- The vault root itself is refused as the state directory. Outside the vault
  (`/data` in Docker) nothing changes.
- An MCP token now reads on `/vault-files/` only what its owner may currently
  read, and never a project hidden from MCP. Tokens without an owner (CLI,
  admin) keep their scope.
- `POST /api/v1/totp/enroll` and `/totp/confirm` answer 409 while a second
  factor is active: disable it first, which takes the password.
- `bridge_filename` may not be `.` or `..`.
- The managed `.gitignore` lines for projects with git sync off are now
  anchored at the vault root and escaped (`/docs/`, `/a\*b/`). Folders of the
  same name inside other projects, which were left out by mistake, now go to
  the remote, and projects named like `#private` or `!x` are now kept off it.
- A trash retention of 0 now keeps the trash for ever. Until now a saved 0
  became the 30-day default at the next start, and the startup prune deleted
  the older entries.

## 2.71.0 — State directory under a visible name

- If your state directory sits inside the vault under a visible name, its zip
  export held the credentials, the configuration and the audit log for anyone
  who could read that folder as a project; it now answers 404. Move the state
  directory outside the vault, as the documentation recommends
  (`GOSIDIAN_STATE_DIR=/data`); the default `<vault>/.gosidian` is hidden and
  not affected.

## 2.70.0 — Legacy MCP port closed

- The image no longer opens the standalone MCP port 8765 and exposes 8080
  only. Before you update, move clients from `http://<host>:8765/sse` to
  `http://<host>:8080/mcp` (transport `http`, same bearer token).
- To keep the old listener, set `GOSIDIAN_MCP_ADDR: "0.0.0.0:8765"` and map
  the port. The example `deploy/docker-compose.yml` drops both.

## 2.68.0 — Enabled languages

- Check `[i18n] enabled_langs`: it now limits the languages the web UI
  offers. A `config.toml` that lists fewer languages (a past Settings save may
  have written `["it", "en"]`) offers only those, and a browser whose language
  is no longer enabled falls back to `default_lang`.
- Unset, every language with a catalogue is offered, as before. The setting
  also has a variable now, `GOSIDIAN_I18N_ENABLED_LANGS` (comma-separated).

## 2.67.2 — Server theme settings removed

- `[theme]` in `config.toml` and the `GOSIDIAN_THEME_*` variables are no
  longer read: remove them. A `[theme]` section still loads and is dropped at
  the next settings save; the web UI's theme is unchanged.

## 2.67.0 — Search reads links as words

- The first start re-reads every note (about 15 seconds for 1,400 notes),
  answering "starting" meanwhile.
- Full-text search no longer matches the folder path of a link: a link reads
  as its alias, or as the linked note's file name and heading. To find the
  notes that link a page, use `memory_backlinks`.

## 2.64.0 — Shrink guard

- `memory_update`, and `memory_ingest` with `overwrite`, now refuse a content
  under 10% of a note of 1 KiB or more. Clients confirm a meant shrink with
  `allow_shrink: true`, which they list after they reconnect.
- Tune the guard with `[mcp] shrink_guard_percent` and
  `shrink_guard_min_bytes` (`GOSIDIAN_MCP_SHRINK_GUARD_PERCENT`,
  `GOSIDIAN_MCP_SHRINK_GUARD_MIN_BYTES`); a percent of 0 turns it off.

## 2.62.0 — Portable project names

- A project name with a NUL or another control character, a trailing dot or
  a Windows device name (`CON`, `NUL`, `COM1`…) is refused when a project is
  created or renamed. A project that already has such a name can be deleted,
  renamed and restored again from 2.71.3.

## 2.61.0 — MCP always needs a token

- An install with no MCP token at all (an empty `<state-dir>/tokens.json`)
  answered every MCP request as admin; such requests now get 401. Create a
  token in Settings → My MCP tokens or with `gosidian token create`, or, for
  local use only, set `GOSIDIAN_MCP_OPEN=true`.
- The token is needed on `/mcp`, `/mcp/sse`, `/upload`, `/download`,
  `/append`, `/manifest` and the legacy standalone listener. Installs that
  already have MCP tokens are not affected.

## 2.60.1 — `gosidian user disable` takes an account

- `gosidian user disable` without arguments used to remove every account; it
  now refuses. Use `--username <name>` to disable one account (its web
  sessions end and its MCP tokens are revoked) or `--all` to remove them all,
  and update scripts that call it.
- A fresh install with no account now creates the owner `admin` with a random
  password, shown once in the log and kept in
  `<state-dir>/initial-admin-password`. `GOSIDIAN_AUTO_OWNER=false` turns this
  off; installs that have accounts are not affected.

## 2.59.0 — Password for sensitive actions

- API clients that remove two-factor (`DELETE /api/v1/totp`), create MCP
  tokens (`POST /api/v1/me/tokens`, `POST /api/v1/admin/tokens`) or change or
  reset a password must send the account's current password in the body as
  `password`. A wrong one answers 403.
- An account the owner creates, or whose password the owner resets, must
  choose its own password at the next login; until then the API answers 403
  `auth.password_change_required`. Accounts created before this release are
  not asked.
- The event stream now reads the session from an `HttpOnly` cookie,
  `gosidian_events`; `?token=` on `/api/v1/events` is deprecated, though
  still accepted.
- With the trash on, `memory_delete` moves the note to the trash instead of
  removing it from disk.

## 2.58.0 — Hex colors are not tags

- The first start re-extracts every note (content version 7) and drops the
  tags that were hex colors (`#AC1F24`, `#fff`). A note that used such a word
  as a tag on purpose keeps it by writing it in its frontmatter; from 2.71.3
  short lowercase words such as `#cafe` are tags again.

## 2.57.0 — Automations and rollup values

- Set `automations.timezone` (`GOSIDIAN_AUTOMATIONS_TIMEZONE`, an IANA
  zone) if the server's local zone is not the one your rules mean: a
  container usually runs in UTC. From 2.102.0 the default is the server's
  `TZ`.
- Automations are on by default (`automations.enabled`), but a rule acts only
  where a database note declares it.
- A rollup's value is now a number in `memory_query` and
  `POST /api/v1/query`, not text. A query over several folders that uses the
  rollups of their databases is refused and names the folder to query alone.

## 2.55.0 — Note authors from the audit log

- The first start adds a table of the notes' authors to the index (schema 5)
  and fills it from the audit log.

## 2.53.0 — Write limit per MCP session

- `mcp.write_per_minute` (`GOSIDIAN_MCP_WRITE_PER_MINUTE`, default 60) now
  caps each MCP session, and all the sessions of one token together get five
  times that. If you changed it, check the value: agents that share a token
  now get up to five times the setting in total.

## 2.50.1 — Frontmatter read as YAML

- The first start re-reads every note (a few seconds), as the index gains a
  column.
- Frontmatter that is valid YAML is now read as YAML: an unquoted ` #` in a
  value starts a comment and cuts it. The lint rule `frontmatter-invalid-yaml`
  and the write notices point such values out.
- Wikilinks in frontmatter values now count as links: notes gain backlinks, a
  rename rewrites them, and `broken-wikilink` reports one that resolves
  nowhere.
- `memory_get_frontmatter` and `memory_search` with `include_frontmatter`
  return a list field as an array instead of its text.

## 2.45.0 — Re-read for database editing

- The first start re-reads every note once, which takes a few seconds.

## 2.43.0 — Database schemas

- A database note written for 2.42.0 with `fields` as a list of prose lines
  is reported by lint (`database-field-invalid`) until its `fields` become a
  map.

## 2.41.4 — Project deletion and tokens

- Deleting a project now revokes the MCP tokens scoped to it alone and
  removes it from tokens that list other projects. Creating, restoring or
  renaming a project onto a name that MCP tokens are still scoped to is
  refused.
- A project trashed before this release is the owner's alone in the trash and
  comes back private, with the owner as admin.
- `memory_delete_project` now goes through the trash when the trash is on.

## 2.41.3 — New lint warnings

- `memory_lint` may report new `status-incoherent` warnings: plans whose
  status is only a `status:in-progress` tag are now checked too.

## 2.41.2 — Project renames and tokens

- A project rename now rewrites the scope of the MCP tokens that name it, and
  a rename onto a name that MCP tokens are still scoped to is refused.
- `memory_append` (and `POST /mcp/append`) now creates a missing note with a
  frontmatter: its title and its project as tag.

## 2.40.0 — Instruction-file stub v3

- `stub_version` goes from 2 to 3: each project's agent regenerates its
  instruction-file stub once, at its next `memory_bootstrap`. Expect one
  change to those files (`CLAUDE.md` and the like) in each project.

## 2.39.0 — Incremental index, 503 while starting

- The first start migrates the index and re-reads every note once (a few
  seconds on a vault of hundreds of notes); later starts re-read only the
  notes that changed.
- The port now opens at once and, until the index is ready, every request
  gets 503 with `Retry-After: 2`, and `/healthz` reports
  `{"status":"starting"}`. Health checks that require `"ok"` keep working.

## 2.38.0 — `user setup` and owner-only projects

- `gosidian user setup` now resets the owner in place (the password, and the
  TOTP secret with `--totp`) and keeps every other account and static MCP
  token. Any other username on an instance with accounts is refused; the old
  behaviour, which replaced the accounts file, needs `--replace`.
- If you ran any version from 2.30.0 to 2.37.x with member accounts, open the
  settings of the private global project and of the self-improvement project,
  set them to private and remove the grants you did not mean to give. An
  upgrade from before 2.30 straight to this release or later keeps them
  private.
- Settings set through `GOSIDIAN_*` variables now show read-only in the
  settings page, and a change to one through `/api/v1/settings` is refused.
- A refused MCP token now says why: 401 `invalid_token` when its account was
  deleted or disabled, 403 `insufficient_scope` when the account can read no
  project. The start log lists the tokens whose owner is missing or disabled.

## 2.36.1 — Earlier `hot-oversize` warning

- The `hot-oversize` lint rule and the bootstrap's `maintenance.hot_oversize`
  fire past 8 KiB instead of 16 KiB; `[lint] hot_oversize_bytes` still
  overrides the threshold.

## 2.32.0 — Restricted new accounts

- An account created from now on (Admin → Users, invite, LDAP) starts
  restricted: it sees only the projects granted to it, directly or through a
  team. The owner lifts the flag in Admin → Users; existing accounts are not
  restricted.
- A new User account gets a private personal project named after it, unless
  you switch this off in Settings → Project access.

## 2.30.0 — Project visibility and grants

- At the first start `projects.json` is converted once: public projects stay
  public, the others become internal (private when member scope was on),
  memberships become grants at the same level, and with member scope off
  every member gets a write grant on every existing project.
- The global `member_scope` setting is gone. Changing a project's settings,
  renaming or deleting it now takes the `admin` level on that project, and
  writing a project created after the upgrade takes a grant.
- New projects default to internal on an upgraded install; change it in
  Settings → Project access. The `public` project field stays as an alias
  (`true` → public, `false` → internal); new clients read `visibility`.
- If you upgrade from before 2.30 to 2.72.2 or later, a member's read
  membership under `member_scope` unset becomes a write grant, since that
  member could write before. See 2.38.0 for the private global and
  self-improvement projects.

## 2.29.1 — Tokens follow the account's access

- MCP tokens of member and guest accounts are now narrowed on every request
  to what the account may currently read and write: a grant removed or
  lowered applies at once, and an account with no readable project left is
  refused. Owner and CLI tokens are unchanged.
- `GET /api/v1/events` now sends each account only the events of the projects
  it may read.

## 2.28.0 — DNS-rebinding guard on `/mcp`

- Both MCP endpoints refuse with 403 a request that arrives on a
  loopback-bound connection with a `Host` that is not a localhost value. A
  reverse proxy on the same host that forwards over `127.0.0.1` with the
  public `Host` trips it: send `Host: localhost`, or set
  `mcp.disable_dns_rebinding_protection`
  (`GOSIDIAN_MCP_DISABLE_DNS_REBINDING_PROTECTION`).
- `GET /mcp` answers 405 by design. Clients on `/mcp/sse` need no change; to
  move Claude Code to `/mcp`, remove the server and add it again with
  `--transport http`.

## 2.27.0 — Two-factor recovery codes

- Accounts enrolled in two-factor before this release have no recovery
  codes: ask their users to generate a set in Settings → Two-factor, which
  takes a current TOTP code.
- `POST /api/v1/totp/confirm` now answers 200 with the codes instead of 204.
  Wrong second-factor codes now close the account for the login window
  (`login_max_failures`, `login_window`), whatever the client's address.

## 2.26.1 — Trusted proxies

- If gosidian sits behind a reverse proxy, list the proxy in
  `webauth.trusted_proxies` (`GOSIDIAN_TRUSTED_PROXIES`, IPs or CIDRs).
  `X-Forwarded-For` is honoured only from those peers; otherwise every client
  behind the proxy shares one failed-login bucket.
- A malformed entry in the `memory_ingest` URL allowlist now stops the server
  at start instead of being ignored.
- Delete, rename, move and the upload paths now count against the per-token
  write rate limit, and a token holds at most 16 unredeemed ingest tickets.

## 2.25.0 — State directory

- `--db` / `GOSIDIAN_DB` now moves the index alone. If you relied on every
  state file following the index's directory, point `--state-dir` /
  `GOSIDIAN_STATE_DIR` at the same directory.
- To keep credentials, configuration and the index out of the vault, mount a
  volume on `/data` and set `GOSIDIAN_STATE_DIR=/data`; the first start moves
  the files once and leaves a `STATE-MOVED-TO` marker. The default stays
  `<vault>/.gosidian/`.

## 2.24.4 — Note-only reads

- `GET /api/v1/notes/{path}`, `memory_get` and the other note read tools
  refuse a path that is not a note; the API answers 400. Agents that fetched
  attachment bytes with `memory_get` should use `memory_attachment_info` and
  `/vault-files/`.

## 2.24.3 — Attachments need authentication

- `/vault-files/` now needs a credential: 401 without one, 404 for a project
  the caller cannot see. Attachment URLs used outside the web UI need
  `Authorization: Bearer` with a session token or an MCP token; in the web UI,
  reload the page if an image does not load.

## 2.24.2 — Inert attachments, note-only writes

- `/vault-files/` serves attachments with a sandbox CSP, `nosniff` and
  `no-referrer`, and non-image attachments (PDF, CSV, JSON, TXT, ZIP, DOCX,
  XLSX) as downloads instead of inline.
- `PUT` and `DELETE /api/v1/notes/{path}` and the MCP write tools refuse any
  file that is not a note; attachments go through the upload tools.
- If untrusted members or write-scoped MCP tokens have existed on the
  instance, review the stored `.svg` attachments for script content.

## 2.24.1 — Hidden paths unreachable

- A path with any segment that starts with `.` is now refused by the vault
  layer, at any depth and for every role, so hidden files and folders inside
  projects cannot be reached by path either.
- If a member account or an MCP token without a project scope has ever
  existed on the instance, rotate account passwords and TOTP enrolments,
  outstanding invite tokens, the git push token and MCP tokens. The audit log
  records no reads, so a disclosure leaves no trace.

## 2.24.0 — Go 1.27

- Building from source now needs Go 1.27 or newer; the image needs nothing.
- From this release `/mcp/sse` refuses with 403 a request on a loopback-bound
  connection whose `Host` is not a localhost value (documented in 2.28.0). If
  a reverse proxy on the same host forwards over `127.0.0.1`, send
  `Host: localhost`; the setting that turns the guard off comes in 2.28.0.

## 2.23.1 — Credential store through `?inline`

- If git sync is on and a member account has existed on the instance, rotate
  the git push token and any outstanding invite tokens.

## 2.19.0 — One door for files

- Tokens with the `core` tool profile now see `memory_ingest` instead of
  `memory_upload_attachment`, `memory_upload_resource` and the media and
  table note creators; `full` tokens keep them.

## 2.17.0 — `memory_get` read guard

- `memory_get` returns a note over 24 KiB truncated (frontmatter, heading
  outline and the first 4 KiB, with `truncated: true`). Agents read the rest
  with `memory_get_section`, or pass `raw: true` for the whole body.
- `memory_bootstrap` now defaults to `mode` auto: an oversize `hot.md` comes
  in lite shape (frontmatter and outline, `auto_lite: true`).

## 2.12.0 — Two-factor enforced

- With the global TOTP policy `required`, the server now enforces
  enrolment: an account without a second factor gets 403
  `auth.enrollment_required` until it enrols, and the web UI shows the
  enrolment screen, even mid-session.

## 2.7.1 — Health ignores git sync

- `/healthz` readiness now depends on the index alone: a degraded git-sync
  backup no longer marks the container unhealthy. Watch `git_sync.healthy` in
  `/healthz` or the `gosidian_gitsync_status` gauge to alert on backup
  failures.

## 2.4.0 — `agent_md` in the bootstrap

- `memory_bootstrap` reports the detected instruction file as `agent_md`,
  replacing `claude_md`; update callers that read that field.

## 2.0.0 — Web UI as a single-page app

- The legacy HTMX endpoints (`/api/preview`, `/api/render`, `/api/tree`,
  `/api/backlinks`, `/api/note-excerpt`, `/api/command-palette`,
  `/api/attach`, `/api/upload`, `/api/i18n`) are gone (404). Move clients to
  `/api/v1/*`, mapped in `docs/migration-v2.md`.
- Login is `POST /api/v1/login`, which returns a token to send as
  `Authorization: Bearer <token>`; the `gosidian_session` cookie is gone.
- Remove `GOSIDIAN_SPA_MODE` from your environment. MCP at `/mcp/sse`,
  end-user URLs and the vault need no change.
