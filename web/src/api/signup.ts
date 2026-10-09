import axios from 'axios'
import client from './client'

/**
 * POST /api/v1/signup — redeems an invite (BUG-099): creates a member
 * account, which then signs in like any other; the response carries no
 * session.
 */
export async function signup(username: string, password: string, invite: string): Promise<void> {
  await client.post('/signup', { username, password, invite })
}

/** True when the server refused the invite itself: unknown, used or expired. */
export function isInvalidInvite(e: unknown): boolean {
  if (!axios.isAxiosError(e)) return false
  const data = e.response?.data as { error?: { code?: string } } | undefined
  return data?.error?.code === 'auth.invite_invalid'
}
