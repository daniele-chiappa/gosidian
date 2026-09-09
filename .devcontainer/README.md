# Demo Codespace

This folder makes gosidian launchable as a **zero-setup demo** in
[GitHub Codespaces](https://github.com/features/codespaces). Click the
"Open in GitHub Codespaces" badge in the project README and you get your
own private, throwaway instance — no install, no account, no exposure of
anyone's network.

## What happens

1. `devcontainer.json` builds a container with Go 1.27 + Node 24.
2. `setup.sh` (run once) builds the SPA + binary from the source you
   opened, seeds the disposable vault in `demo-vault/`, and provisions a
   `demo` / `gosidian-demo` owner.
3. `start.sh` (run on every start) launches the server on port `8080`,
   which is auto-forwarded and opens in your browser.

The instance is yours alone: it runs on your Codespaces quota, the
forwarded port is private to you, and nothing is shared or persisted
beyond the Codespace's lifetime.

## Why a login is required

With zero users, the v2 SPA cannot read any data — every `/api/v1`
route sits behind `requireAuth`. So the demo provisions a single owner
rather than relying on an anonymous "open mode" (which the SPA does not
implement). Credentials are intentionally public: **demo / gosidian-demo**.

## The demo vault

`demo-vault/` is a small, self-contained English vault that showcases
backlinks, the graph view, full-text search, the Karpathy-Wiki-Stack
project layout, an `.html` note, and a media (image) note. Edit it to
change what visitors see first.
