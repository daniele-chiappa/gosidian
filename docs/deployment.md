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

Open `http://localhost:8080` in a browser to provision the first
admin user (see [Authentication](mcp/authentication.md#web-ui-login)).
Then create an MCP token from `/admin/tokens` and wire your client —
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
  60 s or more: `memory_wait_changes` may hold a request for up to 55 s. If you have the legacy
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
curl -sS http://127.0.0.1:8080/healthz    # → "ok" or structured JSON
```

Suitable for Kubernetes liveness/readiness, Docker healthcheck (already
baked into the image), or external uptime monitors.
