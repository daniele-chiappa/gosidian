# gosidian web — Vue 3 SPA

The browser frontend that pairs with the gosidian Go binary. Source for
the v2.0 SPA scheduled for tag `v2.0.0` post-soak. See the authoritative
plan in `gosidian/plans/20260430-v2-spa-rewrite` (vault) for scope and
phasing.

## Getting started

```bash
nvm use            # picks node from .nvmrc (22)
npm ci
npm run dev        # http://localhost:5173, proxies API to 127.0.0.1:8080
```

Build artifacts are emitted into `../internal/server/web/dist/` so the
Go embed picks them up at compile time:

```bash
npm run build
```

## Developing plancia alongside

The window manager comes from [plancia](https://github.com/daniele-chiappa/plancia),
pinned in `package.json` to the tarball of one of its GitHub releases. To
change plancia and see the result here without releasing it, point
`PLANCIA_SRC` at the `src/` folder of a plancia checkout (with its own
`npm install` done: its TypeScript config extends a package there):

```bash
PLANCIA_SRC=/path/to/plancia/src npm run dev      # or: npm run test:unit
```

`plancia` and `plancia/style.css` then resolve to those sources, served
through the dev server with hot reload, and Vue, Pinia and the router
stay a single copy (`resolve.dedupe`). Without the variable — `npm run
build`, the Dockerfile, CI — the pinned release is used. When the change
is ready, release plancia (a version tag: its `release` workflow
attaches `plancia-X.Y.Z.tgz` to the GitHub release), then point
`package.json` at the new tarball URL and run `npm install`.

## Layout

- `src/api/`         typed wrappers around `/api/v1/*`
- `src/stores/`      Pinia stores (auth, ui, theme, events)
- `src/composables/` reusable hooks (useNote, useTheme, useSSE, …)
- `src/components/`  primitives + domain + layout + editor + graph
- `src/views/`       one per route; lazy-imported by the router
- `src/locales/`     vue-i18n loader; catalogs come from `internal/i18n/catalogs/*.json`
- `src/styles/`      semantic CSS tokens + Tailwind layer

## Auth model

The SPA holds a Bearer token in `localStorage` (`gosidian.auth`). Axios
attaches it to every `/api/v1/*` request. CSP is strict (no
`unsafe-inline`/`unsafe-eval` in `script-src`), markdown rendering
passes through DOMPurify, and `npm audit` runs in CI.

## Testing

- `npm run test:unit`   Vitest + happy-dom + msw
- `npm run test:e2e`    Playwright against a real binary

## Phase 0 status

Scaffolding only. The shell renders a placeholder `PlaceholderView`,
imports the IT/EN i18n catalogs, applies Catppuccin Mocha tokens, and
proves the toolchain. Phases 1–8 (REST API, auth, routes, editor,
graph, theme, tests, build/cutover) follow.
