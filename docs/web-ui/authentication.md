# Authentication & roles

gosidian's web UI is multi-user. The **first account ever created**
becomes the **owner**; everyone else joins by invite or — when LDAP is
enabled — by logging in against your directory. This page covers the
role model, project visibility, two-factor (TOTP), and LDAP / Active
Directory login.

> If `<state-dir>/auth.json` does not exist, authentication is
> **disabled** and the UI is open (local bootstrap mode). The first
> `gosidian user setup` or the on-screen "create admin" step turns it on.

## Roles

Three roles, in decreasing privilege: **owner → member → guest**. The
role is a **ceiling**: it says what an account may ever do. What it may
do on a *given project* comes from the project's visibility and from the
grants it holds (next section).

| Capability | owner | member | guest |
|---|:--:|:--:|:--:|
| Read notes of projects visible to the account | ✅ | ✅ | ✅ |
| Create / edit / delete notes (with a write grant) | ✅ | ✅ | — |
| Create projects (becoming their admin) | ✅ | ✅ | — |
| Change a project's settings, rename, delete (with an admin grant) | ✅ | ✅ | — |
| Make a project **public** | ✅ | — | — |
| Manage grants (Projects → Members) | ✅ | — | — |
| Create static MCP tokens | ✅ | — | — |
| Connect MCP clients through OAuth | ✅ | ✅ | ✅ (read) |
| Manage users, invites, roles | ✅ | — | — |
| Edit server settings (`/settings`) | ✅ | — | — |

