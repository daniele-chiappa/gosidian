/** A piece of a rendered note: plain HTML, or the place of a view. */
export type Segment = { kind: 'html'; html: string } | { kind: 'view'; index: number; html: string }

/**
 * splitViews cuts sanitized preview HTML at its top-level view placeholders
 * (`<div class="gosidian-view" data-view="N">`), so that a component can
 * show each view while the rest stays HTML. A placeholder keeps its own HTML
 * as the fallback.
 */
export function splitViews(html: string): Segment[] {
  const tpl = document.createElement('template')
  tpl.innerHTML = html
  const out: Segment[] = []
  let buf = ''
  const flush = () => {
    if (buf) out.push({ kind: 'html', html: buf })
    buf = ''
  }
  const scratch = document.createElement('div')
  for (const node of Array.from(tpl.content.childNodes)) {
    if (
      node instanceof HTMLElement &&
      node.classList.contains('gosidian-view') &&
      node.dataset.view !== undefined
    ) {
      flush()
      out.push({ kind: 'view', index: Number(node.dataset.view), html: node.innerHTML })
      continue
    }
    scratch.replaceChildren(node.cloneNode(true))
    buf += scratch.innerHTML
  }
  flush()
  return out
}
