import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'

vi.mock('@/api/notes', async (orig) => ({
  ...(await orig<typeof import('@/api/notes')>()),
  getRowViews: vi.fn(),
}))
const hooks = vi.hoisted(() => ({ note: (_p: { path?: string }) => {} }))
vi.mock('@/composables/useSSE', () => ({
  useSSE: () => ({
    on: (_kind: string, fn: typeof hooks.note) => {
      hooks.note = fn
      return () => {}
    },
  }),
}))

import { getRowViews } from '@/api/notes'
import RowViews from '@/components/views/RowViews.vue'

describe('RowViews, against other notes changing (BUG-146)', () => {
  beforeEach(() => {
    vi.mocked(getRowViews)
      .mockReset()
      .mockResolvedValue({
        database: 'p/tasks.md',
        views: [{ title: 'Linked', view: { total: 1 }, html: '<table></table>' }],
      })
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('loads the views again while other notes change without a pause', async () => {
    mount(RowViews, {
      props: { path: 'p/tasks/t-1.md' },
      global: {
        plugins: [createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })],
        stubs: { MarkdownPreview: true },
      },
    })
    await flushPromises()
    const loaded = vi.mocked(getRowViews).mock.calls.length
    expect(loaded).toBe(1)

    // Other notes change every 300 ms for 5 s, as with many writers.
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date'] })
    for (let at = 0; at < 5000; at += 300) {
      hooks.note({ path: `p/tasks/t-${at}.md` })
      await vi.advanceTimersByTimeAsync(300)
    }
    expect(vi.mocked(getRowViews).mock.calls.length).toBeGreaterThanOrEqual(loaded + 2)
  })
})
