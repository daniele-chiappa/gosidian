import { saveBlob } from '@/api/download'

// The exports of a diagram (IMP-169): its drawing as SVG, its Mermaid, and
// a flow as an Obsidian canvas. The diagram block and the diagram window
// share them, and the file is named after the diagram's title.

export type DiagramExport = 'svg' | 'mermaid' | 'canvas'

/** The file name of an export: the diagram's title, else «diagram». */
export function exportName(source: string, ext: string): string {
  const title = /^title:\s*["']?(.+?)["']?\s*$/m.exec(source)?.[1] ?? ''
  const base = title
    .normalize('NFKD')
    .replace(/[^\w\s-]/g, '')
    .trim()
    .replace(/\s+/g, '-')
    .toLowerCase()
  return `${base || 'diagram'}.${ext}`
}

/** The kinds a canvas takes: a flow. */
export const canvasKind = (source: string) => /^kind:\s*["']?flow["']?\s*$/m.test(source)

/**
 * Saves an export of a diagram's YAML: `svg` is the drawing the view made
 * (exportSVG), the others are written from the YAML. False when there is
 * nothing to save (no drawing, a diagram that does not read).
 */
export async function exportDiagram(
  source: string,
  as: DiagramExport,
  svg?: () => string | null | Promise<string | null>,
): Promise<boolean> {
  if (as === 'svg') {
    const out = await svg?.()
    if (!out) return false
    saveBlob(new Blob([out], { type: 'image/svg+xml' }), exportName(source, 'svg'))
    return true
  }
  const lib = await import('./diagramView')
  const d = lib.parseDiagram(source).diagram
  if (!d) return false
  if (as === 'mermaid') {
    const text = lib.toMermaid(d)
    if (!text) return false
    saveBlob(new Blob([text], { type: 'text/plain' }), exportName(source, 'mmd'))
    return true
  }
  if (d.kind !== 'flow') return false
  saveBlob(new Blob([lib.toCanvas(d)], { type: 'application/json' }), exportName(source, 'canvas'))
  return true
}
