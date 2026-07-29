import client from './client'

export interface Project {
  name: string
  note_count: number
  hidden_from_mcp: boolean
  skip_git_sync: boolean
  /** Public projects are readable by guest-role users; private (default) are
   *  visible to owner/member only. "Public" = visible to all authenticated
   *  users including guests, not anonymous. */
  public: boolean
  /** Opt the project into the shared "global" projects merge at bootstrap.
   *  Only effective when the server master switch (settings.globals_enabled)
   *  is on. */
  use_globals: boolean
  /** Opt the project into local agent-anchor materialisation at bootstrap.
   *  Only effective when the server master switch (settings.anchors_enabled)
   *  is on. */
  use_anchors: boolean
  /** Opt the project into the per-project lint tag vocabulary declared in
   *  <project>/memory/conventions.md frontmatter (tag_vocabulary). No server
   *  master switch — purely per-project. */
  use_tag_vocabulary: boolean
  /** RFC 3339 UTC string of the directory's mtime — proxy for
   *  "last activity" (fs birth time isn't preserved by rsync /
   *  git checkout / container layer copy). Empty when stat failed. */
  mod_time?: string
}

export interface ProjectListResponse {
  items: Project[]
  total: number
}

export interface UpdateProjectRequest {
  new_name?: string
  hidden_from_mcp?: boolean
  skip_git_sync?: boolean
  public?: boolean
  use_globals?: boolean
  use_anchors?: boolean
  use_tag_vocabulary?: boolean
}

export async function listProjects(): Promise<Project[]> {
  const { data } = await client.get<ProjectListResponse>('/projects')
  return data.items
}

export async function getProject(slug: string): Promise<Project> {
  const { data } = await client.get<Project>(`/projects/${encodeURIComponent(slug)}`)
  return data
}

export async function createProject(name: string): Promise<Project> {
  const { data } = await client.post<Project>('/projects', { name })
  return data
}

export async function updateProject(slug: string, body: UpdateProjectRequest): Promise<Project> {
  const { data } = await client.put<Project>(`/projects/${encodeURIComponent(slug)}`, body)
  return data
}

export async function deleteProject(slug: string): Promise<void> {
  await client.delete(`/projects/${encodeURIComponent(slug)}`)
}

// --- Per-project membership ACL (owner-only) ---

export interface ProjectMember {
  user_id: string
  username: string
  level: string // read | write
}

export async function listProjectMembers(slug: string): Promise<ProjectMember[]> {
  const { data } = await client.get<{ items: ProjectMember[]; total: number }>(
    `/projects/${encodeURIComponent(slug)}/members`,
  )
  return data.items
}

export async function setProjectMember(
  slug: string,
  userId: string,
  level: 'read' | 'write',
): Promise<ProjectMember> {
  const { data } = await client.put<ProjectMember>(`/projects/${encodeURIComponent(slug)}/members`, {
    user_id: userId,
    level,
  })
  return data
}

export async function removeProjectMember(slug: string, userId: string): Promise<void> {
  await client.delete(`/projects/${encodeURIComponent(slug)}/members/${encodeURIComponent(userId)}`)
}
