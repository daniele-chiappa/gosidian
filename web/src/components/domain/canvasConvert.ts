import { isAxiosError } from 'axios'
import { createNote, type CanvasData } from '@/api/notes'

// An Obsidian canvas made a diagram note (IMP-169, F3): the canvas stays as
// it is, read-only, and a note type: diagram beside it holds a flow of its
// cards (plancia-diagrams' fromCanvas), at their places.

/** The YAML of a quoted frontmatter value. */
const yamlText = (s: string) => JSON.stringify(s)

/**
 * Makes the diagram note of the canvas at `path`: `<name>.md` beside it, or
 * `<name>-diagram.md` (then -2, -3…) when that name is taken. Returns the
 * note's path and what the conversion left out.
 */
export async function convertCanvas(
  path: string,
  canvas: CanvasData,
): Promise<{ path: string; warnings: string[] }> {
  const lib = await import('./diagramView')
  const name = path
    .split('/')
    .pop()!
    .replace(/\.canvas$/i, '')
  const r = lib.fromCanvas(JSON.stringify({ nodes: canvas.nodes, edges: canvas.edges }))
  const content = `---\ntitle: ${yamlText(name)}\ntype: diagram\n---\n\n\`\`\`diagram\n${r.source}\`\`\`\n`
  const dir = path.slice(0, path.length - name.length - '.canvas'.length)
  const candidates = [`${dir}${name}.md`, `${dir}${name}-diagram.md`]
  for (let i = 2; i <= 9; i++) candidates.push(`${dir}${name}-diagram-${i}.md`)
  for (const p of candidates) {
    try {
      await createNote(p, content)
      return { path: p, warnings: r.warnings }
    } catch (e) {
      if (!(isAxiosError(e) && e.response?.status === 409)) throw e
    }
  }
  throw new Error(`${candidates[candidates.length - 1]} exists already`)
}
