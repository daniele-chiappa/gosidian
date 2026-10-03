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
export interface ViewData {
  as?: 'table' | 'list' | 'board'
  columns?: ViewColumn[]
  group?: ViewColumn
  groups?: string[]
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
 * data-view index of their placeholder in the HTML.
 */
export async function renderPreviewData(
  markdown: string,
  path?: string,
): Promise<{ html: string; views: ViewData[] }> {
  const { data } = await client.post<PreviewResponse>('/preview', { markdown, path })
  return { html: data.html, views: data.views ?? [] }
}
