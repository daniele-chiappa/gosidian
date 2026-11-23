import client from './client'
import type { OfficeEditAnswer, OfficeEditBody } from '@/composables/officeBridge'

/** What the steps of a team's runs spent today (IMP-195). */
export interface FleetSpend {
  cost_usd: number
  tokens: number
  steps: number
}

/** An open task of a role on the office's map (IMP-233). */
export interface FleetRoleTask {
  path: string
  title: string
  status: string
  waits: string
}

/** A role's state, the first that holds: a task waits for a person, one
 *  waits for a budget or a limit, a run holds one, one is ready, none. */
export type FleetRoleState = 'waiting' | 'held' | 'working' | 'queued' | 'idle'

export interface FleetRole {
  state: FleetRoleState
  agent?: { path: string; title: string; missing: boolean }
  tasks: FleetRoleTask[]
  spent_today?: FleetSpend
}

/** GET /api/v1/fleet/teams/{path}: a team of agents as the server reads it
 *  (docs/plugins.md, the bridge office/1). The window hands it to the
 *  office's page as it is: the team's own fields stay opaque here. */
export interface FleetTeamView {
  path: string
  title: string
  line: number
  team: Record<string, unknown> | null
  problems: { line?: number; message: string; role?: string; room?: string }[]
  pipelines: boolean
  spent_today?: FleetSpend
  roles: Record<string, FleetRole>
  now: string
  /** The note's version, and whether the reader may change the team. */
  etag?: string
  editable?: boolean
}

export async function getFleetTeam(path: string): Promise<FleetTeamView> {
  const { data } = await client.get<FleetTeamView>(`/fleet/teams/${path.split('/').map(encodeURIComponent).join('/')}`)
  return data
}

/** POST /api/v1/fleet/teams/{path}: a draft of the office's map (IMP-233,
 *  phase C), checked, or written when save. */
export async function editFleetTeam(path: string, body: OfficeEditBody): Promise<OfficeEditAnswer> {
  const { data } = await client.post<OfficeEditAnswer>(`/fleet/teams/${path.split('/').map(encodeURIComponent).join('/')}`, body)
  return data
}
