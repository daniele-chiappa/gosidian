import client from './client'

/** A project the signed-in user may grant to an OAuth client. */
export interface OAuthProject {
  name: string
  public: boolean
  note_count: number
}

/** GET /api/v1/oauth/requests/{id}: the pending authorization request the
 *  consent screen decides on, as prepared by the OAuth server (IMP-092). */
export interface OAuthConsent {
  request_id: string
  client_id: string
  client_name: string
  client_cimd: boolean
  redirect_uri: string
  redirect_host: string
  loopback_only: boolean
  scopes: string[]
  expires_at: string
  projects: OAuthProject[]
  can_write: boolean
  is_owner: boolean
}

export interface OAuthDecision {
  /** Empty = every visible project (an owner then gets an unscoped grant). */
  projects: string[]
  scopes: string[]
}

export async function getOAuthRequest(id: string): Promise<OAuthConsent> {
  const { data } = await client.get<OAuthConsent>(`/oauth/requests/${encodeURIComponent(id)}`)
  return data
}

/** Approve: returns the client's redirect URL carrying the authorization code. */
export async function approveOAuthRequest(id: string, decision: OAuthDecision): Promise<string> {
  const { data } = await client.post<{ redirect_to: string }>(
    `/oauth/requests/${encodeURIComponent(id)}/approve`,
    decision,
  )
  return data.redirect_to
}

/** Deny: returns the client's redirect URL carrying error=access_denied. */
export async function denyOAuthRequest(id: string): Promise<string> {
  const { data } = await client.post<{ redirect_to: string }>(
    `/oauth/requests/${encodeURIComponent(id)}/deny`,
    {},
  )
  return data.redirect_to
}
