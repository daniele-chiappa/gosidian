/**
 * useSessionRenewal — keeps the web session going while it is used (BUG-143).
 *
 * The server moves a session's expires_at only on POST /refresh. On a key, a
 * click or a turn of the wheel this asks for it once less than half of the
 * session's window is left: at most once a minute, and only for a session
 * whose expires_at can still move (it stops at hard_expiry). A tab nobody
 * uses expires when its window runs out, even while the vault's events keep
 * reloading it.
 *
 * The window runs from when the current expires_at arrived (the store's
 * renewedAt, on this browser's clock) to expires_at itself.
 */
import { onBeforeUnmount, onMounted } from 'vue'
import { useAuthStore } from '@/stores/auth'

/** The least time between two tries. */
const RETRY_MS = 60_000
const EVENTS = ['keydown', 'pointerdown', 'wheel'] as const

export function useSessionRenewal() {
  const auth = useAuthStore()
  let lastTry = -Infinity

  function due(now: number): boolean {
    if (!auth.token) return false
    const expires = Date.parse(auth.expiresAt)
    if (!Number.isFinite(expires) || expires >= Date.parse(auth.hardExpiry)) return false
    // A session stored before renewedAt existed renews at its first use.
    if (!auth.renewedAt) return true
    return expires - now < (expires - auth.renewedAt) / 2
  }

  function onUse() {
    const now = Date.now()
    if (now - lastTry < RETRY_MS || !due(now)) return
    lastTry = now
    // A refused renewal is left to the next request: its 401 leads to the
    // login like any other.
    auth.refresh().catch(() => {})
  }

  // On the window, in the capture phase: an editor that stops a key's
  // propagation still counts as use.
  onMounted(() => {
    for (const e of EVENTS) window.addEventListener(e, onUse, { capture: true, passive: true })
  })
  onBeforeUnmount(() => {
    for (const e of EVENTS) window.removeEventListener(e, onUse, { capture: true })
  })
}
