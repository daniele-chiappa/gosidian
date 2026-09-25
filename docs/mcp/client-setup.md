# MCP client setup

Connect any MCP-compatible client to the MCP endpoint, passing the
token as a bearer header. Language preference uses the standard
`Accept-Language` header.

The endpoint is **`/mcp` on the web port** (single-port mode), speaking
**Streamable HTTP** — the current MCP transport. The legacy **HTTP + SSE**
transport is still served at `/mcp/sse` for older clients (no removal
date), and the legacy standalone SSE listener on a separate port
(typically 8765) remains opt-in — see the migration notes at the bottom
of this page.

## Generic JSON config

```json
{
  "mcpServers": {
    "gosidian": {
      "type": "http",
      "url": "http://127.0.0.1:8080/mcp",
      "headers": {
        "Authorization": "Bearer gosidian_XXXXXXXXXXXXXXXXXXXXXXXX",
        "Accept-Language": "en"
      }
    }
  }
}
```

Replace `127.0.0.1` with your server hostname and the token with the
plaintext printed by `gosidian token create`. Clients that still speak
only HTTP+SSE use `"type": "sse"` with `"url": ".../mcp/sse"`.

## Claude Code

```bash
claude mcp add gosidian http://127.0.0.1:8080/mcp \
  --transport http \
  --header "Authorization: Bearer gosidian_XXXXXXXXXXXXXXXXXXXXXXXX"
```

After restart, the tools appear under `gosidian__memory_*` and are
callable from any conversation. See
[Agent patterns](patterns.md) for the recommended session opening.
`claude mcp list` shows the server as `(HTTP) - ✓ Connected`.

Optional: the hooks in [`contrib/claude-code/`](../../contrib/claude-code/README.md)
inject the project's `hot.md` focus at every session start and append a
digest of the session to the vault at compaction and at the end — a
safety net for agents that skip the bootstrap or the closing log.

## claude.ai / Claude Desktop custom connector (OAuth)

With `[oauth]` enabled (see [Authentication → OAuth 2.1](authentication.md#oauth-21-for-hosted-clients-claudeai-chatgpt-claude-code)),
add gosidian as a custom connector from *Settings → Connectors*: remote
MCP server URL `https://<issuer>/mcp`, no client ID or secret. Claude
discovers the authorization server, opens the gosidian consent screen in
the browser (log in if asked), and receives its own token. The same works
for ChatGPT connectors and for any client that implements MCP
authorization.

## Claude Code without a pasted token (OAuth)

```bash
claude mcp add gosidian https://<issuer>/mcp --transport http
```

On first use Claude Code answers `401`, opens the consent screen in your
browser and stores the token it receives; `/mcp` inside Claude Code shows
the connection and lets you re-authenticate. Bearer tokens (the
`--header` form above) keep working and are the right choice for CI and
for LAN deployments without HTTPS.

## Zed

Add to your `settings.json`:

```json
{
  "context_servers": {
    "gosidian": {
      "source": "custom",
      "command": {
        "type": "http",
        "url": "http://127.0.0.1:8080/mcp",
        "headers": {
          "Authorization": "Bearer gosidian_..."
        }
      }
    }
  }
}
```

## Cursor / Continue / other clients

Any MCP-compatible client that supports Streamable HTTP (or HTTP+SSE)
with custom headers works. Typical configuration fields:

- `url` — `http://<host>:<port>/mcp` (Streamable HTTP, recommended);
  `http://<host>:<port>/mcp/sse` for SSE-only clients; or the legacy
  `http://<host>:<port>/sse` when the standalone listener is enabled
- `transport` / `type` — `"http"` (Streamable HTTP) or `"sse"`
- `headers.Authorization` — `Bearer <plaintext>`
- `headers.Accept-Language` — optional; `en`, `it`, `es`, `fr`, `de`
  available in v1.10

## stdio clients

gosidian has no stdio mode: the server owns the vault, the index and the
audit trail, so agents always talk to the running instance. A client that
only speaks stdio can go through a bridge such as `mcp-remote`, pointing
it at the `/mcp` URL with the bearer header.

## Custom clients

If you build your own MCP client:

- Both transports follow the standard MCP spec — gosidian adds no custom
  framing. `POST /mcp` answers JSON (`application/json`); `GET /mcp`
  answers 405 because gosidian never sends server-initiated messages —
  poll `memory_wait_changes` for change events instead.
- Errors are JSON objects with `error.code` and `error.message` from
  the [internationalized error catalogue](../../internal/i18n/catalogs/errors.en.json).
- Responses include an `etag` field on every read tool; pass it back
  as `if_match` on the matching write to get optimistic locking.
- Check `memory_self_stats()` for the token's rate-limit headroom
  before doing anything aggressive.

## Verification

Once connected, every client should see 57 tools in `tools/list`. A
good smoke sequence for your first call:

```
memory_bootstrap(project="<name-of-a-project>")
```

A healthy response contains `hot_md_content`, `readme_content`,
`active_plans`, `available_skills`, `recent_notes`, and
`project_stats`. If it comes back with an auth error, the bearer
token is wrong; with an empty project list, your vault has no
top-level folders yet (see
[Agent patterns → Bootstrap a project](patterns.md#bootstrap-a-new-project)).

From a shell, without a client:

```bash
curl -s -X POST http://127.0.0.1:8080/mcp \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | head -c 300
```

## From the MCP Registry

gosidian is listed in the [official MCP Registry](https://registry.modelcontextprotocol.io)
as `io.github.daniele-chiappa/gosidian` (an OCI package on GHCR with the
Streamable HTTP transport; the source of truth is `server.json` in the
repository). The listing is for discovery: gosidian is a server you run,
not a process a client can spawn on demand. A client that installs it
from the registry still has to give the container a vault volume, open
the web UI once to create the admin user and an MCP token, and pass that
token as the `Authorization` header — exactly the steps above.

## Migrating from HTTP+SSE

Clients configured for `/mcp/sse` keep working unchanged. To move to
the current transport, change the URL to `/mcp` and the transport to
`http` (Claude Code: `claude mcp remove gosidian` then the `claude mcp
add … --transport http` command above). Nothing changes server-side.

## Migrating from the legacy standalone port

Versions before the single-port change exposed MCP on its own port
(typically 8765) at path `/sse`. That deployment shape is still
supported when `--mcp-addr` / `GOSIDIAN_MCP_ADDR` is set, but is
deprecated and SSE-only. To migrate:

1. **Update the client URL** from `http://<host>:<legacy-port>/sse` to
   `http://<host>:<web-port>/mcp` with transport `http`. Bearer header
   unchanged.
2. **Drop the second port mapping** from your Docker / compose config
   (the line that bound `8765:8765`).
3. **Unset `GOSIDIAN_MCP_ADDR`** to silence the deprecation warning at
   boot. The standalone listener will not start; clients must use the
   web-port path.

The motivation: a single tunnel (SSH `-L 8080`, reverse proxy, or any
other single-port forwarder) now serves both the web UI and the agent
transport. This removes a class of remote-deployment misconfiguration
where clients reached the web port for `/api/upload` but not the
agent port for MCP.
