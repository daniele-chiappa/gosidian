# Development

Build, test, release. For contribution rules see
[CONTRIBUTING.md](../CONTRIBUTING.md).

## Setup

```bash
git clone https://github.com/daniele-chiappa/gosidian.git
cd gosidian/src                          # Go module lives in src/
go test ./...                            # full test suite
go build -o gosidian ./cmd/gosidian
./gosidian --vault ./testdata/vault
```

The Go source lives under `src/` (the module root, `cmd/gosidian` +
`internal/*`); the Vue 3 + Vite SPA lives under `src/web/`. `npm run
build` (run from `src/web`) emits the bundle that `go build` from
`src/` embeds via `//go:embed`.

**Requirements**: Go 1.27 (and Node 24 for the SPA build). No CGO
required for the default build (`CGO_ENABLED=0`; the SQLite driver is
pure Go). Docker is optional but convenient for end-to-end tests.

## Test suite

```bash
go test ./...            # unit tests
go test -race ./...      # with race detector (recommended before PR)
```

Test packages cover: `api/v1`, `attach`, `audit`, `auth`, `authz`,
`config`, `gitsync`, `i18n`, `index`, `insights`, `lint`, `mcp`,
`parser`, `server`, `trash`, `vault`, `webauth`.

- **Handler tests** use `httptest.NewRecorder` + `s.ServeHTTP` (no
  live socket).
- **MCP tests** call handlers directly, not over the SSE transport.
- **No filesystem mocks**: tests use `t.TempDir()` and real vaults.

## Building the Docker image

```bash
docker build -f src/Dockerfile -t gosidian:dev ./src
```

3-stage (ADR-003):

1. **`node:24-alpine`** — `npm ci && npm run build` of the `src/web`
   SPA, producing the embeddable Vite bundle.
2. **`golang:1.27-alpine`** — `CGO_ENABLED=0 go build` of the Go
   binary with the SPA bundle embedded via `//go:embed`.
3. **`alpine:3.20`** runtime with `git` + `ca-certificates` — the
   final stage keeps `git` on PATH because `gitsync` shells out to it
   at runtime (not distroless, by ADR-003).

## Build flags

```bash
go build -trimpath \
  -ldflags="-s -w -X main.version=v2.x.x" \
  -o gosidian ./cmd/gosidian
```

`-trimpath` removes absolute paths from binaries (reproducible
builds); `-s -w` strips debug info.

## Releasing

gosidian follows [SemVer](https://semver.org/) strictly:

- **MAJOR** — breaking changes to the MCP tool surface, the web URL
  contract, the vault layout convention, or the on-disk format of
  `.gosidian/*` files.
- **MINOR** — new user-facing features that preserve backward
  compatibility (new MCP tools, new pages, new env variables, i18n
  additions).
- **PATCH** — bug fixes on an already-released version.

**Commits land on `main` without a tag by default.** Doc-only
changes, refactors, and work-in-progress don't get their own
release. A tag is cut when accumulated changes justify publishing —
new tool, new config surface, visible behaviour change — so every
`v1.N` is meaningful for downstream Go module consumers. A
maintainer cuts the tag from `main` when that threshold is reached.

**MCP Registry.** Every release tag also publishes
`io.github.daniele-chiappa/gosidian` to the
[official MCP Registry](https://registry.modelcontextprotocol.io) from
CI: the `registry` job runs after the tagged image is on GHCR, rewrites
`version` and the OCI `identifier` in `server.json` from the tag, runs
`mcp-publisher validate`, logs in with GitHub OIDC (no stored secret)
and publishes. The registry verifies ownership through the
`io.modelcontextprotocol.server.name` label baked into the image by the
`Dockerfile`, so an image built without it cannot be listed. To check a
`server.json` change locally before tagging:

```bash
curl -fsSL https://github.com/modelcontextprotocol/registry/releases/download/v1.8.1/mcp-publisher_linux_amd64.tar.gz | tar -xz mcp-publisher
./mcp-publisher validate
```

See [CHANGELOG.md](../CHANGELOG.md) for the release history.

## Architecture & internals

See [Architecture](architecture.md) for package layout, data flow,
and ADRs.
