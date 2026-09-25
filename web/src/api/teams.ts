import client from './client'
import type { GrantLevel, ProjectMember } from './projects'
import type { Visibility } from './access'

/** A team: accounts that share one grant per project. Owner-managed. */
export interface Team {
  id: string
  name: string
  description?: string
  users: TeamUser[]
  grants: TeamGrant[]
  created_at: string
}

export interface TeamUser {
  id: string
  username: string
  role: string
}

export interface TeamGrant {
  project: string
  level: GrantLevel
}

interface ListResp<T> {
  items: T[]
  total: number
}

// --- Admin → Teams (owner-only) ---

export async function listTeams(): Promise<Team[]> {
  const { data } = await client.get<ListResp<Team>>('/admin/teams')
  return data.items
}

export async function createTeam(name: string, description = ''): Promise<Team> {
  const { data } = await client.post<Team>('/admin/teams', { name, description })
  return data
}

export async function updateTeam(id: string, patch: { name?: string; description?: string }): Promise<Team> {
  const { data } = await client.patch<Team>(`/admin/teams/${encodeURIComponent(id)}`, patch)
  return data
}

export async function deleteTeam(id: string): Promise<void> {
  await client.delete(`/admin/teams/${encodeURIComponent(id)}`)
}

export async function addTeamUser(id: string, userId: string): Promise<Team> {
  const { data } = await client.put<Team>(`/admin/teams/${encodeURIComponent(id)}/users`, { user_id: userId })
  return data
}

export async function removeTeamUser(id: string, userId: string): Promise<void> {
  await client.delete(`/admin/teams/${encodeURIComponent(id)}/users/${encodeURIComponent(userId)}`)
}

export async function setTeamGrant(id: string, project: string, level: GrantLevel): Promise<Team> {
  const { data } = await client.put<Team>(`/admin/teams/${encodeURIComponent(id)}/grants`, { project, level })
  return data
}

export async function removeTeamGrant(id: string, project: string): Promise<void> {
  await client.delete(`/admin/teams/${encodeURIComponent(id)}/grants/${encodeURIComponent(project)}`)
}

// --- Project access (owner or project admin) ---

export interface ProjectTeamGrant {
  team_id: string
  name: string
  level: GrantLevel
}

export interface AccessCandidates {
  users: { id: string; username: string; role: string }[]
  teams: { id: string; name: string }[]
}

/** Who holds a grant on a project, directly or through a team. Candidates
 *  (what can still be added) come only for callers who may administer it. */
export interface ProjectAccess {
  project: string
  visibility: Visibility
  users: ProjectMember[]
  teams: ProjectTeamGrant[]
  can_admin: boolean
  candidates?: AccessCandidates
}

export async function getProjectAccess(slug: string): Promise<ProjectAccess> {
  const { data } = await client.get<ProjectAccess>(`/projects/${encodeURIComponent(slug)}/access`)
  return data
}

export async function setProjectTeam(slug: string, teamId: string, level: GrantLevel): Promise<ProjectTeamGrant> {
  const { data } = await client.put<ProjectTeamGrant>(`/projects/${encodeURIComponent(slug)}/teams`, {
    team_id: teamId,
    level,
  })
  return data
}

export async function removeProjectTeam(slug: string, teamId: string): Promise<void> {
  await client.delete(`/projects/${encodeURIComponent(slug)}/teams/${encodeURIComponent(teamId)}`)
}
