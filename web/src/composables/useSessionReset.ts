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

export function noteSignedIn(userId: string): void {
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
}
