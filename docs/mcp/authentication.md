# MCP authentication

Every MCP call requires a bearer token once at least one token exists
on the server.

## MCP bearer tokens

When `<vault>/.gosidian/tokens.json` is empty, the MCP endpoint is
**open** (useful for localhost development). The first token you create
switches auth on globally.

```bash
gosidian token create --vault ./vault \
  --name my-agent \
  --scopes read,write \
  --project gosidian \        # optional: restrict to one project
  --ttl 720h                  # optional: expiry (default: no expiry)
```

The plaintext token is printed **once** and hashed on disk (SHA-256).
Losing it means revoking and recreating.

## Scopes

- `read` — all read tools (`memory_get*`, `memory_search`, `memory_list*`,
  `memory_backlinks`, `memory_outlinks`, `memory_bootstrap`, …)
- `write` — all mutating tools (`memory_create`, `memory_update`,
  `memory_append`, `memory_edit`, `memory_delete`, `memory_rename_note`,
  `memory_move_note`, `memory_ask`, `memory_upload_attachment`, …)

An admin token (no `--project` scope) can also call
`memory_create_project` / `memory_delete_project` /
`memory_rename_project`.

## Per-project scoping

A token created with `--project foo` sees only `foo/*` in every tool
response. `memory_search projects=["bar"]` from such a token returns
an empty result set (no error) because `bar` is outside the token's
scope.

## Token rotation

From the web UI at `/admin/tokens`:

- Owner accounts see and can revoke every token.
- Member accounts see and manage only their own tokens.

Revocation is immediate: the SSE connection using a revoked token
gets disconnected at the next request.

## Web UI login

For the web UI (not MCP), gosidian supports an optional login layer
on top of the bearer-token surface.

### Single-user setup

```bash
gosidian user setup --vault ./vault --username admin
```

With web login enabled, unauthenticated browser requests are
redirected to `/login`. Failed attempts trigger a rate limiter
(default 5 failures per 15 minutes, see [Configuration](../configuration.md)).

### Multi-user (owner + members)

An `owner` account can invite `member` accounts from `/admin/users`:

- Invites are **single-use** and expire in **24 hours**.
- Disabling a member revokes all their MCP tokens automatically
  (cascade on `OnUserDisabled`).
- Sessions live 24 hours by default (`GOSIDIAN_LOGIN_SESSION_TTL`).

### When to skip web login

If gosidian runs on localhost and only you use it, leaving webauth
unconfigured is fine — the UI is open and the MCP token is the only
credential. For anything exposed over the network, always pair a
reverse proxy with TLS **and** web login.
