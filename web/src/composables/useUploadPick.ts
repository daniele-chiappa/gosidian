/**
 * The files chosen for an upload into IN (IMP-172 phase 5), from a file
 * input or from a drop: each with its path under the upload's folder (a
 * folder's files under the folder's name), the files IN would refuse left
 * out and counted, hidden ones (.DS_Store, .git/) apart.
 */
import { pathProblem, type PickedFile } from '@/stores/uploads'

export type PickMode = 'files' | 'folder' | 'zip'

export interface Picked {
  files: PickedFile[]
  /** Hidden files and folders left out. */
  hidden: number
  /** Names IN does not take, left out. */
  refused: string[]
}

function sift(list: PickedFile[]): Picked {
  const out: Picked = { files: [], hidden: 0, refused: [] }
  for (const p of list) {
    const why = pathProblem(p.path)
    if (why === 'hidden') out.hidden++
    else if (why) out.refused.push(p.path)
    else out.files.push({ ...p, path: p.path.normalize('NFC') })
  }
  return out
}

/** The files of a file input: a folder's with their path in it, a zip's
 *  to unpack. */
export function pickedFromInput(list: FileList | File[], mode: PickMode): Picked {
  const files = [...list]
  if (mode === 'zip') {
    return sift(files.filter((f) => /\.zip$/i.test(f.name)).map((f) => ({ file: f, path: f.name, unzip: true })))
  }
  return sift(files.map((f) => ({ file: f, path: (mode === 'folder' && f.webkitRelativePath) || f.name })))
}

function fileOf(e: FileSystemFileEntry): Promise<File> {
  return new Promise((resolve, reject) => e.file(resolve, reject))
}

async function readAll(dir: FileSystemDirectoryEntry): Promise<FileSystemEntry[]> {
  const reader = dir.createReader()
  const out: FileSystemEntry[] = []
  // readEntries gives a hundred at a time, then nothing.
  for (;;) {
    const batch = await new Promise<FileSystemEntry[]>((resolve, reject) => reader.readEntries(resolve, reject))
    if (!batch.length) return out
    out.push(...batch)
  }
}

/**
 * The files of a drop, folders walked. The entries are taken at once: the
 * drop's items are gone once its handler returns. Without the entries API
 * (an old browser) the files dropped, without folders.
 */
export function entriesOfDrop(dt: DataTransfer): FileSystemEntry[] | File[] {
  const items = [...(dt.items ?? [])].filter((i) => i.kind === 'file')
  const entries = items.map((i) => i.webkitGetAsEntry?.()).filter((e): e is FileSystemEntry => !!e)
  return entries.length ? entries : [...dt.files]
}

export async function pickedFromDrop(taken: FileSystemEntry[] | File[]): Promise<Picked> {
  if (!taken.length || taken[0] instanceof File) {
    return sift((taken as File[]).map((f) => ({ file: f, path: f.name })))
  }
  const out: PickedFile[] = []
  let hiddenDirs = 0
  const walk = async (e: FileSystemEntry, prefix: string) => {
    if (e.isFile) {
      out.push({ file: await fileOf(e as FileSystemFileEntry), path: prefix + e.name })
    } else if (e.isDirectory && e.name.startsWith('.')) {
      hiddenDirs++ // a .git/ is not walked to be left out
    } else if (e.isDirectory) {
      for (const c of await readAll(e as FileSystemDirectoryEntry)) await walk(c, `${prefix}${e.name}/`)
    }
  }
  for (const e of taken as FileSystemEntry[]) await walk(e, '')
  const picked = sift(out)
  picked.hidden += hiddenDirs
  return picked
}

/** Many small files go better as a zip: one piece, unpacked on the server. */
export function zipAdvised(files: PickedFile[]): boolean {
  if (files.length < 500) return false
  const bytes = files.reduce((a, p) => a + p.file.size, 0)
  return bytes / files.length < 256 << 10
}
