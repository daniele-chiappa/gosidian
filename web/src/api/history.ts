import client from './client'

export interface HistoryEntry {
  sha: string
  short_sha: string
  /** "name <email>": who wrote the note, with the configured email. */
  author: string
  date: string
  subject: string
  /** The note's path in this commit; from, the one before a rename. */
  path: string
  from?: string
}

/** A version of a note and the diff from it to the note now (IMP-222). */
export interface Version extends HistoryEntry {
  /** The text then; with diff, absent when omitted says why. */
  content?: string
  /** Hunks of a unified diff, "" when the same. */
  diff?: string
  same: boolean
  omitted?: 'deleted' | 'too_large' | 'not_text'
  /** The note's etag the diff was made with: the restore's if_match. */
  etag: string
}

export interface RestoreResult {
  /** false: the version was the note already. */
  restored: boolean
  /** The commit of the restore, the top of the history. */
  commit?: HistoryEntry
  /** Why that commit failed; the note is restored all the same. */
  commit_error?: string
}

interface ListResp {
  items: HistoryEntry[]
  total: number
  limit: number
}

const notePath = (path: string) => `/notes/${encodeURIComponent(path)}`

export async function getHistory(path: string, limit = 50): Promise<HistoryEntry[]> {
  const { data } = await client.get<ListResp>(`${notePath(path)}/history`, { params: { limit } })
  return data.items
}

export async function getVersion(path: string, sha: string): Promise<Version> {
  const { data } = await client.get<Version>(`${notePath(path)}/history/${encodeURIComponent(sha)}`)
  return data
}

export async function restoreVersion(path: string, sha: string, ifMatch: string): Promise<RestoreResult> {
  const { data } = await client.post<RestoreResult>(`${notePath(path)}/restore`, { sha, if_match: ifMatch })
  return data
}
