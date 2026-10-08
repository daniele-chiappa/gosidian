import { saveBlob } from './download'
import { getNote, type Note } from './notes'

/** An Obsidian base or canvas: shown read-only, downloaded as the file it is. */
export function isReadOnlyNotePath(path: string): boolean {
  return /\.(base|canvas)$/i.test(path)
}

/**
 * Saves a note as a file. A note downloads as a SELF-CONTAINED copy, its
 * image references inlined as data: URIs (server ?inline), while the stored
 * note keeps the lightweight reference for MCP reads and editing. A base or
 * a canvas downloads as the file it is (YAML, JSON), not as what gosidian
 * shows of it. `loaded` is the note already in memory, if any: its content
 * stands in when the fetch fails.
 */
export async function downloadNote(path: string, loaded?: Note): Promise<void> {
  const filename = path.split('/').pop() || loaded?.title || 'note'
  const mime = filename.toLowerCase().endsWith('.html') ? 'text/html' : 'text/markdown'
  let content: string
  if (isReadOnlyNotePath(path) || loaded?.kind === 'base' || loaded?.kind === 'canvas') {
    content = (loaded ?? (await getNote(path))).source ?? ''
  } else {
    try {
      content = (await getNote(path, { inline: true })).content
    } catch (e) {
      if (!loaded) throw e
      content = loaded.content
    }
  }
  saveBlob(new Blob([content], { type: `${mime};charset=utf-8` }), filename)
}
