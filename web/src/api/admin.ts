import client from './client'

export interface MCPToken {
  id: string
  name: string
  project?: string
  scopes: string[]
  owner_user_id?: string
  created_at: string
  expires_at?: string
  expired?: boolean
  self_improve_opt_in: boolean
  /** Multi-project scope; absent = inherit (owner: unscoped). */
  projects?: string[]
  tool_profile?: string
  /** "" static bearer | "oauth" grant minted by a consent. */
  kind?: string
  client_id?: string
}

export interface MCPTokenCreated {
  token: string
  record: MCPToken
  usage_hint: string
}

export interface CreateMCPTokenRequest {
  name: string
  project?: string
  scopes: string[]
  ttl_ms?: number
}

export interface SpaToken {
  id: string
  user_id: string
  user_agent?: string
  issued_at: string
  expires_at: string
  hard_expiry: string
  last_seen_at: string
}

export interface AdminUser {
  id: string
  username: string
  role: string
  totp_policy?: string // "" inherit | enabled | disabled
  totp_enrolled?: boolean
  created_at: string
  disabled_at?: string
  /** Restricted accounts ignore visibility and see only their grants. */
  restricted: boolean
  can_create_projects: boolean
  /** The account's own project, when it exists. */
  personal_project?: string
  /** Effective access summary; absent for the owner (everything). */
  projects_readable?: number
  projects_writable?: number
}

export interface Invite {
  token: string
  created_by: string
  created_at: string
  expires_at: string
  consumed_by?: string
  consumed_at?: string
  pending: boolean
}

export interface AuditEntry {
  ts: string
  source: string
  token?: string
  actor?: string
  user_id?: string
  action: string
  path?: string
  to?: string
  size?: number
}

export interface AuditQuery {
  actor?: string
  user_id?: string
  action?: string
  source?: string
  path_prefix?: string
  since?: string
  until?: string
  limit?: number
}

interface ListResp<T> {
  items: T[]
  total: number
  limit?: number
}

// MCP tokens

export async function listMCPTokens(): Promise<MCPToken[]> {
  const { data } = await client.get<ListResp<MCPToken>>('/admin/tokens')
  return data.items
}

export async function createMCPToken(body: CreateMCPTokenRequest): Promise<MCPTokenCreated> {
  const { data } = await client.post<MCPTokenCreated>('/admin/tokens', body)
  return data
}

export async function revokeMCPToken(id: string): Promise<void> {
  await client.delete(`/admin/tokens/${encodeURIComponent(id)}`)
}

/** Enrol or withdraw an existing MCP token from the self-improvement insight
 *  loop (owner-only). Reuses the server-side opt-in setter, so no token
 *  recreation is needed. Returns the updated record. */
export async function setMCPTokenOptIn(id: string, optIn: boolean): Promise<MCPToken> {
  const { data } = await client.patch<MCPToken>(`/admin/tokens/${encodeURIComponent(id)}`, {
    self_improve_opt_in: optIn,
  })
  return data
}

// SPA tokens

export async function listSpaTokens(): Promise<SpaToken[]> {
  const { data } = await client.get<ListResp<SpaToken>>('/admin/spa-tokens')
  return data.items
}

export async function revokeSpaToken(id: string): Promise<void> {
  await client.delete(`/admin/spa-tokens/${encodeURIComponent(id)}`)
}

// Users

export async function listUsers(): Promise<AdminUser[]> {
  const { data } = await client.get<ListResp<AdminUser>>('/admin/users')
  return data.items
}

export interface CreateUserRequest {
  username: string
  password: string
  role: 'member' | 'guest'
  totp_policy?: string // "" inherit | enabled | disabled
  /** Default true: the account sees only its grants until the owner lifts it. */
  restricted?: boolean
  /** Default true for members. */
  can_create_projects?: boolean
}

/** Create a new account directly (owner-only). The owner is a singleton, so only
 *  member/guest may be created here; a duplicate username rejects with 409. The
 *  optional totp_policy sets the per-user override at creation time. */
export async function createUser(body: CreateUserRequest): Promise<AdminUser> {
  const { data } = await client.post<AdminUser>('/admin/users', body)
  return data
}

export async function disableUser(id: string): Promise<void> {
  await client.delete(`/admin/users/${encodeURIComponent(id)}`)
}

/** Change a user's role between member and guest (owner-only). The owner is
 *  immutable and cannot be set here. Demoting to guest revokes the user's MCP
 *  tokens server-side. */
export async function updateUserRole(id: string, role: 'member' | 'guest'): Promise<AdminUser> {
  const { data } = await client.patch<AdminUser>(`/admin/users/${encodeURIComponent(id)}`, { role })
  return data
}

/** Set a user's per-user TOTP override: "" (inherit), "enabled", or "disabled". */
export async function updateUserTOTPPolicy(id: string, totpPolicy: string): Promise<AdminUser> {
  const { data } = await client.patch<AdminUser>(`/admin/users/${encodeURIComponent(id)}`, { totp_policy: totpPolicy })
  return data
}

/** Toggle the restricted flag and/or the project-creation capability. */
export async function updateUserFlags(
  id: string,
  patch: { restricted?: boolean; can_create_projects?: boolean },
): Promise<AdminUser> {
  const { data } = await client.patch<AdminUser>(`/admin/users/${encodeURIComponent(id)}`, patch)
  return data
}

/** Provision the account's personal project by hand (owner-only). */
export async function createPersonalProject(id: string): Promise<string> {
  const { data } = await client.post<{ personal_project: string }>(
    `/admin/users/${encodeURIComponent(id)}/personal-project`,
  )
  return data.personal_project
}

/** Clear a user's TOTP secret and recovery codes (owner-only): the escape
 *  hatch for a lost authenticator. Policy and sessions are untouched — under a
 *  required policy the user re-enrols at the next login. */
export async function resetUserTOTP(id: string): Promise<void> {
  await client.delete(`/admin/users/${encodeURIComponent(id)}/totp`)
}

// Invites

export async function listInvites(): Promise<Invite[]> {
  const { data } = await client.get<ListResp<Invite>>('/admin/invites')
  return data.items
}

export async function createInvite(ttlMs?: number): Promise<Invite> {
  const body = ttlMs ? { ttl_ms: ttlMs } : {}
  const { data } = await client.post<Invite>('/admin/invites', body)
  return data
}

export async function deleteInvite(token: string): Promise<void> {
  await client.delete(`/admin/invites/${encodeURIComponent(token)}`)
}

// Audit

export async function tailAudit(query: AuditQuery = {}): Promise<AuditEntry[]> {
  const params: Record<string, string | number> = {}
  if (query.actor) params.actor = query.actor
  if (query.user_id) params.user_id = query.user_id
  if (query.action) params.action = query.action
  if (query.source) params.source = query.source
  if (query.path_prefix) params.path_prefix = query.path_prefix
  if (query.since) params.since = query.since
  if (query.until) params.until = query.until
  if (query.limit) params.limit = query.limit
  const { data } = await client.get<ListResp<AuditEntry>>('/admin/audit', { params })
  return data.items
}
