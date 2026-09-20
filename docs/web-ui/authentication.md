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

Three roles, in decreasing privilege: **owner → member → guest**.

| Capability | owner | member | guest |
|---|:--:|:--:|:--:|
| Read notes (visible projects) | ✅ | ✅ | ✅ |
| Create / edit / delete notes | ✅ | ✅ | — |
| Create / rename / delete projects | ✅ | ✅ | — |
| See **private** projects | ✅ | ✅ | — |
| See **public** projects | ✅ | ✅ | ✅ |
| Create MCP tokens | ✅ | ✅ | — |
| Manage users, invites, roles | ✅ | — | — |
| Edit server settings (`/settings`) | ✅ | — | — |

Enforcement is centralized and **fail-closed**: an unrecognized role is
treated as guest (public-read only). A *read* that the role may not
perform returns **404** (the resource's existence is hidden); a *write*
it may not perform returns **403**.

Guests can never hold MCP tokens — token creation is owner/member-only,
and demoting a user to guest cascade-revokes any tokens they held. So a
guest account is structurally read-only across both the web UI and MCP.

## Project visibility: public vs private

Every project carries a **`public`** flag (default **private**). Owners
toggle it from **Projects** (or `PUT /api/v1/projects/{name}`):

- **private** — visible only to owners and members.
- **public** — additionally visible to **guests**, read-only.

"Public" means *visible to any signed-in user including guests* — it is
**not** anonymous access. Anonymous visitors still hit the login wall.

This is what lets you hand a contractor or a read-only stakeholder a
guest account: they see exactly the projects you flag public, and
nothing else — in the sidebar, search, tags, and the graph view alike.

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
