# Getting started

Three routes to a running gosidian instance: source, single Docker
container, or Docker Compose. Pick one, then see [Configuration](configuration.md)
for persistent settings and [MCP client setup](mcp/client-setup.md) for
wiring an agent.

## From source

Requires Go 1.22 or newer.

```bash
git clone https://github.com/daniele-chiappa/gosidian.git
cd gosidian
go build -o gosidian ./cmd/gosidian
./gosidian --vault ./vault --mcp-addr 127.0.0.1:8765
```

- Web UI:   `http://127.0.0.1:8080`
- MCP SSE:  `http://127.0.0.1:8765/sse`

## Docker (single container)

```bash
docker run -d \
  --name gosidian \
  -v "$(pwd)/vault:/vault" \
  -p 8080:8080 \
  -p 8765:8765 \
  -e GOSIDIAN_VAULT=/vault \
  -e GOSIDIAN_ADDR=:8080 \
  -e GOSIDIAN_MCP_ADDR=0.0.0.0:8765 \
  ghcr.io/daniele-chiappa/gosidian:latest
```

Verify:

```bash
curl -sS http://127.0.0.1:8080/healthz    # → "ok"
```

## Docker Compose

See [Deployment → Docker Compose](deployment.md#docker-compose) for the
full compose file with optional git sync, reverse proxy, and TLS.

## First run checklist

1. **Open the web UI** at `http://127.0.0.1:8080`.
2. **(Optional) Set up web login** if gosidian will be exposed beyond
   localhost — see [Authentication](mcp/authentication.md#web-ui-login).
3. **Create an MCP token** from `/admin/tokens` (or via the CLI — see
   [MCP authentication](mcp/authentication.md#mcp-bearer-tokens)).
4. **Wire your MCP client** — see [Client setup](mcp/client-setup.md).
5. **Bootstrap your first project** — see
   [Agent patterns → Bootstrap a project](mcp/patterns.md#bootstrap-a-new-project).

That's the full loop: binary up, token created, client connected,
project scaffolded, agent talking to the vault.
