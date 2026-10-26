import client from './client'

/**
 * The upload into a project's IN folder (IMP-172 phase 5): a session lists
 * the files, each file arrives in pieces at the offset the server has, the
 * completion places them (a zip unpacked) as one batch. The same files
 * opened again into the same folder are the same session: the way to
 * resume after a reload.
 */

/** What to do with a file whose place is taken. */
export type UploadPolicy = 'replace' | 'keep' | 'skip'

export type UploadState = 'open' | 'completing' | 'done'

export type UploadResultKind = 'new' | 'replaced' | 'renamed' | 'skipped' | 'same' | 'unzipped' | 'dropped' | 'error'

export interface UploadFileResult {
  result: UploadResultKind
  /** The vault path it went to: the file, or a zip's folder. */
  path?: string
  error?: string
  /** A zip's files by result. */
  counts?: Partial<Record<UploadResultKind, number>>
  /** A zip's names left out on purpose: folders, hidden files, links. */
  ignored?: number
  /** The first of a zip's files refused, "name: why". */
  refused?: string[]
}

export interface UploadFileInfo {
  n: number
  /** Relative to the upload's folder. */
  path: string
  size: number
  unzip?: boolean
  modified?: number
  received: number
  sha256?: string
  dropped?: boolean
  /** On opening: its place (a zip's folder) is taken. */
  exists?: boolean
  result?: UploadFileResult
}

export interface UploadInfo {
  id: string
  project: string
  /** Relative to the IN folder. */
  dir: string
  policy: UploadPolicy
  owner: string
  channel: string
  state: UploadState
  created: string
  updated: string
  expires: string
  error?: string
  file_count: number
  bytes: number
  received: number
  chunk_size: number
  max_chunk: number
  /** In a listing: its files are in folders, or all zips to unpack. */
  folders?: boolean
  zips?: boolean
  batches?: string[]
  files?: UploadFileInfo[]
}

export interface UploadFileSpec {
  path: string
  size: number
  unzip?: boolean
  /** Unix milliseconds. */
  modified?: number
}

const at = (project: string, rest = '') => `/in/${encodeURIComponent(project)}/uploads${rest}`

export async function listUploads(project: string): Promise<UploadInfo[]> {
  const { data } = await client.get<{ uploads: UploadInfo[] }>(at(project))
  return data.uploads
}

export async function openUpload(project: string, dir: string, files: UploadFileSpec[], policy?: UploadPolicy): Promise<UploadInfo> {
  const { data } = await client.post<UploadInfo>(at(project), { dir, files, policy })
  return data
}

/** An upload; without files, its files are left out (to follow it). */
export async function getUpload(project: string, id: string, files = true): Promise<UploadInfo> {
  const { data } = await client.get<UploadInfo>(at(project, `/${id}`), files ? {} : { params: { files: false } })
  return data
}

export interface PieceAnswer {
  n: number
  received: number
  size: number
  sha256?: string
}

/** Sends the bytes of file n from offset on; onProgress hears the bytes of
 *  the piece sent so far. */
export async function putPiece(
  project: string,
  id: string,
  n: number,
  offset: number,
  piece: Blob,
  opts: { signal?: AbortSignal; onProgress?: (loaded: number) => void } = {},
): Promise<PieceAnswer> {
  const { data } = await client.put<PieceAnswer>(at(project, `/${id}/files/${n}`), piece, {
    params: { offset },
    headers: { 'Content-Type': 'application/octet-stream' },
    signal: opts.signal,
    onUploadProgress: (e) => opts.onProgress?.(e.loaded),
    // A piece is bytes: no JSON of it.
    transformRequest: [(d: unknown) => d],
  })
  return data
}

export async function completeUpload(project: string, id: string, policy?: UploadPolicy, drop: number[] = []): Promise<UploadInfo> {
  const { data } = await client.post<UploadInfo>(at(project, `/${id}/complete`), { policy, drop })
  return data
}

export async function cancelUpload(project: string, id: string): Promise<void> {
  await client.delete(at(project, `/${id}`))
}
