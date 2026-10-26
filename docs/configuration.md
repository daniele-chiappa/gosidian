# Configuration

Settings come from four sources, in decreasing precedence:

1. **CLI flags** (`--vault`, `--addr`, `--db`, `--mcp-addr`)
2. **Environment variables** (`GOSIDIAN_*`)
3. **`<state-dir>/config.toml`** (edited from `/settings` UI)
4. **Built-in defaults**

## Environment variables

| Variable | Config field | Default |
|---|---|---|
| `GOSIDIAN_VAULT` | — (CLI only `--vault`) | *required* |
| `GOSIDIAN_ADDR` | — (CLI `--addr`) | `:8080` |
| `GOSIDIAN_STATE_DIR` | `--state-dir` | `<vault>/.gosidian` (see [State directory](#state-directory)) |
| `GOSIDIAN_DB` | — (CLI `--db`) | `<state-dir>/index.db` |
| `GOSIDIAN_MCP_ADDR` | — (CLI `--mcp-addr`) | empty (legacy SSE listener disabled; MCP is always at `/mcp` and `/mcp/sse` on the web port) |
| `GOSIDIAN_LOG_LEVEL` | — | `info` (`debug`, `warn`, `error`) |
| `GOSIDIAN_LOG_FORMAT` | — | `text` (`json`) |
| `GOSIDIAN_PPROF_ADDR` | — | empty (off). A loopback `host:port`, such as `127.0.0.1:6060`, opens Go's profiler at `/debug/pprof/` to measure a slow server under its real load: CPU, heap, blocking and mutex profiles, and an execution trace. It has no authentication, so another host is refused; read it from the same host or container, e.g. `docker exec gosidian wget -qO- 'http://127.0.0.1:6060/debug/pprof/profile?seconds=30' > cpu.out`, then `go tool pprof cpu.out` |
| `TZ` | — | unset: the system's zone, UTC in a container (the server's time zone, an IANA name such as `Europe/Rome`: the times and days gosidian writes into notes and their names, and by default those of the automations and the pipelines; see [Time zone](#time-zone)) |
| `GOSIDIAN_GIT_ENABLED` | `git.enabled` | `false` |
| `GOSIDIAN_GIT_REMOTE` | `git.remote` | empty |
| `GOSIDIAN_GIT_BRANCH` | `git.branch` | `main` |
| `GOSIDIAN_GIT_AUTHOR_NAME` | `git.author_name` | `Gosidian` |
| `GOSIDIAN_GIT_AUTHOR_EMAIL` | `git.author_email` | `gosidian@localhost` |
| `GOSIDIAN_GIT_DEBOUNCE` | `git.commit_debounce` | `30s` |
| `GOSIDIAN_GIT_PUSH` | `git.push` | `false` (when on, each commit is pushed, with the annotated tags of the branch the remote lacks: those of [`vault.commit`](pipelines.md#revisions-as-git-history)) |
| `GOSIDIAN_GIT_TOKEN_ENV` | `git.token_env` | empty (the variable holding the push token; the push hands it to git as an `http.extraheader` in the environment, after any `GIT_CONFIG_COUNT`/`GIT_CONFIG_KEY_n`/`GIT_CONFIG_VALUE_n` you set yourself, such as a `safe.directory` for a vault another uid owns: those stay) |
| `GOSIDIAN_GIT_MAINTENANCE` | `git.maintenance` | `03:30` (when git packs the repository: a time of day in the server's time zone, `auto` for git's own after each commit, or `off`; see [Git maintenance](#git-maintenance)) |
| `GOSIDIAN_MCP_WRITE_PER_MINUTE` | `mcp.write_per_minute` | `60` (writes per minute of one MCP session, rolling window; all the sessions of a token together get 5× that, IMP-141) |
| `GOSIDIAN_MCP_MAX_NOTE_BYTES` | `mcp.max_note_bytes` | `1048576` (1 MiB) |
| `GOSIDIAN_MCP_MAX_PLUGIN_RESULT` | `mcp.max_plugin_result` | `1048576` (1 MiB: the bytes of a plugin tool's result an agent gets; a plugin's own cap goes up with it, such as the database plugin's `limits.max_result`) |
| `GOSIDIAN_MCP_PLUGIN_TOOL_TIMEOUT` | `mcp.plugin_tool_timeout` | `2m` (the longest an agent waits for a plugin's tool, within the plugin's own `timeout`) |
| `GOSIDIAN_MCP_MAX_STEP_OUTPUT_SHOWN` | `mcp.max_step_output_shown` | `4096` (the bytes of a step's output `memory_pipelines` shows; the whole output stays in the run) |
| `GOSIDIAN_MCP_PACKAGE_MAX_FILES` | `mcp.package_max_files` | `500` (files of a `memory_ingest` package) |
| `GOSIDIAN_MCP_PACKAGE_MAX_BYTES` | `mcp.package_max_bytes` | `20971520` (20 MiB, a package unpacked) |
| `GOSIDIAN_MCP_SHRINK_GUARD_PERCENT` | `mcp.shrink_guard_percent` | `10` (`memory_update`, and `memory_ingest` with `overwrite`, refuse a content under this percentage of the note's size unless the call passes `allow_shrink: true`: a placeholder written over a note by mistake; `0` turns the guard off) |
| `GOSIDIAN_MCP_SHRINK_GUARD_MIN_BYTES` | `mcp.shrink_guard_min_bytes` | `1024` (the guard looks only at notes of at least this size) |
| `GOSIDIAN_UPLOAD_QUOTA_BYTES` | `uploads.quota_bytes` | `0` (no limit; bytes of attachments an account may upload per window, its MCP tokens and web uploads together) |
| `GOSIDIAN_UPLOAD_QUOTA_WINDOW` | `uploads.quota_window` | `24h` (the sliding window of the quota) |
| `GOSIDIAN_IN_RESERVE_BYTES` | `in.reserve_bytes` | `5368709120` (5 GiB: the free disk an [upload into IN](in.md#uploading) leaves on the IN folders' filesystem; under it a session, a piece or a zip's next file is refused with 507; `0` turns it off; see [Uploads into IN](#uploads-into-in)) |
| `GOSIDIAN_IN_MAX_IN_BYTES` | `in.max_in_bytes` | `0` (no cap: the bytes of the files on the disk of a project's IN folder, an upload included) |
| `GOSIDIAN_IN_SESSION_TTL` | `in.session_ttl` | `24h` (an upload with no piece for that long goes, with what arrived; a done one is forgotten as long after) |
| `GOSIDIAN_IN_ZIP_MAX_ENTRIES` | `in.zip_max_entries` | `100000` (the files of a zip the server unpacks; `0`: no cap) |
| `GOSIDIAN_IN_ZIP_MAX_BYTES` | `in.zip_max_bytes` | `0` (no cap, the reserve holds: their bytes) |
| `GOSIDIAN_IN_ZIP_MAX_RATIO` | `in.zip_max_ratio` | `200` (their bytes over the zip's, for a zip whose files are over 100 MiB; `0`: no cap) |
| `GOSIDIAN_IN_WEB_MAX_FILE_BYTES` | `in.web.max_file_bytes` | `0` (no cap: a file uploaded from the web UI or the REST API, a zip before it is unpacked) |
| `GOSIDIAN_IN_WEB_MAX_UPLOAD_BYTES` | `in.web.max_upload_bytes` | `0` (no cap: the files of one upload together) |
| `GOSIDIAN_IN_WEB_EXTENSIONS` | `in.web.extensions` | empty (any kind; comma-separated, `pdf,xml`: the only kinds taken, in a zip too, which is taken whatever they say) |
| `GOSIDIAN_IN_MCP_MAX_FILE_BYTES` | `in.mcp.max_file_bytes` | `0` (no cap: a file an agent uploads with `memory_in_upload`) |
| `GOSIDIAN_IN_MCP_MAX_UPLOAD_BYTES` | `in.mcp.max_upload_bytes` | `0` (no cap: the files of one upload of an agent) |
| `GOSIDIAN_IN_MCP_EXTENSIONS` | `in.mcp.extensions` | empty (any kind: the kinds an agent may upload, as for `in.web`) |
| `GOSIDIAN_MCP_OPEN` | `mcp.open` | `false` (`true` lets MCP requests without a token run as admin while no token exists, for local use only; the first token closes it) |
| `GOSIDIAN_MCP_ALLOWED_UPLOAD_ROOTS` | `mcp.allowed_upload_roots` | empty (vault only). A folder that stages files for `source_path` imports inside the vault, a hidden one included, must be listed here or be the bridge dir: the vault itself serves only what the token may read, and no hidden folder ([upload](mcp/upload.md)) |
| `GOSIDIAN_MCP_BRIDGE_DIR` | `mcp.bridge_dir` | empty (off; staging dir for cheap `bridge_filename` uploads, IMP-059). Set inside the vault, it and the upload roots there stay out of git sync: the managed `.gitignore` names them, and a commit that already holds them drops them from the index (the history keeps what was pushed); the log says so at each start |
| `GOSIDIAN_OAUTH_ENABLED` | `oauth.enabled` | `false` (embedded OAuth 2.1 authorization server for claude.ai / ChatGPT / Claude Code logins, see [Authentication](mcp/authentication.md#oauth-21-for-hosted-clients-claudeai-chatgpt-claude-code)) |
| `GOSIDIAN_OAUTH_ISSUER` | `oauth.issuer` | empty (required when enabled: the public HTTPS origin clients use, e.g. `https://notes.example.com`, without a path, which is refused at start; the MCP resource is `<issuer>/mcp`) |
| `GOSIDIAN_OAUTH_ACCESS_TTL` | `oauth.access_ttl` | `1h` (in-memory access tokens) |
| `GOSIDIAN_OAUTH_REFRESH_TTL` | `oauth.refresh_ttl` | `720h` (grant lifetime; refresh tokens rotate on use) |
| `GOSIDIAN_OAUTH_ALLOWED_REDIRECT_HOSTS` | `oauth.allowed_redirect_hosts` | empty (any HTTPS redirect host; loopback always allowed). `oauth.client_max` (`1000`) and `oauth.client_idle_ttl` (`2160h`) bound the registered clients, config file only |
| `GOSIDIAN_MCP_DISABLE_DNS_REBINDING_PROTECTION` | `mcp.disable_dns_rebinding_protection` | `false` (both MCP endpoints, `/mcp` and `/mcp/sse`, answer 403 to a request that arrived on a loopback-bound connection with a non-localhost `Host` header; Docker and LAN listeners never trip it — set `true` only for a same-host reverse proxy that forwards over `127.0.0.1` while keeping the public `Host`, or make the proxy send `Host: localhost`) |
| `GOSIDIAN_INGEST_URL_ALLOWLIST` | `mcp.ingest_url_allowlist` | empty (off; comma-separated absolute `http(s)` URLs the `memory_ingest` `url` source may fetch from — each entry is matched structurally on scheme, host, port and path segment (never as a text prefix, so `https://api.example.com` does not admit `https://api.example.com.evil/`); the allowlist is the SSRF boundary and also gates every redirect hop) |
| `GOSIDIAN_AUTOMATIONS_ENABLED` | `automations.enabled` | `true` (the rules of database notes, [automations](vault/databases.md#automations); a rule acts only where a database note declares it) |
| `GOSIDIAN_AUTOMATIONS_INTERVAL` | `automations.interval` | `5m` (how often the rules run) |
| `GOSIDIAN_AUTOMATIONS_TIMEZONE` | `automations.timezone` | empty (the server's zone, `TZ`; an IANA zone such as `Europe/Rome` for the days and times of the rules) |
| `GOSIDIAN_PIPELINES_ENABLED` | `pipelines.enabled` | `false` (the server runs no [pipelines](pipelines.md); when on, only the projects with `use_pipelines` run theirs) |
| `GOSIDIAN_PIPELINES_WORKERS` | `pipelines.workers` | `2` (steps run at once) |
| `GOSIDIAN_PIPELINES_INTERACTIVE_WORKERS` | `pipelines.interactive_workers` | `1` (workers on top of `workers` that run only the steps of [interactive runs](pipelines.md#interactive-runs), a chat's messages, which the others also take first; `0`: none of their own) |
| `GOSIDIAN_PIPELINES_MAX_SYNC_WAIT` | `pipelines.max_sync_wait` | `60s` (the longest a start [waits](pipelines.md#starting-waiting-resuming) for its run's answer, over HTTP or `memory_pipeline_run`) |
| `GOSIDIAN_PIPELINES_RUN_TOKEN_RATE` | `pipelines.run_token_rate` | `60` (the runs a run token starts in a minute, and the resumes of a run's key; a chat's widget starts one per message of everyone who chats) |
| `GOSIDIAN_PIPELINES_STEP_TIMEOUT` | `pipelines.step_timeout` | `15m` (the longest a step may run) |
| `GOSIDIAN_PIPELINES_RETENTION` | `pipelines.retention` | `720h` (runs kept in `<state-dir>/jobs.db` after they end; `0` keeps them) |
| `GOSIDIAN_PIPELINES_MAX_WAIT` | `pipelines.max_wait` | `168h` (the longest a `wait.call` may wait) |
| `GOSIDIAN_PIPELINES_MAX_LLM_TOKENS` | `pipelines.max_llm_tokens` | `0` (no cap on the LLM tokens of one run) |
| `GOSIDIAN_PIPELINES_MAX_INPUT_BYTES` | `pipelines.max_input_bytes` | `262144` (the bytes of a run's input) |
| `GOSIDIAN_PIPELINES_MAX_OUTPUT_BYTES` | `pipelines.max_output_bytes` | `1048576` (the bytes of a step's output, and of the text files one result of a plugin gives back in place of attachments; raise it for steps that keep many results, and a plugin's own cap with it, such as the database plugin's `limits.max_result`) |
| `GOSIDIAN_PIPELINES_MAX_RUN_DEPTH` | `pipelines.max_run_depth` | `4` (how many runs may start each other in a chain through `pipeline.run`: a pipeline that starts itself stops there) |
| `GOSIDIAN_PIPELINES_MAX_RUN_CHILDREN` | `pipelines.max_run_children` | `1000` (how many runs one run may start through `pipeline.run`, in an `each` too) |
| `GOSIDIAN_PIPELINES_MAX_LIST_ITEMS` | `pipelines.max_list_items` | `5000` (what a node lists or is asked for in one call: a page of `note.list`, the `only` of `note.sections`) |
| `GOSIDIAN_PIPELINES_MAX_CHUNK_META` | `pipelines.max_chunk_meta` | `16384` (the bytes of a chunk's `meta` from `note.sections`; the database plugin's `limits.max_meta` goes up with it) |
| `GOSIDIAN_PIPELINES_MAX_MARK_SETS`, `_MAX_MARKS`, `_MAX_MARK_LABEL`, `_MAX_MARK_TEXT` | `pipelines.max_mark_sets`, `max_marks`, `max_mark_label`, `max_mark_text` | `20`, `500`, `60`, `500` (the sets of marks of a note, the marks of a set, and the characters of a mark's label and text: [note.mark](pipelines.md#marks-on-a-note)) |
| `GOSIDIAN_PIPELINES_MAX_PLUGIN_FILES` | `pipelines.max_plugin_files` | `100` (the files one call of a [plugin](plugins.md) names, a node's or an agent's tool; a plugin's own cap goes up with it, such as `IMAGES_MAX_FILES`) |
| `GOSIDIAN_PIPELINES_MAX_PLUGIN_FILES_OUT` | `pipelines.max_plugin_files_out` | `200` (the files one result of a plugin gives back) |
| `GOSIDIAN_PIPELINES_MAX_PLUGIN_FILES_BYTES` | `pipelines.max_plugin_files_bytes` | `104857600` (100 MiB: the bytes of the files one result gives back) |
| `GOSIDIAN_PIPELINES_MAX_PLUGIN_GRAPH_BYTES` | `pipelines.max_plugin_graph_bytes` | `8388608` (8 MiB: the bytes of a plugin's graph) |
| `GOSIDIAN_PIPELINES_STARLARK_STEPS` | `pipelines.starlark_steps` | `10000000` (the steps of a `starlark` script) |
| `GOSIDIAN_PIPELINES_HTTP_ALLOWLIST` | `pipelines.http_allowlist` | empty (comma-separated URL prefixes `http.request` and the callbacks may call; empty: none) |
| `GOSIDIAN_PIPELINES_PUBLIC_URL` | `pipelines.public_url` | the `[oauth]` issuer (the server's address in `{{ run.resume_url }}`; empty: a path) |
| `GOSIDIAN_PIPELINES_FILES_URL` | `pipelines.files_url` | the public URL (the server's address as the [plugins](plugins.md#files-by-reference) reach it, for the files they fetch by reference: `http://gosidian:8080` on a compose network; a plugin's own `files_url` wins) |
| `GOSIDIAN_PIPELINES_MCP_URL` | `pipelines.mcp_url` | `files_url` (else the public URL) with `/mcp`: gosidian's MCP endpoint as the plugins reach it, for the nodes that ask for it with a token of the call ([plugins](plugins.md#gosidians-mcp-for-a-call)); a plugin's own `mcp_url` wins |
| `GOSIDIAN_PIPELINES_TIMEZONE` | `pipelines.timezone` | the automations' zone, else `TZ` (the days and times of the `schedule` triggers and the days of the `<pipeline>-runs.md` lines) |
| `GOSIDIAN_PIPELINES_LLM_URL`, `_LLM_MODEL`, `_LLM_KEY_SECRET`, `_LLM_KEY_ENV` | `[[pipelines.llm]]` | none (one OpenAI-compatible provider named `default`: its URL up to `/v1`, its model, and its key, as the name of a secret or the name of the variable that holds it (`api_key_env` in the file; an empty variable is no key); more providers in the file) |
| `GOSIDIAN_PIPELINES_PLUGINS` | `[[pipelines.plugins]]` | none (comma-separated `name=url` entries: [plugins](plugins.md) whose MCP endpoint is the url, without a token; an entry replaces the url of the file's plugin of the same name) |
| `GOSIDIAN_PLUGIN_<NAME>_URL`, `_TOKEN`, `_ACCEPT` | `url`, `token_env`, `accept_sha` of `[[pipelines.plugins]]` | none (a plugin registered from its own variables, `<NAME>` in upper case with `_` for `-`: its MCP endpoint, its bearer token in place of a `token_secret`, and the manifest the installer expects, which the server then accepts by itself: [plugins](plugins.md#registering-a-plugin)) |
| `GOSIDIAN_PLUGIN_<NAME>_MAX_CONCURRENT`, `_RESOURCES` | `max_concurrent`, `resources` of `[[pipelines.plugins]]` | its manifest's suggestion, else no limit; none (the plugin's calls at once, the steps' and the agents' together, and the shared resources it takes a place in: [calls at once](pipelines.md#calls-at-once)) |
| `GOSIDIAN_PIPELINES_RESOURCES` | `[[pipelines.resources]]` | none (comma-separated `name=calls`: resources several plugins or providers share, such as one GPU) |
| `GOSIDIAN_PIPELINES_LLM_MAX_CONCURRENT`, `_LLM_RESOURCES` | `max_concurrent`, `resources` of `[[pipelines.llm]]` | no limit; none (the provider `default`'s calls at once from the steps, and its shared resources) |
| `GOSIDIAN_AGENTS_MAX_RUNNING` | `agents.max_running` | `0` (no cap: the agents of all the teams at work at once, on the tasks of every project; see [Agent teams](#agent-teams)) |
| `GOSIDIAN_AGENTS_LEASE` | `agents.lease` | `15m` (how long a task an outside agent takes stays its own, each heartbeat renewing it; from `1m` to `24h`) |
| `GOSIDIAN_AGENTS_MAX_ATTEMPTS` | `agents.max_attempts` | `3` (the attempts after which a task goes failed; from 1 to 20) |
| `GOSIDIAN_AGENTS_MAX_TASKS` | `agents.max_tasks` | `50` (the most sub-tasks an objective may have; from 1 to 1000) |
| `GOSIDIAN_WEB_GRAPH_DEFAULT_NODES` | `web.graph_default_nodes` | `2000` (the most linked nodes a graph asked without a limit keeps; at most `graph_max_nodes`) |
| `GOSIDIAN_WEB_GRAPH_MAX_NODES` | `web.graph_max_nodes` | `5000` (the most nodes a graph, a `graph` block or a plugin's graph may have) |
| `GOSIDIAN_WEB_GRAPH_MAX_EDGES` | `web.graph_max_edges` | `50000` (the most edges of a plugin's graph) |
| `GOSIDIAN_WEB_CUT_MAX_CHUNKS` | `web.cut_max_chunks` | `5000` (the chunks of a note the [cut view](web-ui/overview.md#the-cut-view) reads from an archive) |
| `GOSIDIAN_WEB_PLUGIN_TIMEOUT` | `web.plugin_timeout` | `2m` (the longest the web UI waits for a plugin, a graph or the cut view, within the plugin's own `timeout`) |
| `GOSIDIAN_SECRETS_KEY` | — | empty (32 bytes in base64, `openssl rand -base64 32`: the key of `<state-dir>/secrets.json`, the secrets the pipelines use; without it `gosidian secret` refuses and the nodes that need a secret fail) |
| `GOSIDIAN_TRASH_ENABLED` | `trash.enabled` | `false` |
| `GOSIDIAN_TRASH_RETENTION` | `trash.retention` | `720h`; `0` keeps entries until purged |
| `GOSIDIAN_LOGIN_SESSION_TTL` | `webauth.session_ttl` | `24h` |
| `GOSIDIAN_LOGIN_WINDOW` | `webauth.login_window` | `15m` |
| `GOSIDIAN_LOGIN_MAX_FAILURES` | `webauth.login_max_failures` | `5` |
| `GOSIDIAN_TRUSTED_PROXIES` | `webauth.trusted_proxies` | empty (comma-separated IPs/CIDRs of the reverse proxies whose `X-Forwarded-For` the login rate limiter may trust; empty = the header is ignored and the peer address is used) |
| `GOSIDIAN_TOTP_MODE` | `webauth.totp_mode` | `off` (`optional`, `required`; any case, `require` too; another value reads `required`, with a line in the log) |
| `GOSIDIAN_AUTO_OWNER` | `webauth.auto_owner` | `true` (at the first start, with no account at all, create the owner `admin` with a random password, shown once in the log and kept in `<state-dir>/initial-admin-password` until the owner chooses its own at the first sign-in; `false` leaves the owner to `gosidian user setup`) |
| `GOSIDIAN_LDAP_ENABLED` | `ldap.enabled` | `false` |
| `GOSIDIAN_LDAP_URL` | `ldap.url` | empty (`ldap://host:389`, `ldaps://host:636`) |
| `GOSIDIAN_LDAP_START_TLS` | `ldap.start_tls` | `false` |
| `GOSIDIAN_LDAP_INSECURE_SKIP_VERIFY` | `ldap.insecure_skip_verify` | `false` (dev/self-signed only) |
| `GOSIDIAN_LDAP_BIND_DN` | `ldap.bind_dn` | empty (anonymous search) |
| `GOSIDIAN_LDAP_BIND_PASSWORD` | `ldap.bind_password` | empty |
| `GOSIDIAN_LDAP_USER_BASE_DN` | `ldap.user_base_dn` | empty |
| `GOSIDIAN_LDAP_USER_FILTER` | `ldap.user_filter` | `(uid=%s)` (AD: `(sAMAccountName=%s)`) |
| `GOSIDIAN_VAULT_CACHE_SIZE` | `vault.cache_size` | `128` (`0` disables) |
| `GOSIDIAN_VAULT_HTML_NOTES` | `vault.html_notes` | `false` (treat single-file `.html` as first-class notes; ADR-011, sandboxed iframe) |
| `GOSIDIAN_VAULT_MEDIA_NOTES` | `vault.media_notes` | `false` (resolve image media notes: `.md` with `type: image` + `media:`; ADR-013) |
| `GOSIDIAN_VAULT_TABLE_NOTES` | `vault.table_notes` | `false` (resolve CSV table notes: `.md` with `type: table` + `media:`; ADR-016) |
| `GOSIDIAN_VAULT_DURABILITY` | `vault.durability` | `batch` (when writes reach the disk: `batch`, `always` or `none`; see [Durability](#durability)) |
| `GOSIDIAN_I18N_DEFAULT_LANG` | `i18n.default_lang` | `en` (the language a browser starts in; must be among the enabled ones) |
| `GOSIDIAN_I18N_ENABLED_LANGS` | `i18n.enabled_langs` | every language with a catalogue (`it`, `en`, `es`, `fr`, `de`); comma-separated, the languages the web UI's selector offers |
| `GOSIDIAN_SELF_IMPROVE_ENABLED` | `self_improve.enabled` | `false` |
| `GOSIDIAN_SELF_IMPROVE_TARGET_PROJECT` | `self_improve.target_project` | `insights` |
| `GOSIDIAN_SELF_IMPROVE_EVERY_N_CALLS` | `self_improve.every_n_calls` | `25` |
| `GOSIDIAN_SELF_IMPROVE_COOLDOWN_MINUTES` | `self_improve.cooldown_minutes` | `120` |
| `GOSIDIAN_SELF_IMPROVE_MAX_NUDGES_PER_SESSION` | `self_improve.max_nudges_per_session` | `1` |
| `GOSIDIAN_SELF_IMPROVE_NOTIFY_EMAIL` | `self_improve.notify_email` | empty (scheduled digest off) |
| `GOSIDIAN_GLOBAL_ENABLED` | `global.enabled` | `false` |
| `GOSIDIAN_GLOBAL_PUBLIC_PROJECT` | `global.public_project` | `global` |
| `GOSIDIAN_GLOBAL_PRIVATE_PROJECT` | `global.private_project` | `global-private` |
| `GOSIDIAN_ANCHORS_ENABLED` | `agent_anchors.enabled` | `false` (master switch for [agent anchors](mcp/agent-anchors.md); per-project opt-in via the `use_anchors` flag) |
| — | `lint.hot_oversize_bytes` | `8192` (threshold for the `hot-oversize` lint rule and the bootstrap's `maintenance.hot_oversize`) |

The limits above are the defaults the code ships with; each is a setting
because a vault larger than the ones they were measured on needs more
(IMP-190). A setting a plugin mirrors goes up with the plugin's own: a
cap raised on one side only still stops at the other.

The `config.toml` at `<state-dir>/config.toml` holds the
persistent form of the same settings and is edited from the web UI at
`/settings`. Env vars override the file on every start. The settings
page and `GET /api/v1/settings` show the effective values (file + env +
defaults) and list the env-set fields in `env_overrides`: those are
read-only there, and a `PUT` that changes one is refused.

> **Secrets**: prefer `GOSIDIAN_LDAP_BIND_PASSWORD` (env / Docker secret)
> over writing `ldap.bind_password` into `config.toml` — the `/settings`
> UI never reads back the bind password. See
> [Authentication & roles](web-ui/authentication.md) for the full TOTP and
> LDAP setup, including Active Directory.

## Durability

A note or an attachment is written whole: the bytes go to a temporary
file in the same folder, which is renamed over the target, so a reader
never finds half of it. `vault.durability`
(`GOSIDIAN_VAULT_DURABILITY`) says when the bytes are also synced to the
disk, which is what a power loss cannot take back:

- **`batch`** (the default): a write on its own (MCP, the web UI, the
  API) is synced before it returns; the writes of a pipeline step are
  synced together when the step ends, with one `syncfs` for each
  filesystem they went to, before the step counts as done. A power loss
  in the middle of a step can lose what it wrote, or leave it empty, and
  the step runs again (an `in.import` imports the same documents again). One sync per note
  was 39% of an import's time in the lab. Where `syncfs` is not available
  (not Linux) a step's writes are synced one by one, as with `always`;
- **`always`**: every write is synced before it returns, a pipeline's
  too;
- **`none`**: nothing is synced; the system writes the files back when
  it likes (on Linux within about 30 seconds), and a power loss can take
  the last writes, or leave them empty.

The index is rebuilt from the notes, so it needs no sync of its own: the
boot scan re-indexes what changed. The log says the setting at start:
`vault durability: batch`.

## Uploads into IN

The web UI and the REST API upload files into a project's IN folder in
pieces, with pauses and resumes ([Uploading](in.md#uploading)). Nothing
caps them by default but the free disk the server keeps: an upload that
would take the IN folders' filesystem under `reserve_bytes` is refused
when it opens, and so is each piece, and each file of a zip as it is
unpacked. The caps are the installer's to set, by way in (`[in.web]` for
the web UI and the REST API, `[in.mcp]` for the agents' `memory_in_upload`):

```toml
[in]
reserve_bytes = 10737418240   # keep 10 GiB free
max_in_bytes = 0              # a project's IN folder, no cap
session_ttl = "24h"
zip_max_entries = 100000
zip_max_bytes = 0
zip_max_ratio = 200

[in.web]
max_file_bytes = 4294967296   # 4 GiB a file
max_upload_bytes = 0
extensions = ["pdf", "xml", "zip"]

[in.mcp]
max_upload_bytes = 21474836480  # 20 GiB an upload of an agent
```

A piece is at most 64 MiB, under the 100 MB a request may carry through
Cloudflare; a proxy in front of gosidian must let that through
(`client_max_body_size 64m` for nginx; nginx-proxy-manager's default is
larger). The pieces go to `<project>/IN/.uploads/<id>/`, on the IN
folder's own filesystem, so that a file that arrived takes its place with
a rename even when IN is a volume of its own.

## Agent teams

The tasks of a project are notes its agent teams work on: a pipeline the
`on: task` trigger starts, or an outside agent that takes the task. Four
limits of the installation hold for every project:

```toml
[agents]
max_running = 0    # the agents at work at once, all the teams together; 0: no cap
lease = "15m"      # a task an outside agent takes, renewed by its heartbeat
max_attempts = 3   # then the task goes failed
max_tasks = 50     # the sub-tasks of an objective
```

A value out of range (a negative `max_running`, a `lease` under `1m` or
over `24h`, `max_attempts` outside 1 to 20, `max_tasks` outside 1 to
1000) stops the server at start, with a line that names the setting.

## Git maintenance

With git sync on, git packs the vault's repository from time to time: it
gathers the objects of the last commits into packs and merges small packs
into bigger ones. Left to itself, git does it right after a commit, in the
middle of the writes, and on a vault full of images that took a core at
100% for about 20 seconds after an import.

- **`git.maintenance`** (`GOSIDIAN_GIT_MAINTENANCE`) is a time of day,
  `03:30` by default, in the server's time zone ([Time zone](#time-zone)).
  gosidian turns git's own maintenance off in the repository
  (`gc.auto = 0`, `maintenance.auto = false`) and runs `git gc` every day
  at that time, while no commit is running. Until then the objects of the
  day's commits stay loose, which costs some disk and nothing else. The
  log says it at start: `gitsync: maintenance every day at 03:30
  (Europe/Rome)`, and `gitsync: maintenance done in …` after each run.
- **`auto`** leaves the maintenance to git, as before; gosidian takes back
  the two settings above if it wrote them, and keeps them if they are
  yours.
- **`off`** turns git's maintenance off and runs none: run `git gc` in the
  vault yourself.

**No delta search on compressed formats.** At start gosidian also writes a
managed block into the repository's `info/attributes` with `-delta` on the
attachment formats that are already compressed: png, jpg, jpeg, gif, webp,
pdf, zip, docx and xlsx, in any case. A delta between two images saves next
to nothing, and looking for one made every pack cost seconds of CPU: on a
copy of a 1.1 GB repository, 95% PNG, a repack went from 21.5 to 6.4 s of
CPU, the full one from 60.8 to 21.0 s, the pack 0.1% larger. The notes,
svg, csv, json and txt keep their deltas. The file is the repository's own,
not the vault's: it is not committed and does not reach the remote. Lines
outside the markers are kept, so you can add formats of your own (a video,
`*.mp4 -delta`).

## Time zone

The server's time zone is the standard `TZ` variable (`TZ=Europe/Rome`
in the compose file's `environment`). The binary carries the zone
database, so an image without `/usr/share/zoneinfo` takes it too. Unset,
the zone is the system's (`/etc/localtime`): UTC in a container that does
not mount the host's, and then what follows is what gosidian always
wrote. The log says it at start: `note times: CEST +02:00
(TZ="Europe/Rome")`.

**What follows it** is what gosidian writes into notes and their names,
times as RFC 3339 with the offset (`2026-10-20T22:04:05+02:00`; `Z` in
UTC) and dates as the day there:

- handoffs: the name `YYYYMMDD-HHMMSS-<slug>`, and `created`,
  `claimed_at` and `completed_at`;
- the dates of the notes the server writes: `{{TODAY}}` in templates
  (project scaffolds, database rows, the agent stub), `memory_ask`'s
  questions and `updated`, insights and their digest, the archive line of
  `memory_compact`, the `described` of image notes;
- snapshots: their name, `date` and the line that says when, with the
  zone's name (`CEST`);
- views: their `today` and the modification date they show;
- pipelines: `{{ run.created }}` and a schedule's `{{ trigger.slot }}`;
- the names of downloaded zips.

**What stays in UTC** is the server's own records and the answers about
them: the audit log, tokens, accounts, invites, tickets, the trash, the
runs, `modified` in `memory_query` and the API, the message of the
automatic commits. The keys that make a schedule's slot fire once are in
UTC too, so changing `TZ` fires no slot a second time. Notes written
before keep their names and times.

**Automations and pipelines** take `TZ` unless they have a zone of their
own: `[automations] timezone` sets the days and times of the rules,
`[pipelines] timezone` (by default the automations' zone) those of the
`schedule` triggers and the days of the `<pipeline>-runs.md` lines. The
notes a rule writes, a handoff or a snapshot, are named and dated in
`TZ` like any other.

**Comparisons**: `memory_query`, views and rollups compare and sort a
date-time by its instant, whatever offset it was written with:
`2026-10-20T22:04:05+02:00` comes before `2026-10-20T20:30:00Z`. A
date-time without an offset, and a date, are in `TZ`. A date alone in a
condition (`updated >= 2026-10-01`, `today`) compares with the day a
value writes.

## Lint vocabulary extension

The `frontmatter-tag-unknown` lint rule (run via the `memory_lint`
MCP tool) checks every note's frontmatter `tags` against a closed
built-in vocabulary: the `type:` / `topic:` / `status:` namespaces,
the bare `pinned` tag, and the project name itself. Tags outside
that vocabulary are reported as warnings — typos surface immediately,
and a vault gradually accumulates a coherent metadata language.

Some vaults legitimately use additional tags that are part of their
own structural conventions. To keep those tags warning-free without
weakening the rule for everyone, add them under
`[lint.frontmatter_tag_vocabulary]` in `<state-dir>/config.toml`:

```toml
[lint.frontmatter_tag_vocabulary]
extra_allowed = [
  "status:reference",       # for reference notes that aren't snapshot/draft/done/archived
  "topic:agent-template",   # for an agent-template index folder
  "topic:bootstrap",        # bootstrap kit notes
]
```

Behaviour:

- **Additive only**. Built-in tags are always allowed; the entries
  here are merged on top.
- **Format**: each entry is either a bare token (e.g. `mytag`), a
  `<namespace>:<value>` pair where both halves are non-empty, or a
  namespace wildcard `<namespace>:*` accepting any well-formed value
  in that namespace (e.g. `cm:*`). Malformed entries (empty,
  leading/trailing colon, internal whitespace) are skipped silently —
  a typo in the config doesn't crash the lint.
- **Per-vault scope**: the file is loaded at server startup; the
  lint runner passes the list to the rule. Restart gosidian after
  editing.
- **No file = no change**: vaults without `[lint]` keep the
  baseline behaviour identically.

Note: the `topic:` namespace is **open** — any well-formed value
(`topic:<area>`, no whitespace or extra colons) is accepted without
configuration. `type:` and `status:` stay closed: they are lifecycle
vocabularies the tooling itself depends on.

### Per-project vocabulary (declared in the vault)

The config entries above are instance-wide. A single project can
instead declare its own domain taxonomy **inside the vault**, where
agents can maintain it with regular note edits:

1. Enable the project's `use_tag_vocabulary` flag (Projects view in
   the web UI, or `use_tag_vocabulary` via `PUT /api/v1/projects/{name}` —
   persisted in `<state-dir>/projects.json`). Default off: the
   declaration below is inert without it.
2. Declare the extra vocabulary in the frontmatter of
   `<project>/memory/conventions.md`, field `tag_vocabulary:`:

   ```yaml
   ---
   title: Conventions
   tags: [myproject, type:memory]
   tag_vocabulary:
     - "cm:*"        # domain taxonomy namespace (wildcard)
     - anagrafica    # exact extra tag
   ---
   ```

Behaviour:

- Same entry format as `extra_allowed` (bare, `ns:value`, `ns:*`),
  capped at 64 entries — entries past the cap are ignored.
- Scope is the declaring project only; the instance-wide
  `extra_allowed` still applies everywhere, additively.
- No restart needed: the declaration is read at each `memory_lint` run.
- `memory_bootstrap` surfaces the effective declaration in a
  `tag_vocabulary` block, so the extension stays visible at session
  start instead of acting silently. Document *why* each namespace
  exists in the body of `conventions.md`.

> **Opt-in rule**: `unlinked-mentions` flags prose that names another
> note's title/basename without linking to it. It is **advisory**
> (`info` severity) and **not** in the default rule set — it is known
> and selectable by name only, because it is higher-noise on a dense
> vault. Request it explicitly when running `memory_lint`. It leaves
> out what is noise by design: index notes (`type: index` — README,
> `hot.md`, `log.md`) are not scanned, a title or file name that two or
> more notes share (`README`) is no mention, and a database's
> `template` is neither scanned nor a target.

> **Per-note opt-out**: a note turns a rule off for itself with
> `lint_disable` in its frontmatter, as a list or as names separated by
> commas — `lint_disable: [unlinked-mentions]`,
> `lint_disable: orphan-note, unlinked-mentions`. Its issues for those
> rules are dropped; every other note is checked as before.

## CLI reference

```
gosidian [flags]                         # start the server
gosidian token create --vault <path> --name <s> [flags]
gosidian token list   --vault <path>
gosidian token revoke --vault <path> --id <8hex>
gosidian user setup   --vault <path> --username <s> [--totp] [--replace]
gosidian user add     --vault <path> --username <s> [--role member|guest]
gosidian user disable --vault <path> (--username <s> | --all)
gosidian user list    --vault <path>
gosidian healthcheck  [--addr <host:port>]
gosidian import-vault --vault <path> [flags]
gosidian secret set <name>               # the value on standard input
gosidian secret list | delete <name>
gosidian pipeline token create --pipeline <path.md> --name <service>
gosidian pipeline token list [--pipeline <path.md>] | revoke <id>
gosidian plugin list | show <name>       # the plugins and the manifest one serves
gosidian plugin accept <name> <sha>      # let its nodes run (docs/plugins.md)
gosidian plugin forget <name>
gosidian plugin bind <plugin> <source> <project> [--as <folder>] [--write] [--every 1m]
gosidian plugin unbind <project> <folder> # a source of a plugin as a folder of the IN folder (docs/in.md)
gosidian version                         # print the build version (also --version)
```

Run `gosidian token -h` etc. for per-subcommand options.

## State directory

`<state-dir>` is where gosidian keeps its machine-owned files: `auth.json`
(web accounts), `tokens.json` and `spa_tokens.json` (hashed tokens),
`gitsync.json`, `config.toml`, `projects.json`, `audit.jsonl` and the
SQLite index, `jobs.db` (the job runner's runs: the [pipelines](pipelines.md)' and the
automations' actions, with either on),
`secrets.json` (sealed under `GOSIDIAN_SECRETS_KEY`) and `plugins.json`
(the accepted plugin manifests), and, until the owner of the first start has chosen its own
password, `initial-admin-password`. By default it is `<vault>/.gosidian/`, unchanged from earlier
releases. Set `--state-dir` (or `GOSIDIAN_STATE_DIR`) to move it **out of
the vault root** — the recommended layout: nothing that resolves inside the
vault can reach the credential store, and the vault's git repository never
contains it, by construction rather than by `.gitignore`.

A state dir set inside the vault is accepted and hidden like a dot folder,
whatever its name: no note, project, attachment or zip entry comes from it,
no project operation moves or removes it or a folder holding it, and the
managed `.gitignore` names it (a commit that already holds it drops it from
the index, though the history keeps it: rotate the tokens if it was
pushed). The log says so at every start. The vault root itself is refused.

On the first start with a custom state dir, the known files are moved from
`<vault>/.gosidian/` into it (rename, or copy+remove across filesystems);
files already present in the destination are left untouched and logged;
a `STATE-MOVED-TO` marker is written in the old location. `templates/` and
`trash/` are vault content and stay under `<vault>/.gosidian/`. `--db`
relocates the index alone. The `user` and `token` subcommands honour the
same flag and variable. In Docker, mount a volume on `/data` and set
`GOSIDIAN_STATE_DIR=/data`.
