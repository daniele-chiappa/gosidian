import client from './client'

/**
 * Passwords after creation (IMP-063): an account changes its own with the
 * current one; the owner sets another's, temporary, with the owner's own.
 * A wrong password answers 403 (never 401, which would end the session).
 */

/** POST /me/password. Closes the account's other web sessions. */
export async function changePassword(
  currentPassword: string,
  newPassword: string,
): Promise<number> {
  const { data } = await client.post<{ sessions_closed: number }>('/me/password', {
    current_password: currentPassword,
    new_password: newPassword,
  })
  return data.sessions_closed
}

/** POST /admin/users/{id}/password: the account must change it at its next
 *  request, and its web sessions close. */
export async function resetUserPassword(
  id: string,
  password: string,
  ownerPassword: string,
): Promise<void> {
  await client.post(`/admin/users/${encodeURIComponent(id)}/password`, {
    password,
    owner_password: ownerPassword,
  })
}
