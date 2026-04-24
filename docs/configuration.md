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
| `GOSIDIAN_MCP_ADDR` | — (CLI `--mcp-addr`) | empty (MCP disabled) |
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
| `GOSIDIAN_VAULT_CACHE_SIZE` | `vault.cache_size` | `128` (`0` disables) |
| `GOSIDIAN_I18N_DEFAULT_LANG` | `i18n.default_lang` | `en` |

The `config.toml` at `<vault>/.gosidian/config.toml` holds the
persistent form of the same settings and is edited from the web UI at
`/settings`. Env vars override the file on every start.

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
