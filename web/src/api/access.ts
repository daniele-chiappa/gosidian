import client from './client'

/** Who may read a project. Write and admin always come from a grant. */
export type Visibility = 'public' | 'internal' | 'private'

/** An account's effective level on a project. `none` never appears in a
 *  response (unreadable projects are not listed); it is the client-side
 *  default for unknown projects. */
export type AccessLevel = 'none' | 'read' | 'write' | 'admin'

export interface AccessProject {
  name: string
  visibility: Visibility
  level: AccessLevel
  /** Why: "owner", "public", "internal", "grant:<level>". */
  via: string[]
}

export interface AccessView {
  user_id: string
  role: string
  projects: AccessProject[]
}

/** The caller's own effective access to every project they may read. */
export async function getMyAccess(): Promise<AccessView> {
  const { data } = await client.get<AccessView>('/me/access')
  return data
}

/** Owner-only preview of another account's effective access. */
export async function getUserAccess(userId: string): Promise<AccessView> {
  const { data } = await client.get<AccessView>(`/admin/users/${encodeURIComponent(userId)}/access`)
  return data
}

export const LEVEL_RANK: Record<AccessLevel, number> = { none: 0, read: 1, write: 2, admin: 3 }

export const VISIBILITY_LABEL: Record<Visibility, string> = {
  public: 'Public',
  internal: 'Internal',
  private: 'Private',
}

export const VISIBILITY_HELP: Record<Visibility, string> = {
  public: 'Public — readable by every signed-in account, guests included. Writing still takes a grant.',
  internal: 'Internal — readable by every member account. Writing still takes a grant.',
  private: 'Private — only accounts holding a grant (and the owner) can see it.',
}
