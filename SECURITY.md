# Security policy

## Reporting a vulnerability

If you believe you've found a security issue in gosidian, please report
it **privately** through the project's Security Advisory channel —
don't open a public issue.

Go to the repository's **Security** tab and click **"Report a
vulnerability"**. (Relative URL: `./security/advisories/new` from the
project root.) The advisory is visible only to you and the
maintainers: you can discuss the issue privately, work with us on a
fix inside a linked private fork, and — once the fix ships — publish
coordinated release notes with credit to you.

### What to include

- A description of the issue and the impact you foresee
- Steps to reproduce, ideally with a minimal proof of concept
- The affected gosidian version (`gosidian healthcheck` prints it)
- Your preferred credit text (or `anonymous` if you'd rather not be
  named)

We acknowledge new advisories within **72 hours**, triage impact and
share a tentative fix timeline within **14 days**. Default public
disclosure window is **90 days** from first report, or sooner if a fix
is already released; specific windows are negotiated case by case.

## Supported versions

Only the latest minor release on the `v1.x` line receives security
fixes. Older versions are patched only when the fix is trivial. Once
`v2.0` ships, at least the last minor of `v1.x` will continue to get
security fixes for 6 months.

## Scope

In scope:

- The gosidian binary and its shipped Docker image
- The MCP tool surface (authentication, authorisation, input
  validation)
- The web UI (authentication, session handling, template rendering,
  path traversal in file handlers)
- Vault data handling (attachment upload, token/auth file
  persistence)
- The official release artefacts (signatures, checksums — when
  present)

Out of scope:

- Self-hosted infrastructure around gosidian (reverse proxies, OS,
  Docker runtime)
- Denial-of-service through legitimate expensive operations
  (e.g. very large `memory_batch_get` calls)
- Social engineering against maintainers

## Safe-harbour

Good-faith security research is welcome. We will not pursue legal
action against researchers who:

- Avoid privacy violations, data destruction, and service disruption
- Give us reasonable time to fix before public disclosure
- Make a good-faith effort to comply with this policy
