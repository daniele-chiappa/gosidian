# Deployment

Options for running gosidian in production: Docker Compose as the
recommended starting point, reverse proxy for TLS, backup strategy
for disaster recovery.

## Docker Compose

Two shapes, depending on whether you build the image yourself or pull
a published one from GHCR:

- **Build from source** (development host, fork with custom patches) —
  see the `docker-compose.yml` at the repository root, which uses
  `build: { context: ./src }` to compile from the local checkout.
- **Pull from registry** (staging, production, second machine) — use
  the [examples/docker-compose.image.yml](examples/docker-compose.image.yml)
  template: anonymous pull from
  `ghcr.io/daniele-chiappa/gosidian`, no `docker login` needed for
  public images, no Go toolchain required on the host.

The minimal compose for the pull-from-registry case:

```yaml
services:
  gosidian:
    image: ghcr.io/daniele-chiappa/gosidian:latest
    restart: unless-stopped
    volumes:
      - ./vault:/vault
    environment:
      GOSIDIAN_VAULT: /vault
      GOSIDIAN_ADDR: ":8080"
      # MCP is mounted on the web port: /mcp (Streamable HTTP) and
      # /mcp/sse (legacy HTTP+SSE). Enable a legacy standalone SSE
      # listener only for clients pinned to the old separate port:
      # GOSIDIAN_MCP_ADDR: "0.0.0.0:8765"
      # optional: git sync
      # GOSIDIAN_GIT_ENABLED: "true"
      # GOSIDIAN_GIT_REMOTE: "https://your.git.host/you/vault.git"
      # GOSIDIAN_GIT_PUSH: "true"
      # GOSIDIAN_GIT_TOKEN_ENV: "GIT_TOKEN"
      # GIT_TOKEN: "${GIT_TOKEN}"
      # optional: i18n default language
      # GOSIDIAN_I18N_DEFAULT_LANG: "en"
    ports:
      - "8080:8080"
      # Map the legacy MCP port only if GOSIDIAN_MCP_ADDR is set:
      # - "8765:8765"
```

The example files also harden the container: `read_only: true` with a
`tmpfs` on `/tmp`, `cap_drop: [ALL]` and `no-new-privileges`. The image
runs as uid 65532 and writes only to `/vault`, `/data` and `/tmp`, so
nothing else needs to be writable.

