import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { useAuthStore } from '@/stores/auth'
import { useSessionRenewal } from '@/composables/useSessionRenewal'

const T0 = Date.parse('2026-11-01T10:00:00Z')
const MIN = 60_000
const HOUR = 60 * MIN

// A session whose expires_at arrived at `renewedAt`, `ttl` before it ends.
function signIn({ renewedAt = T0, ttl = 24 * HOUR, hard = T0 + 168 * HOUR } = {}) {
  const auth = useAuthStore()
  auth.token = 'gsp_test'
  auth.user = { id: 'u1', username: 'claude', role: 'member' }
  auth.renewedAt = renewedAt
  auth.expiresAt = new Date(renewedAt + ttl).toISOString()
  auth.hardExpiry = new Date(hard).toISOString()
  return auth
}

// /refresh answers with a window of 24 h from now, or fails when `fail`.
function mockRefresh(fail = false) {
  const fn = vi.fn().mockImplementation(async () => {
    if (fail) throw new Error('network')
    return {
      ok: true,
      status: 200,
      json: async () => ({
        token: 'gsp_test',
        expires_at: new Date(Date.now() + 24 * HOUR).toISOString(),
        hard_expiry: new Date(T0 + 168 * HOUR).toISOString(),
      }),
    }
  })
  globalThis.fetch = fn as unknown as typeof fetch
  return fn
}

function harness() {
  return mount(
    defineComponent({
      setup() {
        useSessionRenewal()
        return () => h('div', { class: 'page' })
      },
    }),
    { attachTo: document.body },
  )
}

// A use of the page, from inside it: the composable listens on the window.
function use(kind = 'keydown') {
  document.querySelector('.page')!.dispatchEvent(new Event(kind, { bubbles: true }))
}

function at(t: number) {
  vi.setSystemTime(t)
}

describe('useSessionRenewal (BUG-143)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.useFakeTimers({ toFake: ['Date'] })
  })
  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
    document.body.innerHTML = ''
  })

  it('does not renew while more than half the window is left', async () => {
    signIn()
    const fetch = mockRefresh()
    const w = harness()
    at(T0 + 11 * HOUR)
    use()
    await flushPromises()
    expect(fetch).not.toHaveBeenCalled()
    w.unmount()
  })

  it('renews on a key once less than half is left, and the window starts again', async () => {
    const auth = signIn()
    const fetch = mockRefresh()
    const w = harness()
    at(T0 + 13 * HOUR)
    use()
    await flushPromises()
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(fetch).toHaveBeenCalledWith('/api/v1/refresh', expect.objectContaining({ method: 'POST' }))
    expect(auth.expiresAt).toBe(new Date(T0 + 37 * HOUR).toISOString())
    expect(auth.renewedAt).toBe(T0 + 13 * HOUR)

    at(T0 + 13 * HOUR + 2 * MIN)
    use()
    await flushPromises()
    expect(fetch).toHaveBeenCalledTimes(1)
    w.unmount()
  })

  it.each(['pointerdown', 'wheel'])('a %s counts as use too', async (kind) => {
    signIn()
    const fetch = mockRefresh()
    const w = harness()
    at(T0 + 13 * HOUR)
    use(kind)
    await flushPromises()
    expect(fetch).toHaveBeenCalledTimes(1)
    w.unmount()
  })

  it('tries at most once a minute', async () => {
    signIn()
    const fetch = mockRefresh(true)
    const w = harness()
    at(T0 + 13 * HOUR)
    use()
    await flushPromises()
    at(T0 + 13 * HOUR + 30_000)
    use()
    await flushPromises()
    expect(fetch).toHaveBeenCalledTimes(1)

    at(T0 + 13 * HOUR + MIN)
    use()
    await flushPromises()
    expect(fetch).toHaveBeenCalledTimes(2)
    w.unmount()
  })

  it('has nothing to renew without a session', async () => {
    const fetch = mockRefresh()
    const w = harness()
    at(T0 + 13 * HOUR)
    use()
    useAuthStore().setAnonymous()
    use()
    await flushPromises()
    expect(fetch).not.toHaveBeenCalled()
    w.unmount()
  })

  it('has nothing to renew once expires_at is the hard expiry', async () => {
    const hard = T0 + 168 * HOUR
    signIn({ renewedAt: hard - 20 * HOUR, ttl: 20 * HOUR, hard })
    const fetch = mockRefresh()
    const w = harness()
    at(hard - 10 * MIN)
    use()
    await flushPromises()
    expect(fetch).not.toHaveBeenCalled()
    w.unmount()
  })

  it('renews a session kept from before renewedAt at its first use', async () => {
    const auth = signIn({ ttl: 20 * HOUR })
    auth.renewedAt = 0
    const fetch = mockRefresh()
    const w = harness()
    at(T0)
    use()
    await flushPromises()
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(auth.renewedAt).toBe(T0)
    w.unmount()
  })

  it('stops listening once unmounted', async () => {
    signIn()
    const fetch = mockRefresh()
    harness().unmount()
    at(T0 + 13 * HOUR)
    window.dispatchEvent(new Event('keydown'))
    await flushPromises()
    expect(fetch).not.toHaveBeenCalled()
  })
})
