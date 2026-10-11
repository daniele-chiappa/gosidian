/**
 * A piece of a rendered note: plain HTML, the place of a view, or a
 * diagram's block.
 */
export type Segment =
  | { kind: 'html'; html: string }
  | { kind: 'view'; index: number; html: string }
  | DiagramSegment

/**
 * A diagram's block. `origin` is the note it was written in when an embed
 * brought it (null when the embed's note did not resolve), undefined when
 * it is the note's own; `occurrence` counts the blocks of the same text
 * before it in that note, so an editor finds it among the note's blocks.
 */
export interface DiagramSegment {
  kind: 'diagram'
  index: number
  source: string
  html: string
  origin?: string | null
  occurrence: number
}

/**
 * The YAML of a top-level `<pre><code class="language-diagram">`, the HTML
 * the renderer makes of a ```diagram block (IMP-169); null for any other node.
 */
function diagramSource(node: Node): string | null {
  if (!(node instanceof HTMLElement) || node.tagName !== 'PRE' || node.children.length !== 1)
    return null
  const code = node.firstElementChild
  if (!code || code.tagName !== 'CODE' || !code.classList.contains('language-diagram')) return null
  return code.textContent ?? ''
}

/** Whether preview HTML may hold a diagram's block, before cutting it. */
export function hasDiagrams(html: string): boolean {
  return html.includes('language-diagram')
}

/**
 * splitViews cuts sanitized preview HTML at its top-level view placeholders
 * (`<div class="gosidian-view" data-view="N">`) and diagram blocks, so that
 * a component can show each view and draw each diagram while the rest stays
 * HTML. A placeholder keeps its own HTML as the fallback; a diagram nested
 * in other markup (a list, a quote) stays its code.
 */
export function splitViews(html: string): Segment[] {
  const tpl = document.createElement('template')
  tpl.innerHTML = html
  const out: Segment[] = []
  let buf = ''
  let diagrams = 0
  const flush = () => {
    if (buf) out.push({ kind: 'html', html: buf })
    buf = ''
  }
  const scratch = document.createElement('div')
  // The note an embed's text comes from, between its head and its end.
  let origin: string | null | undefined
  const seen = new Map<string, number>()
  for (const node of Array.from(tpl.content.childNodes)) {
    if (node instanceof HTMLElement && node.classList.contains('gosidian-embed-start')) {
      origin = node.querySelector('a[data-preview-path]')?.getAttribute('data-preview-path') ?? null
    } else if (node instanceof HTMLElement && node.classList.contains('gosidian-embed-end')) {
      origin = undefined
    }
    if (
      node instanceof HTMLElement &&
      node.classList.contains('gosidian-view') &&
      node.dataset.view !== undefined
    ) {
      flush()
      out.push({ kind: 'view', index: Number(node.dataset.view), html: node.innerHTML })
      continue
    }
    const source = diagramSource(node)
    if (source !== null) {
      flush()
      const key = `${origin === undefined ? '' : `@${origin}`}\u0000${source}`
      const occurrence = seen.get(key) ?? 0
      seen.set(key, occurrence + 1)
      out.push({
        kind: 'diagram',
        index: diagrams++,
        source,
        html: (node as HTMLElement).outerHTML,
        origin,
        occurrence,
      })
      continue
    }
    scratch.replaceChildren(node.cloneNode(true))
    buf += scratch.innerHTML
  }
  flush()
  return out
}
