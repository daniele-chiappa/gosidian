# Deployment

Options for running gosidian in production: Docker Compose as the
recommended starting point, reverse proxy for TLS, backup strategy
for disaster recovery.

## Docker Compose

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
      GOSIDIAN_MCP_ADDR: "0.0.0.0:8765"
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
      - "8765:8765"
```

## Reverse proxy + TLS

gosidian itself speaks plaintext HTTP. For anything beyond localhost,
put a reverse proxy (Caddy, Traefik, nginx) in front and let it
terminate TLS.

Minimal Caddyfile example:

```
notes.example.com {
    reverse_proxy gosidian:8080
}

mcp.example.com {
    reverse_proxy gosidian:8765
}
```

## Backup & disaster recovery

Back up the **vault directory** and `<vault>/.gosidian/`:

- The SQLite index (`.gosidian/index.db`) is **safe to drop** — it
  rebuilds from the markdown files at the next start.
- `.gosidian/tokens.json`, `.gosidian/auth.json`,
  `.gosidian/audit.jsonl`, `.gosidian/config.toml` are the only
  stateful files outside the vault proper. Include them in backups.

Git sync (when enabled) adds a second copy of the vault on a remote
git host. It does **not** back up `.gosidian/` (by design — tokens
and auth live only on the server).

Recommended cadence: nightly tarball of `./vault` (including
`.gosidian/`) with 14-day retention. If git sync is enabled, the
remote already holds a second copy of the notes themselves.

## Health probe

```bash
curl -sS http://127.0.0.1:8080/healthz    # → "ok" or structured JSON
```

Suitable for Kubernetes liveness/readiness, Docker healthcheck (already
baked into the image), or external uptime monitors.
