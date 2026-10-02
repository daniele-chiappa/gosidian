/**
 * Axios client for the gosidian REST API. Three responsibilities:
 *
 *  1. Attach the Bearer token from the auth store to every outgoing
 *     request, so handlers don't repeat the header dance.
 *  2. Centralise 401 handling: clear the auth store and route the
 *     user back to /login with a `next=` query so the SPA can
 *     resume after re-auth.
 *  3. Surface 412 ("etag mismatch") as a typed event the editor
 *     conflict dialog listens for. Other 4xx/5xx pass through as
 *     promise rejections — call sites decide whether to toast,
 *     retry, or render an error pane.
 *
 * The router import is lazy (inside the interceptor) to break a
 * cycle: stores import the api client, the router imports the
 * stores, the api client imports the router. Resolving the router
 * at call time keeps the module graph acyclic.
 */
import axios, { type AxiosError, type AxiosRequestConfig } from 'axios'
import { useAuthStore } from '@/stores/auth'

const client = axios.create({
  baseURL: '/api/v1',
  withCredentials: false,
  headers: { 'Content-Type': 'application/json' },
})

client.interceptors.request.use((config) => {
  const auth = useAuthStore()
  if (auth.token) {
    config.headers = config.headers ?? {}
    ;(config.headers as Record<string, string>)['Authorization'] = `Bearer ${auth.token}`
  }
  return config
})

/**
 * `note.concurrency-conflict` event payload. Emitted on PUT /notes
 * 412 responses so the editor's ConflictDialog (Phase 2bis) can pick
 * up the current_etag without re-fetching the note.
 */
export type ConcurrencyConflictDetail = {
  path: string
  current_etag: string
  current_size: number
  current_content_excerpt: string
}

const eventBus = new EventTarget()

/** Subscribe to a named API client event (today: concurrency conflict). */
export function onApiEvent(
  name: 'note.concurrency-conflict',
  handler: (detail: ConcurrencyConflictDetail) => void,
): () => void {
  const wrapped = (e: Event) => {
    const ce = e as CustomEvent<ConcurrencyConflictDetail>
    handler(ce.detail)
  }
  eventBus.addEventListener(name, wrapped)
  return () => eventBus.removeEventListener(name, wrapped)
}

client.interceptors.response.use(
  (res) => res,
  async (err: AxiosError<{ error?: { code?: string; details?: Record<string, unknown> } }>) => {
    if (!err.response) return Promise.reject(err)

    const status = err.response.status
    const code = err.response.data?.error?.code

    if (status === 401) {
      const auth = useAuthStore()
      auth.clear()
      const next = encodeURIComponent(window.location.pathname + window.location.search)
      // Lazy router import to avoid the auth/api/router cycle.
      const { router } = await import('@/router')
      await router.push(`/login?next=${next}`)
      return Promise.reject(err)
    }

    if (status === 403 && code === 'auth.enrollment_required') {
      // The server gates every non-enrolment route until the user enrols a TOTP
      // secret (BUG-020). Raise the interstitial so the SPA mirrors the server
      // state even mid-session (e.g. the owner just made TOTP required).
      const auth = useAuthStore()
      auth.requireEnrollment()
    }

    if (status === 412 && code === 'concurrency.etag_mismatch') {
      const details = err.response.data?.error?.details ?? {}
      const detail: ConcurrencyConflictDetail = {
        path: extractPath(err.config),
        current_etag: String(details.current_etag ?? ''),
        current_size: Number(details.current_size ?? 0),
        current_content_excerpt: String(details.current_content_excerpt ?? ''),
      }
      eventBus.dispatchEvent(new CustomEvent('note.concurrency-conflict', { detail }))
    }

    return Promise.reject(err)
  },
)

/**
 * True for the 412 that `note.concurrency-conflict` already announced, so a
 * caller can leave it to the conflict banner instead of an error pane.
 */
export function isConcurrencyConflict(e: unknown): boolean {
  if (!axios.isAxiosError(e)) return false
  const data = e.response?.data as { error?: { code?: string } } | undefined
  return e.response?.status === 412 && data?.error?.code === 'concurrency.etag_mismatch'
}

// Call sites pass URLs relative to baseURL (`/notes/…`); the absolute form
// is kept for requests built outside this client.
const NOTE_URL_PREFIXES = ['/notes/', '/api/v1/notes/']

function extractPath(cfg: AxiosRequestConfig | undefined): string {
  const url = cfg?.url
  if (!url) return ''
  const prefix = NOTE_URL_PREFIXES.find((p) => url.startsWith(p))
  return decodeURIComponent(prefix ? url.slice(prefix.length) : url)
}

export default client
