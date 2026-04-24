# Development

Build, test, release. For contribution rules see
[CONTRIBUTING.md](../CONTRIBUTING.md).

## Setup

```bash
git clone https://github.com/daniele-chiappa/gosidian.git
cd gosidian
go test ./...                            # full test suite
go build -o gosidian ./cmd/gosidian
./gosidian --vault ./testdata/vault --mcp-addr 127.0.0.1:8765
```

**Requirements**: Go 1.22 or newer. No CGO required for the default
build (the SQLite driver is pure Go). Docker is optional but
convenient for end-to-end tests.

## Test suite

```bash
go test ./...            # unit tests
go test -race ./...      # with race detector (recommended before PR)
```

Test packages cover: `attach`, `audit`, `auth`, `config`, `gitsync`,
`i18n`, `index`, `lint`, `mcp`, `parser`, `server`, `trash`, `vault`,
`webauth`.

- **Handler tests** use `httptest.NewRecorder` + `s.ServeHTTP` (no
  live socket).
- **MCP tests** call handlers directly, not over the SSE transport.
- **No filesystem mocks**: tests use `t.TempDir()` and real vaults.

## Building the Docker image

```bash
docker build -t gosidian:dev .
```

Multi-stage: `golang:1.25-alpine` builder → `alpine:3.20` runtime
with `git` + `ca-certificates` (ADR-003 — `gitsync` needs `git` on
PATH at runtime).

## Build flags

```bash
go build -trimpath \
  -ldflags="-s -w -X main.version=v1.0.0" \
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

See [CHANGELOG.md](../CHANGELOG.md) for the release history.

## Architecture & internals

See [Architecture](architecture.md) for package layout, data flow,
and ADRs.
