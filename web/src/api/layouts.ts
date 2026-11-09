import client from './client'

/** The app of a plugin (IMP-226): a page gosidian frames for the notes of
 *  a type. origin is the one its messages must come from; alt_urls are the
 *  same page at other addresses. */
export interface PluginApp {
  plugin: string
  name: string
  title: string
  description?: string
  url: string
  alt_urls?: string[]
  origin: string
  note_type: string
  bridge: string
  engine?: string
  status: string
}

/** The app at the address on the host gosidian was opened at, which is the
 *  one this browser reaches: the LAN's or a tunnel's, say. Else as it is. */
export function appOnHost(app: PluginApp, hostname: string): PluginApp {
  const url = [app.url, ...(app.alt_urls ?? [])].find((u) => new URL(u).hostname === hostname)
  return url ? { ...app, url, origin: new URL(url).origin } : app
}

/** GET /api/v1/plugins/apps — the apps of the accepted manifests, each at
 *  the address this browser opens. */
export async function listPluginApps(hostname = () => window.location.hostname): Promise<PluginApp[]> {
  const { data } = await client.get<{ apps: PluginApp[] }>('/plugins/apps')
  return (data.apps ?? []).map((a) => appOnHost(a, hostname()))
}

/** A note's design opened: the editor's package, or null for a note without
 *  one yet; the assets whose files the reader may not open. */
export interface OpenedLayout {
  bytes: ArrayBuffer | null
  etag: string
  missing: string[]
  /** Without a design: the page and the page layout it starts from (IMP-232). */
  page?: string
  template?: string
}

const at = (path: string) => `/layouts/${path.split('/').map(encodeURIComponent).join('/')}`

/** GET /api/v1/layouts/{path} — the note put back together for the editor;
 *  with version, a tag, as that version holds it. */
export async function openLayout(path: string, version?: string): Promise<OpenedLayout> {
  const res = await client.get<ArrayBuffer>(at(path), {
    params: version ? { version } : undefined,
    responseType: 'arraybuffer',
    validateStatus: (s) => s === 200 || s === 204,
  })
  const missing = String(res.headers['gosidian-layout-missing'] ?? '')
  // Path-escaped: a header carries no accents.
  const said = (name: string) => {
    const v = String(res.headers[name] ?? '')
    try {
      return v ? decodeURIComponent(v) : undefined
    } catch {
      return undefined
    }
  }
  return {
    bytes: res.status === 204 ? null : res.data,
    etag: String(res.headers['etag'] ?? ''),
    missing: missing ? missing.split(',') : [],
    page: said('gosidian-layout-page'),
    template: said('gosidian-layout-template'),
  }
}

const quoted = (etag: unknown) => `"${String(etag).replace(/^"|"$/g, '')}"`

/** PUT /api/v1/layouts/{path} — the package the editor saved. */
export async function saveLayout(
  path: string,
  bytes: ArrayBuffer,
  etag: string,
): Promise<{ etag: string }> {
  const headers: Record<string, string> = { 'Content-Type': 'application/vnd.designcraft+zip' }
  if (etag) headers['If-Match'] = etag
  const { data } = await client.put<{ etag: string }>(at(path), bytes, { headers })
  return { etag: quoted(data.etag) }
}

/** A piece of a note's text: what stands from the comment that numbers it to
 *  the next one. */
export interface Piece {
  /** Its number, 0 for the blocks before the first comment. */
  n: number
  kind: 'heading' | 'paragraph' | 'image' | 'list' | 'table' | 'code' | 'quote' | 'other'
  text: string
  hash: string
  line: number
  blocks: number
  excluded?: boolean
  duplicate?: boolean
  image?: { src: string; alt?: string; alt_hash?: string; path?: string; described?: string }
}

export interface NotePieces {
  etag: string
  pieces: Piece[]
}

const piecesOf = (data: NotePieces): NotePieces => ({ etag: quoted(data.etag), pieces: data.pieces })

/** GET /api/v1/layouts/{path}/pieces — the pieces of the note's text. */
export async function listPieces(path: string, version?: string): Promise<NotePieces> {
  const { data } = await client.get<NotePieces>(`${at(path)}/pieces`, {
    params: version ? { version } : undefined,
  })
  return piecesOf(data)
}

/** POST /api/v1/layouts/{path}/pieces/number — a comment before the blocks
 *  without one: every one, or those of piece n. */
export async function numberPieces(path: string, etag: string, n?: number): Promise<NotePieces> {
  const headers = etag ? { 'If-Match': quoted(etag) } : undefined
  const { data } = await client.post<NotePieces>(`${at(path)}/pieces/number`, n === undefined ? {} : { n }, {
    headers,
  })
  return piecesOf(data)
}

/** POST /api/v1/layouts/{path}/pieces/reorder — the numbered pieces in that
 *  order, each with the hash it was read with (IMP-231). A 409 says one
 *  changed since. */
export async function reorderPieces(path: string, order: { n: number; hash: string }[]): Promise<NotePieces> {
  const { data } = await client.post<NotePieces>(`${at(path)}/pieces/reorder`, { order })
  return piecesOf(data)
}

/** POST /api/v1/layouts/{path}/pieces/edit — a new text for the piece read
 *  with that hash. A 409 says it changed since. */
export async function editPiece(path: string, n: number, hash: string, text: string): Promise<NotePieces> {
  const { data } = await client.post<NotePieces>(`${at(path)}/pieces/edit`, { n, hash, text })
  return piecesOf(data)
}

/** What the design adds to the note: a text, or an attachment. */
export type NewPiece = { after?: number } & ({ text: string } | { image: string; alt?: string })

/** POST /api/v1/layouts/{path}/pieces — a new piece in the note. */
export async function addPiece(path: string, piece: NewPiece): Promise<NotePieces & { added?: Piece }> {
  const { data } = await client.post<NotePieces & { added?: Piece }>(`${at(path)}/pieces`, piece)
  return { ...piecesOf(data), added: data.added }
}

/** POST /api/v1/layouts/{path}/export — a PDF an app wants kept, which the
 *  note lists in layout_pdfs: its etag changes. With a version (a tag of the
 *  note) it is that version's, by its label; without, the draft's
 *  (IMP-229). */
export async function exportLayout(
  path: string,
  name: string,
  bytes: ArrayBuffer,
  version?: string,
): Promise<{ path: string; label: string; etag: string }> {
  const { data } = await client.post<{ path: string; label: string; etag: string }>(`${at(path)}/export`, bytes, {
    params: version ? { name, version } : { name },
    headers: { 'Content-Type': 'application/pdf' },
  })
  return { path: data.path, label: data.label, etag: quoted(data.etag) }
}

export interface AttachmentInfo {
  path: string
  size: number
  modified: string
  image: boolean
}

/** GET /api/v1/attachments?project=&kind=image — the images of a project. */
export async function listImages(project: string): Promise<AttachmentInfo[]> {
  const { data } = await client.get<{ attachments: AttachmentInfo[] }>('/attachments', {
    params: { project, kind: 'image' },
  })
  return data.attachments ?? []
}

/** Where a vault file is served, which the session's cookie opens. */
export const vaultFileHref = (path: string) =>
  '/vault-files/' + path.split('/').map(encodeURIComponent).join('/')

/** The bytes of an attachment. */
export async function readVaultFile(path: string): Promise<ArrayBuffer> {
  const res = await fetch(vaultFileHref(path), { credentials: 'same-origin' })
  if (!res.ok) throw new Error(`${path}: ${res.status}`)
  return res.arrayBuffer()
}
