import client from './client'
import type { RunBrief } from './pipelines'

/**
 * The IN folders of the projects (IMP-172): files to work on, under their
 * own names, which the pipelines take in. Paths are vault paths
 * (<project>/IN/…); the API also takes them relative to the IN folder.
 */

export type INStatus = 'new' | 'working' | 'done' | 'error' | 'ignored'

export const IN_STATUSES: INStatus[] = ['new', 'working', 'done', 'error', 'ignored']

export interface INFile {
  project: string
  path: string
  name: string
  stem: string
  ext: string
  /** Its folder, relative to the IN folder ("" at its root). */
  dir: string
  size: number
  modified: string
  sha256?: string
  status: INStatus
  run?: string
  source: string
  batch?: string
  first_seen: string
  updated: string
}

export interface INFolder {
  /** Relative to the IN folder. */
  path: string
  files: number
  bytes: number
  new: number
}

export interface INCounts {
  files: number
  bytes: number
  new: number
  working: number
  errors: number
}

export interface INListing {
  project: string
  /** The IN folder's vault path. */
  root: string
  dir: string
  counts: INCounts
  folders: INFolder[]
  files: INFile[]
  next: string
  track_in_git: boolean
}

export interface INQuery {
  dir?: string
  /** Every file under dir, without its subfolders. */
  recursive?: boolean
  match?: string
  kinds?: string
  status?: INStatus | ''
  limit?: number
  cursor?: string
}

const at = (project: string, action = '') =>
  `/in/${encodeURIComponent(project)}${action ? '/' + action : ''}`

export async function listIN(project: string, q: INQuery = {}): Promise<INListing> {
  const params: Record<string, string> = {}
  for (const [k, v] of Object.entries(q)) {
    if (v !== undefined && v !== '' && v !== false) params[k] = String(v)
  }
  const { data } = await client.get<INListing>(at(project), { params })
  return data
}

export async function moveIN(project: string, from: string, to: string): Promise<{ path: string; from: string }> {
  const { data } = await client.post<{ path: string; from: string }>(at(project, 'move'), { from, to })
  return data
}

export async function setINStatus(project: string, paths: string[], status: INStatus): Promise<void> {
  await client.post(at(project, 'status'), { paths, status })
}

export async function deleteIN(project: string, paths: string[]): Promise<{ removed: string[]; trash: boolean }> {
  const { data } = await client.post<{ removed: string[]; trash: boolean }>(at(project, 'delete'), { paths })
  return data
}

/** A file chosen that the pipeline's trigger does not take. */
export interface INSkipped {
  path: string
  reason: 'folder' | 'kinds' | 'match'
  /** What the trigger says: its folder, kinds or glob. */
  value: string
  why: string
}

/** Starts a pipeline on files, the way its trigger would: a run per file
 *  for an in pipeline per file, one run otherwise. */
export async function runIN(project: string, pipeline: string, paths: string[]): Promise<{ runs: RunBrief[]; skipped: INSkipped[] }> {
  const { data } = await client.post<{ runs: RunBrief[]; skipped: INSkipped[] }>(at(project, 'run'), { pipeline, paths })
  return data
}

/** Where a file of an IN folder is served: /vault-files/, which the
 *  session's cookie opens (ADR-022). */
export function inFileHref(path: string): string {
  return '/vault-files/' + path.split('/').map(encodeURIComponent).join('/')
}
