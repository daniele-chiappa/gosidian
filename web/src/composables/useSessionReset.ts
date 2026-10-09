/**
 * Per-account client state must not survive a change of account in the same
 * browser tab: the tree, the effective-access map, the recently-viewed notes
 * and the persisted window layout all belong to whoever was signed in, and
 * showing them — even for the few hundred milliseconds it takes the new
 * account's requests to land — leaks project and note names across accounts.
 *
 * resetSessionState() drops all of it; call it on sign-out. noteSignedIn()
 * records the account that just signed in and resets when it differs from
 * the last one seen in this browser (a session that expired and was resumed
 * by another account never went through sign-out).
 */
import { useWindowsStore } from 'plancia'
import { useTreeStore } from '@/stores/tree'
import { useAccessStore } from '@/stores/access'
import { useRecentlyViewed } from '@/composables/useRecentlyViewed'

const LAST_USER_KEY = 'gosidian.lastUser'
/** Must match the storageKey passed to usePlanciaSync in AppShell. */
const PLANCIA_STORAGE_KEY = 'gosidian.plancia'

export function resetSessionState(): void {
  useTreeStore().invalidateAll()
  useAccessStore().reset()
  useRecentlyViewed().clear()
  useWindowsStore().reset()
  try {
    localStorage.removeItem(PLANCIA_STORAGE_KEY)
  } catch {
    /* storage disabled: nothing persisted to drop */
  }
}

/** Records the account that signed in, dropping the state of another one
 *  seen last in this browser. True only when it is that same account: an
 *  unknown last one (storage blocked or cleared) is not taken for it. */
export function noteSignedIn(userId: string): boolean {
  let last = ''
  try {
    last = localStorage.getItem(LAST_USER_KEY) ?? ''
  } catch {
    /* ignore */
  }
  if (last && last !== userId) resetSessionState()
  try {
    localStorage.setItem(LAST_USER_KEY, userId)
  } catch {
    /* ignore */
  }
  return last === userId
}

/**
 * withoutWorkspace drops the plancia's windows (`w`, `f`) from a `next=`
 * target. The 401 handler copies the whole address, which carries the
 * windows of the account whose session ran out: after another account
 * signs in they reopened under it, paths and titles included (BUG-116,
 * S6-5). The rest of the target (a consent request, a route) stays.
 */
export function withoutWorkspace(next: string): string {
  const q = next.indexOf('?')
  if (q < 0) return next
  const h = next.indexOf('#', q)
  const params = new URLSearchParams(h >= 0 ? next.slice(q + 1, h) : next.slice(q + 1))
  params.delete('w')
  params.delete('f')
  const rest = params.toString()
  return next.slice(0, q) + (rest ? `?${rest}` : '') + (h >= 0 ? next.slice(h) : '')
}
