import client from './client'

/** The colours of a mark (IMP-188). */
export type MarkColor = 'blue' | 'green' | 'orange' | 'red' | 'cyan' | 'grey'

/**
 * A span of a note a pipeline marked: a chunk of note.sections by its
 * base, else a section by the path of its headings, whole or its paragraph
 * `block` (from 1).
 */
export interface Mark {
  base?: string
  section?: string
  block?: number
  color: MarkColor
  label?: string
  text?: string
}

/** The marks a run put on a note; stale when the note changed since. */
export interface MarkSet {
  path: string
  set: string
  pipeline?: string
  run?: string
  hash: string
  created: string
  marks: Mark[]
  stale: boolean
}

/** GET /api/v1/marks: the sets of marks of a note. */
export async function listMarks(path: string): Promise<MarkSet[]> {
  const { data } = await client.get<{ sets: MarkSet[] }>('/marks', { params: { path } })
  return data.sets ?? []
}
