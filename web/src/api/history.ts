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

/** A version of a note that has a name (IMP-201). */
export interface NoteTag {
  /** The whole tag; label is what people call it, V3. */
  tag: string
  label: string
  sha: string
  short_sha: string
  by?: string
  date: string
  message?: string
  /** The note's path in that version. */
  path: string
}

/** The version readers must see: the note as it is is then the draft. */
export interface ValidVersion {
  tag: string
  label: string
  sha?: string
  short_sha?: string
  date?: string
  /** The note as it is is that version: no draft to tell about. */
  same: boolean
  /** The tag gives no text: the reader sees the note as it is. */
  missing?: boolean
}

/** What a note gives who calls over MCP (IMP-228): tag is a tag of the note,
 *  or "valid" for the version readers see, label the version that makes. */
export interface MCPVersion {
  tag: string
  label?: string
  /** It gives no text: they read the note as it is. */
  missing?: boolean
}

export interface Versions {
  items: NoteTag[]
  valid: ValidVersion | null
  mcp?: MCPVersion | null
  can_admin: boolean
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

export async function getVersions(path: string): Promise<Versions> {
  const { data } = await client.get<Versions>(`${notePath(path)}/versions`)
  return data
}

/** Names a version of the note, a commit of its history. */
export async function tagVersion(path: string, label: string, sha: string): Promise<NoteTag> {
  const { data } = await client.post<{ tag: NoteTag }>(`${notePath(path)}/versions`, { label, sha })
  return data.tag
}

export async function setValidVersion(path: string, tag: string): Promise<void> {
  await client.put(`${notePath(path)}/valid`, { tag })
}

export async function clearValidVersion(path: string): Promise<void> {
  await client.delete(`${notePath(path)}/valid`)
}

export async function setMCPVersion(path: string, tag: string): Promise<void> {
  await client.put(`${notePath(path)}/mcp-version`, { tag })
}

export async function clearMCPVersion(path: string): Promise<void> {
  await client.delete(`${notePath(path)}/mcp-version`)
}
