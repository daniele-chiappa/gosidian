import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { createPinia, setActivePinia } from 'pinia'
import enUI from '@catalogs/ui.en.json'
import type { Note } from '@/api/notes'

// The view's tables against a stream of other notes' events, with the
// debounce as it runs (BUG-146): NoteView.test.ts makes it immediate.
vi.mock('@/api/notes', async (orig) => ({
  ...(await orig<typeof import('@/api/notes')>()),
  getNote: vi.fn(),
}))
vi.mock('@/api/preview', async (orig) => ({
  ...(await orig<typeof import('@/api/preview')>()),
  renderPreviewData: vi.fn(async () => ({ html: '<div class="gosidian-view"></div>', views: [], diagramLinks: {} })),
}))
vi.mock('@/api/marks', () => ({ listMarks: vi.fn(async () => []) }))
const hooks = vi.hoisted(() => ({ note: (_p: { path?: string; etag?: string; action?: string }) => {} }))
vi.mock('@/composables/useSSE', () => ({
  useSSE: () => ({
    on: (_kind: string, fn: typeof hooks.note) => {
      hooks.note = fn
      return () => {}
    },
  }),
}))
vi.mock('@/api/client', async (orig) => ({
  ...(await orig<typeof import('@/api/client')>()),
  onApiEvent: () => () => {},
  onSessionExpired: () => () => {},
}))
vi.mock('@/composables/useRecentlyViewed', async (orig) => ({
  ...(await orig<typeof import('@/composables/useRecentlyViewed')>()),
  useRecentlyViewed: () => ({ record: vi.fn(), clear: vi.fn() }),
}))
vi.mock('@/stores/access', async (orig) => ({
  ...(await orig<typeof import('@/stores/access')>()),
  useAccessStore: () => ({ canWrite: () => true, deleteNoteKey: 'note.confirm_delete' }),
}))
vi.mock('@/stores/tree', async (orig) => ({
  ...(await orig<typeof import('@/stores/tree')>()),
  useTreeStore: () => ({ refresh: vi.fn() }),
}))
vi.mock('@/stores/ui', async (orig) => ({
  ...(await orig<typeof import('@/stores/ui')>()),
  useUIStore: () => ({ pipelinesEnabled: true }),
}))
vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => ({ windows: [], open: vi.fn(), identify: vi.fn() }),
}))
vi.mock('@/components/editor/CodeMirrorEditor.vue', async () => {
  const { defineComponent, h } = await import('vue')
  return { __esModule: true, default: defineComponent({ name: 'CodeMirrorEditor', setup: () => () => h('div') }) }
})

import { getNote } from '@/api/notes'
import { renderPreviewData } from '@/api/preview'
import NoteView from '@/views/NoteView.vue'

const board: Note = {
  path: 'p/board.md',
  title: 'Board',
  content: '# Board\n\n```view\nfrom: p/tasks\n```\n',
  etag: 'e-1',
  size: 30,
  mod_time: '2026-11-02T10:00:00Z',
}

describe('NoteView, the tables of its views (BUG-146)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked(getNote).mockReset().mockResolvedValue(board)
    vi.mocked(renderPreviewData).mockClear()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  it('draws them again while other notes change without a pause', async () => {
    mount(NoteView, {
      props: { path: board.path },
      global: {
        plugins: [createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })],
        stubs: { MarkdownPreview: true, CutPreview: true, PropertiesPanel: true, RowViews: true },
      },
    })
    await flushPromises()
    const drawn = vi.mocked(renderPreviewData).mock.calls.length
    expect(drawn).toBeGreaterThan(0)

    // Other notes change every 300 ms for 5 s, as with many writers.
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date'] })
    for (let at = 0; at < 5000; at += 300) {
      hooks.note({ path: `p/tasks/t-${at}.md`, etag: `x-${at}`, action: 'update' })
      await vi.advanceTimersByTimeAsync(300)
    }
    expect(vi.mocked(renderPreviewData).mock.calls.length).toBeGreaterThanOrEqual(drawn + 2)
  })
})
