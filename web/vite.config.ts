import { defineConfig, searchForWorkspaceRoot, type Plugin } from 'vite'
import vue from '@vitejs/plugin-vue'
import VueI18nPlugin from '@intlify/unplugin-vue-i18n/vite'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import { basename, dirname, join, resolve } from 'node:path'
import { fileURLToPath, URL } from 'node:url'

// Mermaid loads each kind of diagram it knows on demand, so a build carries
// all of them as chunks (38 kinds: 3.7 MB of JS, 9 MB of source maps), and
// the binary embeds the build. plancia-diagrams draws four with it (the mind
// map, the fishbone, the Venn and the Gantt) and reads two more when it
// converts Mermaid to a diagram (the flowchart and the quadrant chart),
// IMP-169: the others are left out, each replaced by an empty module that
// no diagram of a note ever reaches. The list is read from mermaid's own
// core, so a new version needs no change here.
function mermaidKinds(keep: RegExp): Plugin {
  let skipped = new Set<string>()
  return {
    name: 'gosidian:mermaid-kinds',
    enforce: 'pre',
    buildStart() {
      try {
        const core = join(
          dirname(createRequire(import.meta.url).resolve('mermaid/package.json')),
          'dist/mermaid.core.mjs',
        )
        const names = [
          ...readFileSync(core, 'utf8').matchAll(
            /import\("\.\/chunks\/mermaid\.core\/([\w-]+\.mjs)"\)/g,
          ),
        ].map((m) => m[1]!)
        skipped = new Set(names.filter((n) => !keep.test(n)))
      } catch {
        skipped = new Set() // no mermaid installed: nothing to leave out
      }
    },
    load(id) {
      if (
        /[\\/]mermaid[\\/]dist[\\/]chunks[\\/]mermaid\.core[\\/]/.test(id) &&
        skipped.has(basename(id))
      ) {
        return 'export const diagram = undefined\n'
      }
    },
  }
}

// Developing plancia alongside gosidian: with PLANCIA_SRC set to the src/
// folder of a plancia checkout, `plancia` resolves to those sources instead
// of the release pinned in package.json, so a change there shows at once in
// `npm run dev` and in the unit tests, with no release in between. Builds
// without the variable (the Dockerfile, CI) use the release. See README.md.
const planciaSrc = process.env.PLANCIA_SRC ? resolve(process.env.PLANCIA_SRC) : ''
// The same for plancia-diagrams (IMP-169): PLANCIA_DIAGRAMS_SRC at the src/
// folder of the diagrams/ package of a plancia checkout, its dependencies
// installed there.
const diagramsSrc = process.env.PLANCIA_DIAGRAMS_SRC
  ? resolve(process.env.PLANCIA_DIAGRAMS_SRC)
  : ''

// gosidian SPA Vite config.
//
// Build output is written into the Go server's embedded FS at
// internal/server/web/dist via --outDir at npm run build time. Asset
// URLs are rooted at /static/dist/ so the embed handler can serve
// them with aggressive cache headers; the generated index.html and
// HTML shell stays at /, served by handlers_spa.go.
// The Go server `npm run dev` proxies to: the development lab by default.
const devApi = process.env.GOSIDIAN_API ?? 'http://127.0.0.1:8084'

export default defineConfig({
  plugins: [
    vue(),
    mermaidKinds(
      /^(mindmap-definition|ishikawaDiagram|vennDiagram|ganttDiagram|flowDiagram|quadrantDiagram)-/,
    ),
    // Pre-compile i18n message catalogs into AOT functions at build
    // time. Without this, vue-i18n's runtime compiler uses
    // `new Function(...)` to compile messages on demand — blocked by
    // our strict CSP `script-src 'self'` (no `unsafe-eval`), which
    // would otherwise throw on every t() call.
    VueI18nPlugin({
      include: [fileURLToPath(new URL('../internal/i18n/catalogs/**', import.meta.url))],
      runtimeOnly: true,
      compositionOnly: true,
      // Catalogs are shared with the Go side and contain inline <code>
      // / <kbd> tags for some help blurbs — strictMessage off keeps
      // them as literal strings (we control the rendering surface,
      // and DOMPurify still sanitises any v-html paths).
      strictMessage: false,
      escapeHtml: false,
    }),
  ],
  base: '/static/dist/',
  resolve: {
    alias: [
      { find: '@', replacement: fileURLToPath(new URL('./src', import.meta.url)) },
      {
        find: '@catalogs',
        replacement: fileURLToPath(new URL('../internal/i18n/catalogs', import.meta.url)),
      },
      ...(planciaSrc
        ? [
            { find: /^plancia$/, replacement: `${planciaSrc}/index.ts` },
            { find: /^plancia\/style\.css$/, replacement: `${planciaSrc}/style.css` },
          ]
        : []),
      ...(diagramsSrc
        ? [
            { find: /^plancia-diagrams$/, replacement: `${diagramsSrc}/index.ts` },
            { find: /^plancia-diagrams\/style\.css$/, replacement: `${diagramsSrc}/style.css` },
          ]
        : []),
    ],
    // One Vue, Pinia and router: plancia's sources must not pick up the
    // copies in its own node_modules.
    dedupe: ['vue', 'pinia', 'vue-router'],
  },
  server: {
    port: 5173,
    // The dev server serves only files under the workspace; plancia's
    // sources are outside it.
    ...(planciaSrc || diagramsSrc
      ? {
          fs: {
            allow: [
              searchForWorkspaceRoot(process.cwd()),
              ...[planciaSrc, diagramsSrc].filter(Boolean),
            ],
          },
        }
      : {}),
    proxy: {
      // During dev, proxy API + SSE and the vault's attachments to the Go
      // server so the SPA can run with `npm run dev` while the binary
      // serves data. Not /static/dist: that is this server's own base, and
      // proxying it sent even /src/main.ts to the Go server (a 404).
      // The default is the lab, never the production server: GOSIDIAN_API
      // points it elsewhere (GOSIDIAN_API=http://localhost:8080 npm run dev).
      '/api': devApi,
      '/vault-files': devApi,
    },
  },
  build: {
    target: 'es2022',
    sourcemap: true,
    manifest: true,
    rollupOptions: {
      output: {
        // Split heavy chunks (the editor) so the initial shell
        // stays small on first load. Vite 8 / Rolldown dropped the
        // object form of manualChunks — use the function form (module
        // id → chunk name), which both Rollup and Rolldown accept.
        manualChunks(id) {
          if (/node_modules\/@codemirror\//.test(id)) return 'editor'
        },
      },
    },
  },
})
