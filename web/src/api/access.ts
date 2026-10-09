import { i18n } from '@/locales'
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
  /** Restricted accounts ignore visibility and see only their grants. */
  restricted: boolean
  can_create_projects: boolean
  /** The account's own project, when it exists. */
  personal_project?: string
  /** True when a delete goes to the trash, false when it is for good. */
  trash?: boolean
  projects: AccessProject[]
}

// Labels in the user's language (IMP-155): the catalogue's `access.*`
// keys, read through the global i18n so a template that calls these follows
// a change of language.
const tr = (key: string) => i18n.global.t(key)
const ROLES = new Set(['owner', 'member', 'guest'])

/** The human label of a stored role value. */
export function roleLabel(role: string | undefined): string {
  return role && ROLES.has(role) ? tr(`access.role.${role}`) : (role ?? '')
}

/** The label of a visibility: Public, Internal, Private. */
export function visibilityLabel(v: Visibility | string): string {
  return tr(`access.visibility.${v}`)
}

/** What a visibility means, for a tooltip. */
export function visibilityHelp(v: Visibility | string): string {
  return tr(`access.visibility_help.${v}`)
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
