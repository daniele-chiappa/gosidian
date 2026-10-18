import { downloadFile, saveBlob } from './download'
import { getNote, type Note } from './notes'

/** An Obsidian base or canvas: shown read-only, downloaded as the file it is. */
export function isReadOnlyNotePath(path: string): boolean {
  return /\.(base|canvas)$/i.test(path)
}

/**
 * Saves a note as a file. The server decides the form (IMP-204): a note
 * that names attachments comes as a zip, the note with its references made
 * relative and the files under attachments/; one that names none comes as
 * the file it is. A base or a canvas downloads as the file it is (YAML,
 * JSON), not as what gosidian shows of it. `loaded` is the note already in
 * memory, if any: its content stands in when the download fails.
 */
export async function downloadNote(path: string, loaded?: Note): Promise<void> {
  const filename = path.split('/').pop() || loaded?.title || 'note'
  if (isReadOnlyNotePath(path) || loaded?.kind === 'base' || loaded?.kind === 'canvas') {
    const content = (loaded ?? (await getNote(path))).source ?? ''
    saveBlob(new Blob([content], { type: 'text/plain;charset=utf-8' }), filename)
    return
  }
  try {
    await downloadFile(`/notes/${encodeURIComponent(path)}?export=zip`, filename)
  } catch (e) {
    if (!loaded) throw e
    const mime = filename.toLowerCase().endsWith('.html') ? 'text/html' : 'text/markdown'
    saveBlob(new Blob([loaded.content], { type: `${mime};charset=utf-8` }), filename)
  }
}
