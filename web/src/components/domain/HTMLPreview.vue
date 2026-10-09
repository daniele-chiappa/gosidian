<script setup lang="ts">
/**
 * HTMLPreview — renders a single-file HTML note (HTML + inline CSS + inline JS)
 * inside a locked-down sandboxed iframe.
 *
 * SECURITY MODEL (ADR-011) — do not weaken without review:
 *  - sandbox="allow-scripts" WITHOUT allow-same-origin. The document runs in an
 *    opaque origin: it cannot read the parent's cookies, localStorage, or DOM,
 *    and cannot make credentialed (same-origin) requests to the gosidian API.
 *    NEVER add allow-same-origin alongside allow-scripts — together they
 *    dissolve the sandbox and reintroduce stored-XSS against the viewer.
 *  - an injected <meta http-equiv="Content-Security-Policy"> with
 *    default-src 'none' blocks ALL network: the note's JS runs but cannot
 *    exfiltrate data or pull remote resources. Notes must be self-contained
 *    (inline CSS/JS, data: images). External assets are blocked by design.
 *
 * IMAGE REFERENCES (IMP-059): a note may reference vault images by URL
 * (`<img src="/vault-files/.../attachments/x.webp">`) instead of inlining them.
 * The iframe CSP only allows `img-src data:`, so we fetch those vault images in
 * the PARENT context (where /vault-files is reachable) and inline them as data:
 * URIs at render time only. The stored note is never modified — the reference
 * form is preserved for MCP reads, downloads and storage (token savings). This
 * stays within ADR-011: the CSP is unchanged, the iframe never reaches the
 * network, and data: images are inert.
 *
 * The HTML is the author's content rendered as-is (NOT sanitized): isolation,
 * not sanitization, is the boundary here.
 *
 * LINKS (BUG-091): an about:srcdoc document resolves relative URLs against
 * the PARENT's URL, so a click on `href="other.html"`, or even on
 * `href="#part"`, navigated the iframe to the SPA's own origin (a 404, or a
 * shell that cannot start in the sandbox). A small script, stamped with the
 * nonce like the note's own, takes the clicks: `#part` scrolls within the
 * page, any other link goes to the parent with postMessage. The parent
 * accepts only its own iframe's messages, sent while the user is
 * interacting, and resolves the link against the note's path: a note opens
 * in a window, an attachment or an external URL in a new tab. Relative
 * images under an attachments/ folder are inlined like /vault-files ones.
 * The iframe still has no network and no same-origin access.
 */
import { useI18n } from 'vue-i18n'
import { inject, ref, watch, onMounted, onBeforeUnmount } from 'vue'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { planciaKey, base } from '@/composables/planciaKey'
import { hasScheme, linkTarget, resolveRelative } from './noteLinks'
import { buildSrcdoc as buildDoc } from './htmlNoteDoc'
import { inlinedImages } from './dataUrlCache'

const { t } = useI18n()

const props = defineProps<{ html: string; path?: string }>()

const store = useWindowsStore()
const openWindow = inject<(spec: OpenSpec) => string>('openWindow', (s) => store.open(s))
const frame = ref<HTMLIFrameElement | null>(null)

