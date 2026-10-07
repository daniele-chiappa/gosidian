import { defineConfig, searchForWorkspaceRoot } from 'vite'
import vue from '@vitejs/plugin-vue'
import VueI18nPlugin from '@intlify/unplugin-vue-i18n/vite'
import { resolve } from 'node:path'
import { fileURLToPath, URL } from 'node:url'

// Developing plancia alongside gosidian: with PLANCIA_SRC set to the src/
// folder of a plancia checkout, `plancia` resolves to those sources instead
// of the release pinned in package.json, so a change there shows at once in
// `npm run dev` and in the unit tests, with no release in between. Builds
// without the variable (the Dockerfile, CI) use the release. See README.md.
const planciaSrc = process.env.PLANCIA_SRC ? resolve(process.env.PLANCIA_SRC) : ''

// gosidian SPA Vite config.
//
// Build output is written into the Go server's embedded FS at
// internal/server/web/dist via --outDir at npm run build time. Asset
// URLs are rooted at /static/dist/ so the embed handler can serve
// them with aggressive cache headers; the generated index.html and
// HTML shell stays at /, served by handlers_spa.go.
export default defineConfig({
  plugins: [
    vue(),
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
    ],
    // One Vue, Pinia and router: plancia's sources must not pick up the
    // copies in its own node_modules.
    dedupe: ['vue', 'pinia', 'vue-router'],
  },
  server: {
    port: 5173,
    // The dev server serves only files under the workspace; plancia's
    // sources are outside it.
    ...(planciaSrc ? { fs: { allow: [searchForWorkspaceRoot(process.cwd()), planciaSrc] } } : {}),
    proxy: {
      // During dev, proxy API + SSE and the vault's attachments to the Go
      // server so the SPA can run with `npm run dev` while the binary
      // serves data. Not /static/dist: that is this server's own base, and
      // proxying it sent even /src/main.ts to the Go server (a 404).
      '/api': 'http://127.0.0.1:8080',
      '/vault-files': 'http://127.0.0.1:8080',
    },
  },
  build: {
    target: 'es2022',
    sourcemap: true,
    manifest: true,
    rollupOptions: {
      output: {
        // Split heavy chunks (graph, editor) so the initial shell
        // stays small on first load. Vite 8 / Rolldown dropped the
        // object form of manualChunks — use the function form (module
        // id → chunk name), which both Rollup and Rolldown accept.
        manualChunks(id) {
          if (/node_modules\/cytoscape(-fcose)?\//.test(id)) return 'graph'
          if (/node_modules\/@codemirror\//.test(id)) return 'editor'
        },
      },
    },
  },
})
