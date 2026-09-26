# MCP authentication

Every MCP call requires a bearer token once at least one token exists
on the server.

## MCP bearer tokens

When `<state-dir>/tokens.json` is empty, the MCP endpoint is
**open** (useful for localhost development). The first token you create
switches auth on globally.

```bash
gosidian token create --vault ./vault \
  --name my-agent \
  --scopes read,write \
  --project gosidian \        # optional: restrict to one or more projects
  --ttl 720h \                # optional: expiry (default: no expiry)
  --tool-profile core         # optional: worker tool subset (default: full)
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

## Tool profiles

`--tool-profile` controls which slice of the MCP tool catalogue the
token sees (REST: `tool_profile` on `POST /api/v1/admin/tokens`;
introspection: `memory_self_stats`):

- **`full`** (default, and the value every pre-existing token keeps):
  the whole catalogue.
- **`core`**: the worker subset — session start (`memory_bootstrap`),
  note CRUD, targeted reads (`get_section`/`get_outline`/
  `get_frontmatter`/`batch_get`), `memory_search`/`query`/`list_notes`/
  `notes_by_tag`/`list_projects`, **`memory_ingest` as the single file
  door** (it routes to table/media notes and attachments internally, so
  the dedicated upload/creator tools stay full-profile and workers never
  pay their schemas), the full handoff lifecycle and
  `memory_wait_changes`. `memory_self_improve` is admitted only for
  tokens opted into that loop.

The profile is an **access-control boundary**, not a display filter: a
tool outside the profile is absent from `tools/list` *and* answers
`tool not found` if called by name. Give `core` to sub-agent tokens to
cut their per-session schema cost (~60-70% fewer tool descriptions);
keep `full` for orchestrators and interactive use.

## Per-project scoping

A token created with `--project foo` sees only `foo/*` in every tool
response. `memory_search projects=["bar"]` from such a token returns
an empty result set (no error) because `bar` is outside the token's
scope.

### Multi-project tokens

`--project` accepts a comma-separated list:

```bash
gosidian token create --vault ./vault \
  --name orchestrator \
  --scopes read,write \
  --project agent-a,agent-b,agent-c
```

The token reads and writes in all listed projects and nowhere else —
the natural shape for an **orchestrator** that dispatches handoffs to
several agent projects without holding an over-privileged admin
token. Semantics to know:

- **Explicit project required**: where a single-project token is
  silently defaulted to its project (`memory_list_notes`,
  `memory_bootstrap`, …), a multi-project token must name one of its
  projects — an omitted argument never silently widens a query.
  `memory_wait_changes` is the exception by design: with no `project`
  filter it watches all of the token's projects at once.
- **Not an admin**: project lifecycle tools
  (`memory_create_project` / `memory_delete_project` /
  `memory_rename_project`) still require an unscoped token.
- **Search intersects**: `memory_search projects=[...]` keeps only
  the projects inside the scope.
- The REST API accepts the same shape (`POST /api/v1/admin/tokens`
  with `projects: ["a","b"]`); `memory_self_stats` reports the list.
- **Backward compatible**: single-project tokens behave exactly as
  before, and `tokens.json` files from older versions load unchanged.

## Token rotation

From the web UI at `/admin/tokens`:

- Owner accounts mint and revoke any token from `/admin/tokens`; every
  other account mints its own from **Settings → My MCP tokens**
  (`POST /api/v1/me/tokens`, mode `inherit` or `custom`; read-only
  accounts get read tokens only) and sees its OAuth grants there too.
- A token never outruns the account it belongs to. On every request the
  token's project list is intersected with what its owner may currently
  read (the project's visibility and the account's grants), and its write
  scope with the projects the account holds a write or admin grant on, so
  removing a grant, downgrading it to read or making a project private
  takes effect immediately without touching the token. Tokens owned by
  the owner account, or minted from the CLI, keep their declared scope.

Revocation is immediate: the SSE connection using a revoked token
gets disconnected at the next request.

## OAuth 2.1 for hosted clients (claude.ai, ChatGPT, Claude Code)

Static bearer tokens are the default and work everywhere a header can be
set. Hosted clients — claude.ai custom connectors, ChatGPT connectors —
and Claude Code's browser login expect the MCP authorization flow
instead: OAuth 2.1 with PKCE, discovery documents and a consent screen.
gosidian ships that authorization server, off by default:

```toml
[oauth]
enabled = true
issuer  = "https://notes.example.com"   # the public origin clients use
```

(or `GOSIDIAN_OAUTH_ENABLED=true` and `GOSIDIAN_OAUTH_ISSUER=…`). The
issuer must be the exact URL users type into their client, served over
HTTPS by your reverse proxy: the MCP resource is `<issuer>/mcp` and every
OAuth endpoint hangs off the same origin.

What happens when a client connects:

1. The client calls `/mcp` without a token and gets `401` with
   `WWW-Authenticate: Bearer resource_metadata="<issuer>/.well-known/oauth-protected-resource/mcp", scope="read write"`.
2. It reads that document and the authorization-server metadata at
   `/.well-known/oauth-authorization-server`, then identifies itself:
   with a **Client ID Metadata Document** (an HTTPS URL as `client_id`,
   the way claude.ai, ChatGPT and Claude Code do) or through **Dynamic
   Client Registration** (`POST /oauth/register`). Only public clients
   exist; there is no client secret.
3. It opens `/oauth/authorize` in the browser. gosidian validates the
   request and sends the browser to the SPA consent screen — through
   the normal login first if there is no session, two-factor included.
4. On the consent screen you pick the **projects** the client may use
   (all visible ones by default; an owner keeping them all gets an
   unscoped grant that also covers future projects) and whether it may
   **write**. Guests can only grant read.
5. The client exchanges the code at `/oauth/token` (PKCE S256 verified)
   for an **access token** (1 hour, held in memory) and a **refresh
   token** (30 days, rotated on every use). Presenting a rotated refresh
   token again revokes the whole grant.

The consent becomes a **grant**: an ordinary MCP token record named
`<client> · oauth`, listed in Admin → Tokens with its projects and
scopes. Revoke it there (or from the CLI) and every access token dies
with it; disabling the user revokes their grants like any other token.
Access tokens do not survive a restart — clients refresh transparently.
Audit actions: `oauth_client_register`, `oauth_grant`, `oauth_refresh`,
`oauth_revoke`.

Redirect URIs must be HTTPS, or HTTP on a loopback host; loopback
redirects match regardless of port (Claude Code binds an ephemeral one).
`oauth.allowed_redirect_hosts` can pin the non-loopback hosts you accept.
Registered clients are kept in `oauth_clients.json` in the state dir,
capped and expired when idle; metadata documents are fetched with an
SSRF guard (public HTTPS hosts only, no redirects, 64 KiB) and cached.

Scopes: `read`, `write` and `offline_access` (the client's way of asking
for a refresh token; one is issued regardless). A client asking for
`resource` must name `<issuer>/mcp` exactly.

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

Lost the authenticator of an account with TOTP? `gosidian user totp-reset
--vault ./vault --username admin` clears its secret and recovery codes,
server running or not — see
[Authentication & roles](../web-ui/authentication.md#lost-authenticator-resetting-two-factor).

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
