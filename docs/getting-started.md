# Getting started

Three routes to a running gosidian instance: source, single Docker
container, or Docker Compose. Pick one, then see [Configuration](configuration.md)
for persistent settings and [MCP client setup](mcp/client-setup.md) for
wiring an agent.

## From source

Requires Go 1.27 (the Go module lives under `src/`).

```bash
git clone https://github.com/daniele-chiappa/gosidian.git
cd gosidian/src
go build -o gosidian ./cmd/gosidian
./gosidian --vault ./vault
```

- Web UI:   `http://127.0.0.1:8080`
- MCP:      `http://127.0.0.1:8080/mcp` (Streamable HTTP; legacy
  HTTP+SSE at `/mcp/sse`)

(MCP is mounted on the web port by default. To enable the legacy
standalone listener for backward compatibility, append
`--mcp-addr 127.0.0.1:8765`.)

## Docker (single container)

```bash
docker run -d \
  --name gosidian \
  -v "$(pwd)/vault:/vault" \
  -p 8080:8080 \
  -e GOSIDIAN_VAULT=/vault \
  -e GOSIDIAN_ADDR=:8080 \
  ghcr.io/daniele-chiappa/gosidian:latest
```

Verify:

```bash
curl -sS http://127.0.0.1:8080/healthz    # → {"status":"ok",…}
```

At the first start, with no account yet, the server creates the owner
`admin` with a random password, and prints it in the log:

```bash
docker logs gosidian 2>&1 | grep "owner account created"
```

The same password is in `initial-admin-password` in the state dir
(`<vault>/.gosidian/` unless you moved it). Sign in with it: the web UI
then asks you to choose your own, and the file goes away.
`GOSIDIAN_AUTO_OWNER=false` turns this off, and the owner then comes from
`gosidian user setup`.

## Docker Compose

Two flavours, depending on where the image comes from:

- **Pull from GHCR** (recommended for staging / second machine): copy
  [examples/docker-compose.image.yml](examples/docker-compose.image.yml),
  `mkdir -p vault`, `docker compose -f docker-compose.image.yml pull && up -d`.
  Anonymous pull, no `docker login` needed for the public image.
- **Build from source** (development / fork): the `docker-compose.yml`
  at the repository root builds the image locally from `./src` —
  useful when you're hacking on the codebase and want to iterate fast.

See [Deployment → Docker Compose](deployment.md#docker-compose) for the
fully-annotated compose with optional git sync, reverse proxy, and TLS.

## First run checklist

1. **Open the web UI** at `http://127.0.0.1:8080`.
2. **Sign in as `admin`** with the password of the first start (in the
   log, and in `<state-dir>/initial-admin-password`), then choose your
   own — see [Authentication](mcp/authentication.md#web-ui-login).
3. **Create an MCP token** — MCP answers nothing without one — from
   `/admin/tokens` (or via the CLI — see
   [MCP authentication](mcp/authentication.md#mcp-bearer-tokens)).
4. **Wire your MCP client** — see [Client setup](mcp/client-setup.md).
5. **Bootstrap your first project** — see
   [Agent patterns → Bootstrap a project](mcp/patterns.md#bootstrap-a-new-project).

That's the full loop: binary up, token created, client connected,
project scaffolded, agent talking to the vault.
