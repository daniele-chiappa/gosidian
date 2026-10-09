import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import type { InternalAxiosRequestConfig } from 'axios'
import client, { isConcurrencyConflict, onApiEvent } from '@/api/client'
import { useAuthStore } from '@/stores/auth'

// The 401 handler imports the router lazily; a stub records where it goes.
const routerPush = vi.hoisted(() => vi.fn(() => Promise.resolve()))
vi.mock('@/router', () => ({ router: { push: routerPush } }))

// We exercise the interceptors via axios's request adapter by
// stubbing it — we don't need a real network. Each test sets up a
// fresh adapter so behaviour is independent.
function setAdapter(adapter: NonNullable<typeof client.defaults.adapter>) {
  client.defaults.adapter = adapter
}

describe('api/client interceptors', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })
  afterEach(() => {
    vi.restoreAllMocks()
    delete client.defaults.adapter
  })

  it('attaches Authorization: Bearer when auth.token is set', async () => {
    const auth = useAuthStore()
    auth.token = 'gsp_test'
    auth.user = { id: 'u', username: 'owner', role: 'owner' }
    let captured: Record<string, string> | undefined
    setAdapter((config) => {
      captured = (config.headers as Record<string, string>) ?? {}
      return Promise.resolve({
        data: {},
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      })
    })
    await client.get('/me')
    expect(captured?.Authorization).toBe('Bearer gsp_test')
  })

  it('omits Authorization header when no token', async () => {
    let captured: Record<string, string> | undefined
    setAdapter((config) => {
      captured = (config.headers as Record<string, string>) ?? {}
      return Promise.resolve({
        data: {},
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      })
    })
    await client.get('/health')
    expect(captured?.Authorization).toBeUndefined()
  })

  it('emits note.concurrency-conflict on 412 with details', async () => {
    setAdapter((config) =>
      Promise.reject({
        config,
        response: {
          status: 412,
          statusText: 'Precondition Failed',
          headers: {},
          config,
          data: {
            error: {
              code: 'concurrency.etag_mismatch',
              details: {
                current_etag: '"new-etag"',
                current_size: 42,
                current_content_excerpt: 'updated by another tab',
              },
            },
          },
        },
        isAxiosError: true,
      }),
    )

    const handler = vi.fn()
    const off = onApiEvent('note.concurrency-conflict', handler)
    // Same URL shape as api/notes.ts: relative to baseURL, path encoded.
    const err = await client
      .put(`/notes/${encodeURIComponent('p/a b.md')}`, { content: 'x' })
      .catch((e: unknown) => e)
    expect(isConcurrencyConflict(err)).toBe(true)
    expect(isConcurrencyConflict(new Error('boom'))).toBe(false)
    expect(handler).toHaveBeenCalledTimes(1)
    expect(handler).toHaveBeenCalledWith(
      expect.objectContaining({
        path: 'p/a b.md',
        current_etag: '"new-etag"',
        current_size: 42,
        current_content_excerpt: 'updated by another tab',
      }),
    )
    off()
  })

  // S6-11: the requests that fail together when a session ends send the
  // browser to the login page once, and the page's own requests do not wrap
  // its address in a new next=.
  it('sends a 401 to the login page with the deep link, and only once', async () => {
    const unauthorized = (config: InternalAxiosRequestConfig) =>
      Promise.reject({
        config,
        response: { status: 401, statusText: 'Unauthorized', headers: {}, config, data: {} },
        isAxiosError: true,
      })
    setAdapter(unauthorized)
    routerPush.mockClear()
    window.history.replaceState({}, '', '/notes/p/x?w=1')
    await client.get('/me').catch(() => undefined)
    expect(routerPush).toHaveBeenCalledWith(`/login?next=${encodeURIComponent('/notes/p/x?w=1')}`)

    routerPush.mockClear()
    window.history.replaceState({}, '', `/login?next=${encodeURIComponent('/notes/p/x?w=1')}`)
    await client.get('/auth-config').catch(() => undefined)
    expect(routerPush).not.toHaveBeenCalled()
    window.history.replaceState({}, '', '/')
  })
})
