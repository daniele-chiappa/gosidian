/**
 * A piece of a rendered note: plain HTML, the place of a view, a diagram's
 * block, or a Mermaid block that may become one.
 */
export type Segment =
  | { kind: 'html'; html: string }
  | { kind: 'view'; index: number; html: string }
  | DiagramSegment
  | MermaidSegment
  | PipelineSegment
  | GraphSegment
  | ChatSegment

/** The ```chat block of a bot's note (IMP-191). */
export interface ChatSegment {
  kind: 'chat'
  index: number
  source: string
  html: string
}

/** A ```graph block at the top level (IMP-183): a saved view of the graph. */
export interface GraphSegment {
  kind: 'graph'
  index: number
  source: string
  html: string
}

/**
 * The ```pipeline block of a pipeline note, at the top level: drawn, with
 * «Edit» for who may change the note it is written in (`origin` as a
 * diagram's). A note has one.
 */
export interface PipelineSegment {
  kind: 'pipeline'
  index: number
  source: string
  html: string
  origin?: string | null
}

/**
 * A ```mermaid block at the top level that is no diagram's twin: shown as
 * its code, it may be converted to a diagram. `origin` and `occurrence` as
 * a diagram's, among the note's Mermaid blocks of the same text.
 */
export interface MermaidSegment {
  kind: 'mermaid'
  index: number
  source: string
  html: string
  origin?: string | null
  occurrence: number
}

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
function diagramSource(node: Node, lang = 'diagram'): string | null {
  if (!(node instanceof HTMLElement) || node.tagName !== 'PRE' || node.children.length !== 1)
    return null
  const code = node.firstElementChild
  if (!code || code.tagName !== 'CODE' || !code.classList.contains(`language-${lang}`)) return null
  return code.textContent ?? ''
}

/** The first line of a diagram's Mermaid twin (plancia-diagrams' TWIN_MARK). */
const TWIN_MARK = '%% diagram-twin '

/** Whether preview HTML may hold a diagram's block, before cutting it. */
export function hasDiagrams(html: string): boolean {
  return html.includes('language-diagram')
}

/** Whether preview HTML may hold a pipeline's block. */
export function hasPipeline(html: string): boolean {
  return html.includes('language-pipeline')
}

/** Whether preview HTML may hold a graph's block. */
export function hasGraph(html: string): boolean {
  return html.includes('language-graph')
}

/** Whether preview HTML may hold a bot's block. */
export function hasChat(html: string): boolean {
  return html.includes('language-chat')
}

/** Whether preview HTML may hold a Mermaid block. */
export function hasMermaid(html: string): boolean {
  return html.includes('language-mermaid')
}

/**
 * splitViews cuts sanitized preview HTML at its top-level view placeholders
 * (`<div class="gosidian-view" data-view="N">`) and diagram blocks, so that
 * a component can show each view and draw each diagram while the rest stays
 * HTML. A placeholder keeps its own HTML as the fallback; a diagram nested
 * in other markup (a list, a quote) stays its code. A diagram's Mermaid
 * twin, right after it, is left out (the diagram is drawn); another
 * top-level Mermaid block is a segment of its own.
 */
export function splitViews(html: string): Segment[] {
  const tpl = document.createElement('template')
  tpl.innerHTML = html
  const out: Segment[] = []
  let buf = ''
  let diagrams = 0
  let mermaids = 0
  let pipelines = 0
  let graphs = 0
  let chats = 0
  // Whether the last thing kept was a diagram's block: a twin follows it.
  let afterDiagram = false
  const flush = () => {
    if (buf) out.push({ kind: 'html', html: buf })
    buf = ''
  }
  const scratch = document.createElement('div')
  // The note an embed's text comes from, between its head and its end.
  let origin: string | null | undefined
  const seen = new Map<string, number>()
  const seenMermaid = new Map<string, number>()
  for (const node of Array.from(tpl.content.childNodes)) {
    if (node.nodeType === Node.TEXT_NODE && !(node.textContent ?? '').trim()) {
      buf += node.textContent ?? ''
      continue
    }
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
      afterDiagram = true
      continue
    }
    const pipelineSrc = diagramSource(node, 'pipeline')
    if (pipelineSrc !== null) {
      flush()
      out.push({
        kind: 'pipeline',
        index: pipelines++,
        source: pipelineSrc,
        html: (node as HTMLElement).outerHTML,
        origin,
      })
      afterDiagram = false
      continue
    }
    const graphSrc = diagramSource(node, 'graph')
    if (graphSrc !== null) {
      flush()
      out.push({ kind: 'graph', index: graphs++, source: graphSrc, html: (node as HTMLElement).outerHTML })
      afterDiagram = false
      continue
    }
    const chatSrc = diagramSource(node, 'chat')
    if (chatSrc !== null) {
      flush()
      out.push({ kind: 'chat', index: chats++, source: chatSrc, html: (node as HTMLElement).outerHTML })
      afterDiagram = false
      continue
    }
    const mermaid = diagramSource(node, 'mermaid')
    if (mermaid !== null) {
      if (afterDiagram && mermaid.startsWith(TWIN_MARK)) {
        afterDiagram = false
        continue
      }
      flush()
      const key = `${origin === undefined ? '' : `@${origin}`}\u0000${mermaid}`
      const occurrence = seenMermaid.get(key) ?? 0
      seenMermaid.set(key, occurrence + 1)
      out.push({
        kind: 'mermaid',
        index: mermaids++,
        source: mermaid,
        html: (node as HTMLElement).outerHTML,
        origin,
        occurrence,
      })
      afterDiagram = false
      continue
    }
    afterDiagram = false
    scratch.replaceChildren(node.cloneNode(true))
    buf += scratch.innerHTML
  }
  flush()
  return out
}
