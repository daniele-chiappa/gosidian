import client from './client'

/** A column of a view, typed by the database's schema when it lists one. */
export interface ViewColumn {
  name: string
  type?: string
  options?: string[]
  required?: boolean
}

/** A wikilink in a field value; path is "" when it names no visible note. */
export interface ViewLink {
  text: string
  path?: string
}

/** A note a view lists; fields as the index reads them. */
export interface ViewRow {
  path: string
  title: string
  modified: string
  fields: Record<string, string | string[]>
  /** The wikilinks of each field's values, in order. */
  links?: Record<string, ViewLink[]>
  writable: boolean
}

/** A computed view in the form the editors use (IMP-127 phase 5). */
/** A group of a count view: the notes whose group_by field has value. */
export interface ViewCount {
  value: string
  count: number
}

export interface ViewData {
  as?: 'table' | 'list' | 'board' | 'count'
  columns?: ViewColumn[]
  group?: ViewColumn
  groups?: string[]
  /** The groups of a count view with group_by. */
  counts?: ViewCount[]
  rows?: ViewRow[]
  total: number
  database?: string
  source?: string
  /** The values a new row made from the view starts with: its filters. */
  defaults?: Record<string, import('./notes').FieldValue>
  /** Whether the reader may add rows to the database. */
  creatable?: boolean
  error?: string
}

export interface PreviewResponse {
  html: string
  views?: ViewData[]
  /** The [[links]] of the note's diagrams, as written (heading included) → the note's path. */
  diagram_links?: Record<string, string>
}

/**
 * POST /api/v1/preview — render markdown to sanitized HTML. `path` is the
 * note being shown, so its ```view blocks can use this.<field>.
 */
export async function renderPreview(markdown: string, path?: string): Promise<string> {
  const { data } = await client.post<PreviewResponse>('/preview', { markdown, path })
  return data.html
}

/**
 * As renderPreview, plus the note's views as data, in the order of the
 * data-view index of their placeholder in the HTML, and the notes the
 * links of its diagrams resolve to.
 */
export async function renderPreviewData(
  markdown: string,
  path?: string,
): Promise<{ html: string; views: ViewData[]; diagramLinks: Record<string, string> }> {
  const { data } = await client.post<PreviewResponse>('/preview', { markdown, path })
  return { html: data.html, views: data.views ?? [], diagramLinks: data.diagram_links ?? {} }
}

/** The rules of the cut view (IMP-170): those of the pipeline's note.sections. */
export interface CutRequest {
  /** <plugin>/<collection>: compare each block with the archive. */
  collection?: string
  max_chars?: number
  context?: 'heading' | 'path' | 'none'
  media_words?: { icon: string; image: string }
  /** The longest side, in pixels, of an image in a sentence that is an
   *  icon (0: every one); omitted, 128, as in the pipeline's node. */
  inline_max_side?: number
}

/** A section, a part of a long one, or a paragraph, as note.sections cuts it. */
export interface CutBlock {
  id: string
  parent?: string
  level: number
  section?: string
  chars: number
  hash: string
  part?: number
  over?: boolean
  /** Against the collection: as stored, stored with another text, or not stored. */
  state?: 'same' | 'changed' | 'missing'
  /** A section's heading, rendered. */
  head?: string
  html: string
}

export interface CutMedia {
  images: number
  icons: number
  described: number
  missing: number
  word_alt: number
}

export interface CutView {
  blocks: CutBlock[]
  sections: number
  paragraphs: number
  media: CutMedia
  max_chars: number
  collection?: string
  stored: number
  orphans: number
  states?: Partial<Record<'same' | 'changed' | 'missing', number>>
  error?: string
}

/** POST /api/v1/preview with cut: the note cut in sections and paragraphs. */
export async function renderCut(markdown: string, path: string, cut: CutRequest): Promise<CutView> {
  const { data } = await client.post<{ cut: CutView }>('/preview', { markdown, path, cut })
  return data.cut
}

/** A collection of an archive of vectors exposed in a project. */
export interface CutCollection {
  source: string
  plugin: string
  collection: string
  model?: string
  count: number
}

/** The collections, and why an archive exposed in the project lists none
 *  (an archive's plugin that does not answer, say). */
export interface CutCollections {
  collections: CutCollection[]
  problems: string[]
}

/** GET /api/v1/cut/collections: what the cut view can compare a note with. */
export async function listCutCollections(project: string): Promise<CutCollections> {
  const { data } = await client.get<Partial<CutCollections>>('/cut/collections', { params: { project } })
  return { collections: data.collections ?? [], problems: data.problems ?? [] }
}