Pin a specific version (`:vX.Y.Z`) for production rather than
`:latest` to make rollbacks deterministic. Available tags:
[ghcr.io/daniele-chiappa/gosidian](https://github.com/daniele-chiappa/gosidian/pkgs/container/gosidian).

### Bring-up commands

```bash
mkdir -p ./vault
docker compose -f docker-compose.image.yml pull
docker compose -f docker-compose.image.yml up -d
docker compose -f docker-compose.image.yml logs -f gosidian
```

Smoke test once it's up:

```bash
curl -s   http://localhost:8080/healthz                 # → ok
curl -sI  http://localhost:8080/mcp/sse | head -1        # → 401 (auth required, route mounted)
curl -s -o /dev/null -w "%{http_code}\n" -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" -d '{}'         # → 401 (Streamable HTTP mounted)
```

The first start creates the owner `admin` with a random password, shown
once in the log (`docker compose logs gosidian | grep "owner account
created"`) and kept in `<state-dir>/initial-admin-password`. Open
`http://localhost:8080`, sign in with it and choose your own (see
[Authentication](mcp/authentication.md#web-ui-login)); the file goes away
then. Then create an MCP token from `/admin/tokens` and wire your client —
[Client setup](mcp/client-setup.md) covers Claude Code, Zed, Cursor.

## Reverse proxy + TLS

gosidian itself speaks plaintext HTTP. For anything beyond localhost,
put a reverse proxy (Caddy, Traefik, nginx) in front and let it
terminate TLS.

Minimal Caddyfile example:

```
notes.example.com {
    reverse_proxy gosidian:8080
}
```

Both the web UI and the MCP transports (`/mcp`, `/mcp/sse`) are served
from port 8080, so a single virtual host suffices. Two proxy details
matter for MCP:

- **Same-host proxy over loopback.** Both MCP endpoints refuse a
  request that arrived on a loopback-bound connection with a `Host`
  header that is not `localhost`/`127.0.0.1` (DNS-rebinding guard,
  403). Containers and LAN bind addresses never trip it. If your
  proxy runs on the same machine and forwards to `127.0.0.1:8080` with
  `proxy_set_header Host $host`, either make it send `Host: localhost`
  or set `mcp.disable_dns_rebinding_protection = true`
  (`GOSIDIAN_MCP_DISABLE_DNS_REBINDING_PROTECTION=true`).
- **OAuth issuer.** With `[oauth]` enabled, `oauth.issuer` must be the
  public HTTPS origin of this virtual host (`https://notes.example.com`):
  the discovery documents under `/.well-known/` and `/oauth/*` are served
  from it, and hosted clients refuse a plain-HTTP authorization server.
- **Buffering.** gosidian answers `/mcp` and `/mcp/sse` with
  `X-Accel-Buffering: no`, so nginx-style proxies pass the SSE stream
  through even with `proxy_buffering on`. Keep `proxy_read_timeout` at
  60 s or more: `memory_wait_changes` may hold a request for up to 55 s.
- **Credentials and access logs.** The web UI never puts its session
  token in a URL: the live-update stream `/api/v1/events` reads it from
  an `HttpOnly` cookie scoped to that path. Clients from before v2.59
  still send it as `?token=`, accepted for one release. If your proxy
  logs full request lines (nginx's default `$request`, Nginx Proxy
  Manager's `proxy` format), keep the access logs private, or log the
  path without the query string. If you have the legacy
standalone listener enabled (`GOSIDIAN_MCP_ADDR` set), you can
optionally publish it under a separate hostname:

```
mcp-legacy.example.com {
    reverse_proxy gosidian:8765
}
```

## Backup & disaster recovery

Back up the **vault directory** and the **state dir** (`--state-dir` / `GOSIDIAN_STATE_DIR`, default `<vault>/.gosidian/`; see [Configuration → State directory](configuration.md#state-directory)):

- The SQLite index (`<state-dir>/index.db`) is **safe to drop** — it
  rebuilds from the markdown files at the next start.
- `<state-dir>/tokens.json`, `spa_tokens.json`, `auth.json`,
  `oauth_clients.json`, `gitsync.json`, `projects.json`, `audit.jsonl`
  and `config.toml` are
  the only stateful files outside the vault proper. Include them in
  backups — `auth.json` and `gitsync.json` hold secrets, keep the
  archive private.

Git sync (when enabled) adds a second copy of the vault on a remote
git host. It does **not** back up the state dir (by design — tokens
and auth live only on the server; with `--state-dir` they are outside
the repository altogether).

Recommended cadence: nightly tarball of `./vault` and of the state dir
(one archive when the default `<vault>/.gosidian/` is in use) with
14-day retention. If git sync is enabled, the remote already holds a
second copy of the notes themselves.

## Health probe

```bash
curl -sS http://127.0.0.1:8080/healthz
# → {"status":"ok","version":"v2.72.1","mcp_tools":61,
#    "git_sync":{"enabled":true,"healthy":true}}
curl -sS -H "Authorization: Bearer $OWNER_TOKEN" http://127.0.0.1:8080/healthz
# → {…,"vault":"/vault","notes":1406,
#    "git_sync":{"enabled":true,"healthy":false,"last_error":"…","last_error_at":"…"}}
```

Without credentials the probe says only the status, the version, the
MCP tool count and whether git sync is healthy. The vault path, the
note count and git sync's last error and times (git's stderr may carry
the remote's URL) need a credential of the owner: an owner web session
or an MCP token of the owner or of no account (`gosidian token create`)
that no project list limits.

Suitable for Kubernetes liveness/readiness, Docker healthcheck (already
baked into the image: `gosidian healthcheck` asks the port of
`GOSIDIAN_ADDR`, on the loopback for a wildcard address), or external
uptime monitors. The probe passes on
`"status":"ok"` (HTTP 200); a failing git sync shows in
`git_sync.healthy` and never fails it. `mcp_tools` is how many MCP tools
the server registered: after an upgrade it tells whether a new tool is
there, without reconnecting an MCP client, which keeps the tool list it
read when it connected.

At startup the port opens before the vault scan brings the index up to
date. Until the scan ends every request gets a `503` with
`Retry-After: 2`: `/healthz` answers `{"status":"starting"}`, a browser
gets a page that reloads itself, and API and MCP clients get the usual
JSON error with code `server.unavailable`. Event streams are the
exception: a `GET` asking for `text/event-stream` (MCP over HTTP+SSE,
the web UI's live updates) waits, up to 30 s, and gets its stream as
soon as the server is ready, because an EventSource stops reconnecting
at any answer but a 200. The scan re-indexes only the
notes whose content changed since the last run, so a restart takes
under a second on a vault of a few hundred notes. The first start of a
new release that changes how notes are indexed re-reads every note
(the log line `scan complete in …` says how many were re-indexed).
