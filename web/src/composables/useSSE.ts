/**
 * useSSE — singleton EventSource bound to /api/v1/events. Phase 2bis
 * scaffolding: the connection lifecycle (open on auth, close on
 * logout, reconnect on token refresh) is in place; the real
 * subscribers (tree store invalidation, editor "modified externally"
 * badge) wire up in Phase 3 alongside the components themselves.
 *
 * Usage from a Vue component:
 *
 *   import { useSSE } from '@/composables/useSSE'
 *   const sse = useSSE()
 *   sse.on('tree', (payload) => treeStore.invalidate())
 *   sse.on('note', (payload) => editor.handleExternalUpdate(payload))
 *
 * The composable is idempotent: calling useSSE() many times returns
 * the same shared connection, so a tree-store consumer and an
 * editor consumer don't open two parallel sockets.
 */
import { ref, onScopeDispose } from 'vue'
import { useAuthStore } from '@/stores/auth'

export type SSETopic = 'tree' | 'note' | 'sidebar' | 'audit' | 'insight'

// ALL_TOPICS is the full known topic set. The singleton subscribes to all of
// them so any consumer's .on() works regardless of which component opened the
// connection first (the Sidebar opens it for 'tree', but the owner insights
// badge still needs 'insight').
const ALL_TOPICS: SSETopic[] = ['tree', 'note', 'sidebar', 'audit', 'insight']

export interface SSEPayload {
  action?: string
  path?: string
  etag?: string
  source?: string
  [key: string]: unknown
}

type Listener = (payload: SSEPayload) => void

let sharedSource: EventSource | null = null
let sharedToken = ''
// Reopening after the browser gave up (see es.onerror): the delay grows
// from RETRY_MIN_MS to RETRY_MAX_MS and resets once a connection opens.
const RETRY_MIN_MS = 2000
const RETRY_MAX_MS = 30000
let retryDelay = 0
let retryTimer: ReturnType<typeof setTimeout> | null = null
const listeners = new Map<SSETopic, Set<Listener>>()
const status = ref<'idle' | 'connecting' | 'open' | 'closed' | 'error'>('idle')

function dispatch(topic: SSETopic, raw: string) {
  let payload: SSEPayload = {}
  try {
    payload = JSON.parse(raw) as SSEPayload
  } catch {
    payload = { raw } as SSEPayload
  }
  const subs = listeners.get(topic)
  if (!subs) return
  for (const fn of subs) {
    try {
      fn(payload)
    } catch (err) {
      // Surface to console once; never let one bad listener kill
      // the bus for the others.
      console.error('useSSE listener error for', topic, err)
    }
  }
}

function connect(token: string, topics: SSETopic[]) {
  if (sharedSource) {
    if (sharedToken === token) return // already on this token
    sharedSource.close()
    sharedSource = null
  }
  sharedToken = token
  status.value = 'connecting'
  // The session rides on the HttpOnly gosidian_events cookie the server set
  // at login, never on the URL, which proxy access logs keep (IMP-090);
  // the token only tells a change of session apart.
  const params = new URLSearchParams()
  // Subscribe to the full known topic set (union with the caller's request)
  // so every consumer's .on() is served by the single shared connection.
  const subscribed = Array.from(new Set<SSETopic>([...topics, ...ALL_TOPICS]))
  params.set('topics', subscribed.join(','))
  const url = `/api/v1/events?${params.toString()}`
  const es = new EventSource(url)
  sharedSource = es

  es.onopen = () => {
    status.value = 'open'
    retryDelay = 0
  }
  es.onerror = () => {
    status.value = 'error'
    // After a network error the browser reconnects by itself. Any
    // non-200 answer instead closes the EventSource for good: a proxy
    // 502 during a restart, the server's 503 when a start outlasts the
    // time it holds event streams.
    // Reopen it ourselves then, with a growing delay, as long as it is
    // still the current connection.
    if (es.readyState !== EventSource.CLOSED || sharedSource !== es) return
    sharedSource = null
    retryDelay = Math.min(retryDelay ? retryDelay * 2 : RETRY_MIN_MS, RETRY_MAX_MS)
    const tok = token
    retryTimer = setTimeout(() => {
      retryTimer = null
      if (!sharedSource && sharedToken === tok) connect(tok, [])
    }, retryDelay)
  }
  // Wire each topic explicitly. The default `message` handler isn't
  // useful because we always send named events from the server.
  for (const topic of ALL_TOPICS) {
    es.addEventListener(topic, (e) => {
      const data = (e as MessageEvent).data ?? ''
      dispatch(topic, String(data))
    })
  }
}

function disconnect() {
  if (retryTimer) {
    clearTimeout(retryTimer)
    retryTimer = null
  }
  retryDelay = 0
  if (sharedSource) {
    sharedSource.close()
    sharedSource = null
  }
  sharedToken = ''
  status.value = 'closed'
}

export function useSSE(topics: SSETopic[] = []) {
  const auth = useAuthStore()

  // Open the connection lazily on first use, but only when auth is
  // ready. Components that need SSE call useSSE() and we tie the
  // lifecycle to the auth token presence.
  if (auth.token && (!sharedSource || sharedToken !== auth.token)) {
    connect(auth.token, topics)
  }

  return {
    status,
    on(topic: SSETopic, fn: Listener): () => void {
      let set = listeners.get(topic)
      if (!set) {
        set = new Set()
        listeners.set(topic, set)
      }
      set.add(fn)
      // Auto-remove on the calling component's unmount so
      // /events cache stays small as routes change.
      onScopeDispose(() => set?.delete(fn))
      return () => set?.delete(fn)
    },
    disconnect,
  }
}

/** Test/dev helper: closes the singleton + clears listeners. */
export function _resetSSEForTests() {
  disconnect()
  listeners.clear()
  status.value = 'idle'
}
