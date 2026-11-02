import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useAuthStore } from '@/stores/auth'

// The store talks to /api/v1 via window.fetch (it can't import the
// axios client without creating a cycle). We swap fetch for a mock
// per spec.
function mockFetchOnce(response: { ok: boolean; status?: number; body: unknown }) {
  const fn = vi.fn().mockResolvedValue({
    ok: response.ok,
    status: response.status ?? 200,
    json: async () => response.body,
  })
  globalThis.fetch = fn as unknown as typeof fetch
  return fn
}

describe('auth store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('starts unauthenticated', () => {
    const auth = useAuthStore()
    expect(auth.isAuthenticated).toBe(false)
    expect(auth.isOwner).toBe(false)
  })

  it('login populates token + user + flips isAuthenticated', async () => {
    mockFetchOnce({
      ok: true,
      body: {
        token: 'gsp_test',
        expires_at: '2026-12-31T00:00:00Z',
        hard_expiry: '2027-01-07T00:00:00Z',
        user: { id: 'u1', username: 'owner', role: 'owner' },
      },
    })
    const auth = useAuthStore()
    await auth.login('owner', 'pwd')
    expect(auth.token).toBe('gsp_test')
    expect(auth.isAuthenticated).toBe(true)
    expect(auth.isOwner).toBe(true)
    expect(auth.username).toBe('owner')
  })

  it('login surfaces server error message via thrown Error', async () => {
    mockFetchOnce({
      ok: false,
      status: 401,
      body: { error: { code: 'auth.invalid_credentials', message: 'wrong creds' } },
    })
    const auth = useAuthStore()
    await expect(auth.login('owner', 'bad')).rejects.toThrow(/wrong creds/)
    expect(auth.isAuthenticated).toBe(false)
  })

  it('clear() drops everything (no token, no user)', () => {
    const auth = useAuthStore()
    auth.token = 'leftover'
    auth.user = { id: 'u', username: 'x', role: 'member' }
    auth.clear()
    expect(auth.isAuthenticated).toBe(false)
    expect(auth.token).toBe('')
    expect(auth.user).toBeNull()
  })

  it('login and refresh note when their expires_at arrived (BUG-143)', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(Date.parse('2026-11-01T10:00:00Z'))
    mockFetchOnce({
      ok: true,
      body: {
        token: 'gsp_test',
        expires_at: '2026-11-02T10:00:00Z',
        hard_expiry: '2026-11-08T10:00:00Z',
        user: { id: 'u1', username: 'owner', role: 'owner' },
      },
    })
    const auth = useAuthStore()
    await auth.login('owner', 'pwd')
    expect(auth.renewedAt).toBe(Date.parse('2026-11-01T10:00:00Z'))

    vi.setSystemTime(Date.parse('2026-11-01T23:00:00Z'))
    const fn = mockFetchOnce({
      ok: true,
      body: { token: 'gsp_test', expires_at: '2026-11-02T23:00:00Z', hard_expiry: '2026-11-08T10:00:00Z' },
    })
    await auth.refresh()
    expect(fn).toHaveBeenCalledWith('/api/v1/refresh', expect.objectContaining({ method: 'POST' }))
    expect(auth.expiresAt).toBe('2026-11-02T23:00:00Z')
    expect(auth.renewedAt).toBe(Date.parse('2026-11-01T23:00:00Z'))

    auth.clear()
    expect(auth.renewedAt).toBe(0)
    vi.useRealTimers()
  })

  it('login notes how long the session lasts at most (BUG-142)', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(Date.parse('2026-11-01T10:00:00Z'))
    mockFetchOnce({
      ok: true,
      body: {
        token: 'gsp_test',
        expires_at: '2026-11-02T10:00:00Z',
        hard_expiry: '2026-11-08T10:00:00Z',
        user: { id: 'u1', username: 'owner', role: 'owner' },
      },
    })
    const auth = useAuthStore()
    await auth.login('owner', 'pwd')
    expect(auth.maxSessionMs).toBe(7 * 24 * 3600_000)
    auth.clear()
    expect(auth.maxSessionMs).toBe(0)
    vi.useRealTimers()
  })

  // The login page says so after a session that ended (BUG-142). The mark
  // lives with the account, not in the address, which the workspace's own
  // writes can replace before the page reads it.
  it('marks a session that ended until the next login', async () => {
    const auth = useAuthStore()
    auth.token = 'gsp_test'
    auth.user = { id: 'u1', username: 'owner', role: 'owner' }
    auth.endSession()
    expect(auth.token).toBe('')
    expect(auth.user).toBeNull()
    expect(auth.sessionEnded).toBe(true)
    // The other requests the end refused clear the account again.
    auth.clear()
    expect(auth.sessionEnded).toBe(true)

    mockFetchOnce({
      ok: true,
      body: {
        token: 'gsp_test',
        expires_at: '2026-12-31T00:00:00Z',
        hard_expiry: '2027-01-07T00:00:00Z',
        user: { id: 'u1', username: 'owner', role: 'owner' },
      },
    })
    await auth.login('owner', 'pwd')
    expect(auth.sessionEnded).toBe(false)
  })

  it('logout calls /logout then clears (best-effort on network error)', async () => {
    const auth = useAuthStore()
    auth.token = 'gsp_test'
    auth.user = { id: 'u1', username: 'owner', role: 'owner' }
    // Make /logout fail (e.g. 500); the store should still clear.
    globalThis.fetch = vi.fn().mockRejectedValue(new Error('network')) as unknown as typeof fetch
    await auth.logout()
    expect(auth.isAuthenticated).toBe(false)
  })
})
