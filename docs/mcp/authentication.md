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
  `get_frontmatter`/`batch_get`), `memory_search`/`list_notes`/
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