Enforcement is centralized and **fail-closed**: an unrecognized role is
treated as guest (public-read only). A *read* that the account may not
perform returns **404** (the resource's existence is hidden); a *write*
it may not perform returns **403**. The same predicate gates the REST
API, the live event stream and every MCP token (see
[MCP authentication](../mcp/authentication.md)).

## Project access: visibility and grants

Who can do what on a project is the combination of two things:

- **Visibility** says who may **read** it. Every project has one:
  - **private** — only accounts holding a grant (and the owner).
  - **internal** — every member account.
  - **public** — every signed-in account, guests included. This is *not*
    anonymous access: visitors still hit the login wall (unless the
    server runs in open mode).
- **Grants** give a specific account a level on the project, whatever
  its visibility:
  - **read** — may open it even when the visibility would not allow it;
  - **write** — may also create, edit and delete notes and attachments;
  - **admin** — may also change its settings (visibility, flags), rename
    and delete it.

The effective level is the higher of the two, capped by the role: a guest
never exceeds read, the owner is admin everywhere. **Writing always takes
a grant** — visibility alone never lets anyone edit.

Where to manage it in the web UI:

- **Projects** shows each project with its visibility (lock = private,
  globe = public), your own level, and how many accounts hold a grant.
  Project admins change the visibility from the row (public is the
  owner's alone); the owner opens **Members** to add, change or remove
  grants.
- The sidebar draws the same lock/globe cue next to project roots, and
  shows the "new note" button only where you may write.
- **Admin → Users** shows each account's access at a glance (how many
  projects it can read and write) and a **View** button listing exactly
  which projects it sees, at which level and why.
- **Settings → Project access** sets the **default visibility** for
  projects created from now on (and for folders that appear on disk
  without settings). Fresh installations default to private; upgraded
  ones to internal.

New accounts start with the projects their role and the visibilities
give them — a member sees internal and public projects, a guest public
ones — and gain the rest through grants. The account that creates a
project is its admin.

### Upgrading from 2.29 and earlier

Earlier releases had a `public` flag plus a global *member scope*
switch: by default every member saw and edited every project, and the
per-project memberships only applied after flipping the switch. On the
first start after the upgrade the store is converted once:

- `public` projects stay **public**; the others become **internal** when
  the switch was off (its default) or **private** when it was on;
- existing memberships become grants with the same level;
- when the switch was off, every member account receives a **write
  grant on every existing project**, so nobody loses access they had.
  Writing a project created *after* the upgrade takes a grant.

The API keeps `public` as an alias (`true` → public, `false` →
internal) and adds `visibility`, `access` (the caller's level) and
`members_count` to the project payloads; `GET /api/v1/me/access` lists
the caller's effective access and `GET /api/v1/admin/users/{id}/access`
the same for any account.

## Invites

Owners create single-use, time-limited invite links from **Admin →
Users** (default TTL **24h**). The invitee opens the link, picks a
username and password, and is created as a **member** (the owner can
change the role afterwards). Invites are consumed on signup and stored
alongside accounts in `auth.json`.

## Two-factor authentication (TOTP)

TOTP (RFC 6238, any authenticator app) is governed by a **global mode**
plus an optional **per-user override**.

### Global mode — `webauth.totp_mode`

Set from `/settings` (owner) or `GOSIDIAN_TOTP_MODE`:

| Mode | Behaviour |
|---|---|
| `off` | **Master switch.** No 2FA at login, even for users who enrolled. The TOTP field is hidden on the login form. |
| `optional` | Users *may* enroll; once enrolled (or if their per-user policy is `enabled`) a code is required at login. |
| `required` | Every user must enroll. Users without a secret hit a **forced-enrollment** step right after their password before they can proceed. |

`off` is a deliberate escape hatch: flipping the global switch to `off`
disables enforcement immediately, so a misconfiguration can never lock
the team out.

### Per-user policy

In **Admin → Users**, each account has a TOTP policy:

- **inherit** (default) — follow the global mode.
- **enabled** — force 2FA for this user even when the global mode is
  `optional`.
- **disabled** — exempt this user (e.g. a shared service login), unless
  the global mode is `required`.

### Enrolling

A user enrolls from **Settings → Two-factor**: scan the QR code (or copy
the secret), then **confirm a current code** — the secret only activates
after a successful confirmation, so a mistyped setup can't lock the
account. Enrollment is self-service; owners don't handle secrets.

### Recovery codes

Confirming the enrollment also issues **8 single-use recovery codes**
(`xxxxx-xxxxx`), shown once. Each one signs the user in **once** in place
of a TOTP: type it in the same field of the login form. A session opened
with a recovery code shows a banner with the remaining count. The set
can be regenerated at any time from **Settings → Two-factor** by
entering a current TOTP code; every previous code stops working. The
server stores only bcrypt hashes of the codes.

### Lost authenticator: resetting two-factor

When a user has neither the authenticator nor a recovery code left:

- **Owner, from the UI**: **Admin → Users → Reset** next to the TOTP
  marker clears that user's secret and recovery codes
  (`DELETE /api/v1/admin/users/{id}/totp`, audited as `totp_reset`).
  The per-user policy and the user's sessions are untouched — under a
  `required` policy the user simply enrolls again at the next login.
- **Any account, the owner included, from the CLI**:

  ```bash
  gosidian user totp-reset --vault ./vault --username alice
  # in Docker (the binary is /gosidian, not on PATH; the state dir comes from the env):
  docker exec gosidian /gosidian user totp-reset --vault /vault --username alice
  ```

  Safe with the server running: the accounts file is re-read at the
  next request. `gosidian user setup --totp` prints the owner's recovery
  codes right after the QR.

### Brute-force protection

Two limiters guard the login, sharing the `login_max_failures` /
`login_window` knobs: one per client IP on any failure, one per
**account** on wrong second factors only. The account limiter is fed
exclusively by "right password, wrong code" attempts, so a stranger
spraying wrong passwords cannot lock anyone out, while a distributed
guess at the 6-digit code is capped per account. A wrong TOTP on the
recovery-code regeneration counts too.

## LDAP / Active Directory login

With LDAP enabled, users authenticate against your directory and a local
**guest** account is **auto-provisioned on first login** — no manual user
creation, no password stored in gosidian.

### How it works

1. **Search-then-bind.** gosidian binds as the service account
   (`bind_dn` / `bind_password`, or anonymously if unset), searches
   `user_base_dn` for `user_filter` with the typed username, then
   **re-binds as the found DN** with the supplied password to verify it.
2. **Auto-provision.** On the first successful LDAP login, gosidian
   creates a local account with role **guest** and `auth_source: ldap`
   (no password hash). An owner can promote it to **member** from
   **Admin → Users**.
3. **Local accounts win.** A local username always **shadows** LDAP — an
   existing local account is verified against its bcrypt hash, never
   against the directory. This keeps the bootstrap owner working even if
   the directory is down.

### Configuration

See [Configuration](../configuration.md) for the full env-var / TOML
table. A typical OpenLDAP setup:

```toml
[ldap]
enabled       = true
url           = "ldap://ldap.example.com:389"
bind_dn       = "cn=svc-gosidian,ou=services,dc=example,dc=com"
bind_password = "…"            # prefer GOSIDIAN_LDAP_BIND_PASSWORD
user_base_dn  = "ou=people,dc=example,dc=com"
user_filter   = "(uid=%s)"     # %s = escaped username
```

For **Active Directory**, only the attributes differ — the flow is
identical:

```toml
[ldap]
enabled       = true
url           = "ldaps://dc1.corp.example.com:636"   # AD usually requires TLS
bind_dn       = "CN=svc-gosidian,OU=Services,DC=corp,DC=example,DC=com"
bind_password = "…"
user_base_dn  = "OU=Users,DC=corp,DC=example,DC=com"
user_filter   = "(sAMAccountName=%s)"
```

### TLS

Two encrypted transports are supported:

- **LDAPS** — `url = "ldaps://host:636"`.
- **StartTLS** — `url = "ldap://host:389"` + `start_tls = true`.

Modern AD typically rejects unencrypted binds, so use one of these in
production. `insecure_skip_verify = true` accepts a self-signed
certificate and is **for development only** — in production trust the
directory's CA instead.
