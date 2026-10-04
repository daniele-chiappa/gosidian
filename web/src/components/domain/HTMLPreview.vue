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
import { inject, ref, watch, onMounted, onBeforeUnmount } from 'vue'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { planciaKey } from '@/composables/planciaKey'

const props = defineProps<{ html: string; path?: string }>()

const store = useWindowsStore()
const openWindow = inject<(spec: OpenSpec) => string>('openWindow', (s) => store.open(s))
const frame = ref<HTMLIFrameElement | null>(null)

// Restrictive policy injected INTO the iframe document. Allows inline script and
// style (the note's own), data: images/fonts/media, and nothing over the
// network. Mirrors the "single self-contained file" contract.
const INJECTED_CSP = [
  "default-src 'none'",
  "script-src 'unsafe-inline'",
  "style-src 'unsafe-inline'",
  'img-src data:',
  'font-src data:',
  'media-src data:',
].join('; ')

const META = `<meta http-equiv="Content-Security-Policy" content="${INJECTED_CSP}">`

// Match <img> src attributes: /vault-files/ paths, and relative ones that
// resolve to a vault attachment.
const imgSrcRe = /(<img\b[^>]*?\bsrc\s*=\s*)(["'])([^"']+)\2/gi

/** The folder of the note, "" without a path. */
function noteDir(): string {
  const p = props.path ?? ''
  const i = p.lastIndexOf('/')
  return i >= 0 ? p.slice(0, i) : ''
}

/** Resolves a relative path against the note's folder; null when it climbs out of the vault. */
function resolveRelative(rel: string): string | null {
  const parts = noteDir() ? noteDir().split('/') : []
  for (const seg of rel.split('/')) {
    if (seg === '' || seg === '.') continue
    if (seg === '..') {
      if (parts.length === 0) return null
      parts.pop()
    } else {
      parts.push(seg)
    }
  }
  return parts.join('/')
}

const hasScheme = (u: string) => /^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(u)

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
  const p = resolveRelative(rel)
  return p && `/${p}`.includes('/attachments/') ? `/vault-files/${p}` : null
}

const dataUrlCache = new Map<string, string>()

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

// Strip a leading frontmatter block so it never renders as visible text. The
// HTML-comment form (<!-- --- ... --- -->, ADR-011) is already invisible, but
// the bare markdown form (--- ... ---) shows — drop either.
function stripFrontmatter(html: string): string {
  return html
    .replace(/^\s*<!--\s*\r?\n---[\s\S]*?---\s*\r?\n?\s*-->\s*/, '')
    .replace(/^\s*---\r?\n[\s\S]*?\r?\n---\r?\n?/, '')
}

// The shell is served with a per-request CSP nonce (script-src 'self'
// 'nonce-X'). An about:srcdoc iframe INHERITS that policy, which intersects
// away the injected 'unsafe-inline' above — so a note's inline <script> only
// runs if it carries the nonce. Read it from the shell <meta> and stamp it
// onto every <script> tag (BUG-019). Absent (e.g. `npm run dev`), leave the
// markup untouched and the dev shell's looser CSP applies.
function cspNonce(): string {
  return document.querySelector('meta[name="csp-nonce"]')?.getAttribute('content') ?? ''
}

function stampScriptNonce(html: string, nonce: string): string {
  if (!nonce) return html
  return html.replace(/<script(?=[\s>])/gi, `<script nonce="${nonce}"`)
}

// LINK_SCRIPT runs inside the iframe and takes the clicks on links (see
// LINKS above). It runs after the note's own handlers (bubbling), so a link
// a note's script already handles is left alone.
const LINK_SCRIPT = `(function(){document.addEventListener('click',function(e){
if(e.defaultPrevented||e.button!==0)return;
var a=e.target&&e.target.closest?e.target.closest('a[href]'):null;if(!a)return;
var h=a.getAttribute('href')||'';if(/^javascript:/i.test(h))return;
e.preventDefault();
if(h.charAt(0)==='#'){var id=h.slice(1);try{id=decodeURIComponent(id)}catch(_){}
var el=document.getElementById(id)||document.getElementsByName(id)[0];if(el)el.scrollIntoView();return;}
parent.postMessage({gosidian:'html-note-link',href:h},'*');});})();`

function buildSrcdoc(rawHtml: string): string {
  const nonce = cspNonce()
  const html = stampScriptNonce(stripFrontmatter(rawHtml), nonce)
  // The closing tag is split so it does not close this component's own
  // <script> block.
  const head = `${META}<script${nonce ? ` nonce="${nonce}"` : ''}>${LINK_SCRIPT}<` + '/script>'
  // Inject the CSP meta as early as possible so it governs everything that
  // follows. Place it inside an existing <head>, else after <html>, else wrap
  // the fragment in a minimal document.
  if (/<head[^>]*>/i.test(html)) {
    return html.replace(/<head[^>]*>/i, (m) => `${m}${head}`)
  }
  if (/<html[^>]*>/i.test(html)) {
    return html.replace(/<html[^>]*>/i, (m) => `${m}<head>${head}</head>`)
  }
  return `<!DOCTYPE html><html><head>${head}</head><body>${html}</body></html>`
}

/** Opens a link of the note: a note in a window, anything else in a new tab. */
function followLink(href: string) {
  if (/^https?:\/\//i.test(href) || /^mailto:/i.test(href)) {
    window.open(href, '_blank', 'noopener,noreferrer')
    return
  }
  if (hasScheme(href)) return
  const [rawPath] = href.split(/[?#]/)
  let target = rawPath ?? ''
  try {
    target = decodeURIComponent(target)
  } catch {
    /* keep it as written */
  }
  let notePath: string | null
  if (target.startsWith('/notes/')) notePath = target.slice('/notes/'.length)
  else if (target.startsWith('/vault-files/')) {
    window.open(target, '_blank', 'noopener,noreferrer')
    return
  } else if (target.startsWith('/')) return
  else notePath = resolveRelative(target)
  if (!notePath) return
  if (!/\.(md|html)$/i.test(notePath)) {
    if (`/${notePath}`.includes('/attachments/')) {
      window.open(`/vault-files/${notePath}`, '_blank', 'noopener,noreferrer')
      return
    }
    notePath += '.md'
  }
  openWindow({
    type: 'note',
    key: planciaKey('note', notePath),
    title: (notePath.split('/').pop() ?? notePath).replace(/\.(md|html)$/i, ''),
    props: { path: notePath },
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

// Render immediately (text/layout show at once), then swap in the inlined
// version once the vault images have been fetched + converted.
const srcdoc = ref(buildSrcdoc(props.html ?? ''))

async function rebuild() {
  const inlined = await inlineVaultImages(props.html ?? '')
  srcdoc.value = buildSrcdoc(inlined)
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
    title="HTML note"
    class="w-full min-h-[70vh] h-full border-0 bg-white"
  />
</template>
