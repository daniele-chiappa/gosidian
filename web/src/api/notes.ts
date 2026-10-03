import client from './client'

export interface NoteSummary {
  path: string
  title: string
}

// MediaRef is the resolved payload of a media-style note — an image media
// note (ADR-013, `type: image`) or a CSV table note (ADR-016, `type: table`)
// — populated by the backend when the matching feature flag is on. `broken`
// is true when the `media:` pointer doesn't resolve.
export interface MediaRef {
  path: string
  url: string
  mime?: string
  size?: number
  broken?: boolean
}

export interface Note {
  path: string
  title: string
  content: string
  etag: string
  size: number
  mod_time: string
  kind?: 'image' | 'table'
  media?: MediaRef
}

export interface ListResponse {
  items: NoteSummary[]
  total: number
  limit: number
  offset: number
}

export async function listNotes(
  params: { project?: string; tag?: string; limit?: number } = {},
): Promise<ListResponse> {
  const { data } = await client.get<ListResponse>('/notes', { params })
  return data
}

export async function getNote(path: string, opts?: { inline?: boolean }): Promise<Note> {
  // inline=1 returns the content with image references embedded as data: URIs
  // (self-contained download); the stored note keeps the lightweight reference.
  const { data } = await client.get<Note>(`/notes/${encodeURIComponent(path)}`, {
    params: opts?.inline ? { inline: 1 } : undefined,
  })
  return data
}

export async function createNote(path: string, content: string): Promise<Note> {
  const { data } = await client.post<Note>('/notes', { path, content })
  return data
}

export async function updateNote(
  path: string,
  body: { content: string; ifMatch?: string },
): Promise<Note> {
  const headers: Record<string, string> = {}
  if (body.ifMatch) headers['If-Match'] = body.ifMatch
  const { data } = await client.put<Note>(
    `/notes/${encodeURIComponent(path)}`,
    { content: body.content },
    { headers },
  )
  return data
}

export async function deleteNote(path: string): Promise<void> {
  await client.delete(`/notes/${encodeURIComponent(path)}`)
}

/** A frontmatter value as the API takes it. */
export type FieldValue = string | number | boolean | string[] | null

export interface PatchFrontmatterBody {
  set?: Record<string, FieldValue>
  unset?: string[]
  /** The values last seen, checked field by field: 409 when one changed. */
  expect?: Record<string, FieldValue>
}

/**
 * PATCH /api/v1/notes/{path}/frontmatter — write and remove frontmatter keys
 * of a markdown note; only their lines change (IMP-127 phase 5).
 */
export async function patchFrontmatter(path: string, body: PatchFrontmatterBody): Promise<Note> {
  const { data } = await client.patch<Note>(`/notes/${encodeURIComponent(path)}/frontmatter`, body)
  return data
}

/** The fields of a note as the property panel shows them. */
export interface RowFields {
  /** The database the note is a row of; absent for any other note. */
  database?: string
  source?: string
  /** The schema's fields, in declaration order. */
  columns?: import('./preview').ViewColumn[]
  /** The note's fields the schema does not declare. */
  others?: string[]
  values: Record<string, string | string[]>
  links?: Record<string, import('./preview').ViewLink[]>
  writable: boolean
}

/** GET /api/v1/notes/{path}/fields — a row's fields, for the property panel. */
export async function getRowFields(path: string): Promise<RowFields> {
  const { data } = await client.get<RowFields>(`/notes/${encodeURIComponent(path)}/fields`)
  return data
}