// Match <img> src attributes: /vault-files/ paths, and relative ones that
// resolve to a vault attachment.
const imgSrcRe = /(<img\b[^>]*?\bsrc\s*=\s*)(["'])([^"']+)\2/gi

/** The /vault-files URL of an image src, or null when it names no attachment. */
function vaultImageURL(src: string): string | null {
  if (src.startsWith('/vault-files/')) return src
  if (hasScheme(src) || src.startsWith('/') || src.startsWith('#')) return null
  let rel = src.split(/[?#]/)[0] ?? ''
  try {
    rel = decodeURIComponent(rel)
  } catch {
    /* keep it as written */
  }
  const p = resolveRelative(props.path, rel)
  return p && `/${p}`.includes('/attachments/') ? `/vault-files/${p}` : null
}

const dataUrlCache = inlinedImages

async function toDataUrl(url: string): Promise<string | null> {
  const cached = dataUrlCache.get(url)
  if (cached) return cached
  try {
    // The server returns the data: URI directly (?inline), with an immutable
    // Cache-Control — content-addressed images never change, so the browser
    // keeps the base64 forever and we don't regenerate it on every render.
    const sep = url.includes('?') ? '&' : '?'
    const res = await fetch(`${url}${sep}inline=1`)
    if (!res.ok) return null
    const dataUrl = (await res.text()).trim()
    if (!dataUrl.startsWith('data:')) return null
    dataUrlCache.set(url, dataUrl)
    return dataUrl
  } catch {
    return null
  }
}

async function inlineVaultImages(html: string): Promise<string> {
  const urls = new Set<string>()
  for (const m of html.matchAll(imgSrcRe)) {
    const u = m[3] ? vaultImageURL(m[3]) : null
    if (u) urls.add(u)
  }
  if (urls.size === 0) return html
  const resolved = new Map<string, string>()
  await Promise.all(
    [...urls].map(async (u) => {
      const d = await toDataUrl(u)
      if (d) resolved.set(u, d)
    }),
  )
  return html.replace(imgSrcRe, (full, pre, quote, src) => {
    const u = vaultImageURL(src)
    const d = u ? resolved.get(u) : undefined
    return d ? `${pre}${quote}${d}${quote}` : full
  })
}

/** Opens a link of the note: a note in a window, anything else in a new tab. */
function followLink(href: string) {
  const target = linkTarget(href, props.path)
  if (target.kind === 'browser') {
    if (/^mailto:/i.test(target.url)) window.open(target.url, '_blank', 'noopener,noreferrer')
    return
  }
  if (target.kind === 'tab') {
    // The app's own addresses stay out of reach of a sandboxed note: only
    // its attachments, and other sites, open.
    const own = target.url.startsWith('/') && !target.url.startsWith('//')
    if (!own || target.url.startsWith('/vault-files/')) window.open(target.url, '_blank', 'noopener,noreferrer')
    return
  }
  if (target.kind !== 'note') return
  openWindow({
    type: 'note',
    key: planciaKey('note', target.path),
    title: base(target.path),
    props: { path: target.path },
  })
}

// Only messages from this note's own iframe, sent while the user is
// interacting (a click propagates the activation to the parent): a note's
// script cannot open windows on its own.
function onMessage(e: MessageEvent) {
  if (!frame.value || e.source !== frame.value.contentWindow) return
  const d = e.data as { gosidian?: string; href?: unknown } | null
  if (!d || d.gosidian !== 'html-note-link' || typeof d.href !== 'string') return
  const ua = (navigator as Navigator & { userActivation?: { isActive: boolean } }).userActivation
  if (ua && !ua.isActive) return
  followLink(d.href)
}

// The shell is served with a per-request CSP nonce (script-src 'self'
// 'nonce-X'), which an about:srcdoc iframe inherits: see htmlNoteDoc.ts.
function cspNonce(): string {
  return document.querySelector('meta[name="csp-nonce"]')?.getAttribute('content') ?? ''
}
const buildSrcdoc = (html: string) => buildDoc(html, cspNonce())

// Render immediately (text/layout show at once), then swap in the inlined
// version once the vault images have been fetched + converted. Only the
// last rebuild lands: an older one, slower to fetch its images, put a
// previous version of the note back (BUG-117, S7-12).
const srcdoc = ref(buildSrcdoc(props.html ?? ''))
let generation = 0

async function rebuild() {
  const mine = ++generation
  const inlined = await inlineVaultImages(props.html ?? '')
  if (mine === generation) srcdoc.value = buildSrcdoc(inlined)
}

onMounted(() => {
  window.addEventListener('message', onMessage)
  void rebuild()
})
onBeforeUnmount(() => window.removeEventListener('message', onMessage))
watch(
  () => [props.html, props.path],
  () => {
    srcdoc.value = buildSrcdoc(props.html ?? '')
    void rebuild()
  },
)
</script>

<template>
  <iframe
    ref="frame"
    :srcdoc="srcdoc"
    sandbox="allow-scripts"
    referrerpolicy="no-referrer"
    :title="t('note.html_frame')"
    class="w-full min-h-[70vh] h-full border-0 bg-white"
  />
</template>
