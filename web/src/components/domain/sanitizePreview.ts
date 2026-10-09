import DOMPurify from 'dompurify'

/**
 * The one barrier between a note's HTML and the page (IMP-128). The
 * server renders markdown with goldmark's raw HTML passing through
 * (WithUnsafe), so a note's `<script>`, `on*=` handlers or `javascript:`
 * links reach the client untouched: whatever shows server-rendered HTML
 * goes through here. The additions keep what the renderer marks up — math,
 * resolved wikilinks (`data-preview-path`), views, headings, embeds.
 *
 * A note's HTML sits in the page itself, so it may not restyle or cover
 * the app (BUG-117, S7-4): a `<style>` hid it, an `<a style="position:
 * fixed; inset: 0">` caught every click, and a form could draw a "session
 * expired" prompt over it. `<style>` and forms are dropped (a task list
 * keeps its checkboxes, disabled); `style` keeps the text's color,
 * background color, weight, slant, alignment and decoration only; `class`
 * keeps the renderer's own classes only, as the app's (`fixed inset-0
 * z-50`, `w-screen`) did the same as a style. The preview is also a
 * containing block of its own (MarkdownPreview).
 */

const purify = DOMPurify(window)

const STYLE_PROPS = new Set([
  'color',
  'background-color',
  'font-weight',
  'font-style',
  'text-align',
  'text-decoration',
  'text-decoration-line',
  'text-decoration-color',
  'text-decoration-style',
])
// A value of names, numbers, `#` colors and color functions: no url(), no
// quotes, no escapes, no `!important`.
const STYLE_VALUE = /^[#a-z0-9 .,%()-]+$/i
const COLOR_FUNCTION = /^(rgba?|hsla?)$/i

/** The declarations of a style attribute a note may keep, "" for none. */
export function filterStyle(style: string): string {
  const kept: string[] = []
  for (const decl of style.split(';')) {
    const i = decl.indexOf(':')
    if (i < 0) continue
    let prop = decl.slice(0, i).trim().toLowerCase()
    const value = decl.slice(i + 1).trim()
    if (prop === 'background') prop = 'background-color' // a color alone passes the value check
    if (!STYLE_PROPS.has(prop) || !STYLE_VALUE.test(value)) continue
    const fns = [...value.matchAll(/([a-z-]*)\s*\(/gi)].map((m) => m[1] ?? '')
    if (fns.some((f) => !COLOR_FUNCTION.test(f))) continue
    kept.push(`${prop}: ${value}`)
  }
  return kept.join('; ')
}

// The classes the server's renderer writes: views, counts, embeds, links,
// tags, callouts, footnotes, the code's language, and chroma's highlighting
// (`chroma`, `lntable` and its token classes of up to four letters, none of
// them a class of the app).
const RENDERER_CLASS =
  /^(gosidian-[\w-]+|wikilink|unresolved|tag|callout(-[\w-]+)?|footnotes?|footnote-[\w-]+|language-[\w+#.-]+|chroma|lntable|lnlinks|math|katex[\w-]*|task-list-item|contains-task-list|[a-z][a-z0-9]{0,3})$/

/** The classes of a class attribute the renderer may have written, "" for none. */
export function filterClass(value: string): string {
  return value
    .split(/\s+/)
    .filter((c) => RENDERER_CLASS.test(c))
    .join(' ')
}

purify.addHook('uponSanitizeAttribute', (_node, data) => {
  const filter = data.attrName === 'style' ? filterStyle : data.attrName === 'class' ? filterClass : null
  if (!filter) return
  const kept = filter(data.attrValue)
  if (kept) data.attrValue = kept
  else data.keepAttr = false
})

purify.addHook('uponSanitizeElement', (node, data) => {
  if (data.tagName !== 'input' || !(node instanceof Element)) return
  if ((node.getAttribute('type') ?? '').toLowerCase() === 'checkbox') node.setAttribute('disabled', '')
  else node.remove()
})

export function sanitizePreviewHtml(html: string): string {
  return purify.sanitize(html, {
    ADD_TAGS: ['math', 'mfrac', 'mrow', 'msup', 'mn', 'mi'],
    ADD_ATTR: ['class', 'data-preview-path', 'data-view', 'data-heading', 'data-embed'],
    FORBID_TAGS: [
      'style',
      'form',
      'button',
      'textarea',
      'select',
      'option',
      'optgroup',
      'datalist',
      'fieldset',
      'output',
      'dialog',
    ],
    FORBID_ATTR: ['formaction', 'form'],
  })
}
