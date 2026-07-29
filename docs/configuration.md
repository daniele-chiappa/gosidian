# Configuration

Settings come from four sources, in decreasing precedence:

1. **CLI flags** (`--vault`, `--addr`, `--db`, `--mcp-addr`)
2. **Environment variables** (`GOSIDIAN_*`)
3. **`<vault>/.gosidian/config.toml`** (edited from `/settings` UI)
4. **Built-in defaults**

## Environment variables

| Variable | Config field | Default |
|---|---|---|
| `GOSIDIAN_VAULT` | — (CLI only `--vault`) | *required* |
| `GOSIDIAN_ADDR` | — (CLI `--addr`) | `:8080` |
| `GOSIDIAN_DB` | — (CLI `--db`) | `<vault>/.gosidian/index.db` |
| `GOSIDIAN_MCP_ADDR` | — (CLI `--mcp-addr`) | empty (legacy listener disabled; MCP is always at `/mcp/sse` on the web port) |
| `GOSIDIAN_LOG_LEVEL` | — | `info` (`debug`, `warn`, `error`) |
| `GOSIDIAN_LOG_FORMAT` | — | `text` (`json`) |
| `GOSIDIAN_GIT_ENABLED` | `git.enabled` | `false` |
| `GOSIDIAN_GIT_REMOTE` | `git.remote` | empty |
| `GOSIDIAN_GIT_BRANCH` | `git.branch` | `main` |
| `GOSIDIAN_GIT_AUTHOR_NAME` | `git.author_name` | `Gosidian` |
| `GOSIDIAN_GIT_AUTHOR_EMAIL` | `git.author_email` | `gosidian@localhost` |
| `GOSIDIAN_GIT_DEBOUNCE` | `git.commit_debounce` | `30s` |
| `GOSIDIAN_GIT_PUSH` | `git.push` | `false` |
| `GOSIDIAN_GIT_TOKEN_ENV` | `git.token_env` | empty |
| `GOSIDIAN_MCP_WRITE_PER_MINUTE` | `mcp.write_per_minute` | `60` |
| `GOSIDIAN_MCP_MAX_NOTE_BYTES` | `mcp.max_note_bytes` | `1048576` (1 MiB) |
| `GOSIDIAN_MCP_ALLOWED_UPLOAD_ROOTS` | `mcp.allowed_upload_roots` | empty (vault only) |
| `GOSIDIAN_MCP_BRIDGE_DIR` | `mcp.bridge_dir` | empty (off; staging dir for cheap `bridge_filename` uploads, IMP-059) |
| `GOSIDIAN_INGEST_URL_ALLOWLIST` | `mcp.ingest_url_allowlist` | empty (off; comma-separated URL prefixes the `memory_ingest` `url` source may fetch from — the allowlist is the SSRF boundary and also gates every redirect hop) |
| `GOSIDIAN_TRASH_ENABLED` | `trash.enabled` | `false` |
| `GOSIDIAN_TRASH_RETENTION` | `trash.retention` | `720h` |
| `GOSIDIAN_THEME_PRESET` | `theme.preset` | `midnight-luxury` |
| `GOSIDIAN_THEME_DEEP_SPACE` | `theme.deep_space` | `#0B0C10` |
| `GOSIDIAN_THEME_GUNMETAL` | `theme.gunmetal` | `#1F2833` |
| `GOSIDIAN_THEME_SILVER_MIST` | `theme.silver_mist` | `#C5C6C7` |
| `GOSIDIAN_THEME_ELECTRIC_BLUE` | `theme.electric_blue` | `#66FCF1` |
| `GOSIDIAN_THEME_GOLD_LEAF` | `theme.gold_leaf` | `#C5A021` |
| `GOSIDIAN_LOGIN_SESSION_TTL` | `webauth.session_ttl` | `24h` |
| `GOSIDIAN_LOGIN_WINDOW` | `webauth.login_window` | `15m` |
| `GOSIDIAN_LOGIN_MAX_FAILURES` | `webauth.login_max_failures` | `5` |
| `GOSIDIAN_TOTP_MODE` | `webauth.totp_mode` | `off` (`optional`, `required`) |
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
| `GOSIDIAN_I18N_DEFAULT_LANG` | `i18n.default_lang` | `en` |
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
| — | `lint.hot_oversize_bytes` | `16384` (threshold for the `hot-oversize` lint rule) |

The `config.toml` at `<vault>/.gosidian/config.toml` holds the
persistent form of the same settings and is edited from the web UI at
`/settings`. Env vars override the file on every start.

> **Secrets**: prefer `GOSIDIAN_LDAP_BIND_PASSWORD` (env / Docker secret)
> over writing `ldap.bind_password` into `config.toml` — the `/settings`
> UI never reads back the bind password. See
> [Authentication & roles](web-ui/authentication.md) for the full TOTP and
> LDAP setup, including Active Directory.

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
`[lint.frontmatter_tag_vocabulary]` in `<vault>/.gosidian/config.toml`:

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
   persisted in `<vault>/.gosidian/projects.json`). Default off: the
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
> vault. Request it explicitly when running `memory_lint`.

## CLI reference

```
gosidian [flags]                         # start the server
gosidian token create --vault <path> --name <s> [flags]
gosidian token list   --vault <path>
gosidian token revoke --vault <path> --id <8hex>
gosidian user setup   --vault <path> --username <s>
gosidian healthcheck  [--addr <host:port>]
gosidian import-vault --vault <path> [flags]
```

Run `gosidian token -h` etc. for per-subcommand options.
