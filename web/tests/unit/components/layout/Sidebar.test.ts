import { describe, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import { mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'

const windows = vi.hoisted(() => ({ open: vi.fn((): string => '') }))
vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => windows,
  usePlanciaFocus: () => ({ focusWindow: vi.fn(async () => true) }),
}))
vi.mock('@/composables/useSSE', () => ({ useSSE: () => ({ on: vi.fn() }) }))
vi.mock('@/composables/useRecentlyViewed', () => ({
  useRecentlyViewed: () => ({
    entries: ref([
      { path: 'p/a.md', title: 'Alpha', ts: 1 },
      { path: 'p/b.md', title: 'Beta', ts: 2 },
      { path: 'p/tasks/20261031-004154-nota-delta.md', title: '20261031-004154-nota-delta', ts: 3 },
    ]),
    record: vi.fn(),
  }),
}))
vi.mock('@/stores/tree', () => ({
  useTreeStore: () => ({ byProject: {}, loading: {}, error: {}, load: vi.fn(), refresh: vi.fn() }),
}))
vi.mock('@/stores/access', () => ({ useAccessStore: () => ({ load: vi.fn() }) }))

import Sidebar from '@/components/layout/Sidebar.vue'

const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
const mountSidebar = () =>
  mount(Sidebar, {
    global: { plugins: [createPinia(), i18n()], stubs: { TreeNode: true, TreeContextMenu: true } },
  })

// F-18 (IMP-219): the sidebar's small targets reach 24 x 24 px.
describe('Sidebar', () => {
  it('makes the Recent entries 24 px tall and the tree refresh 24 x 24', async () => {
    const w = mountSidebar()
    const recent = w.findAll('button').filter((b) => ['Alpha', 'Beta'].includes(b.text()))
    expect(recent).toHaveLength(2)
    for (const b of recent) expect(b.classes()).toEqual(expect.arrayContaining(['h-6', 'leading-6']))

    const refresh = w.find('button[aria-label="Refresh the tree"]')
    expect(refresh.classes()).toEqual(expect.arrayContaining(['h-6', 'w-6']))

    await recent[0]!.trigger('click')
    expect(windows.open).toHaveBeenCalledWith(expect.objectContaining({ type: 'note', props: { path: 'p/a.md' } }))
  })

  // F-19: a recent note shows its title, its file name on hover; one
  // without a title, its file name cut in the middle.
  it('shows a recent note by its title, its file name on hover', () => {
    const w = mountSidebar()
    const alpha = w.findAll('button').find((b) => b.text() === 'Alpha')!
    expect(alpha.find('[title]').attributes('title')).toBe('a.md')
    const cut = w.findAll('button').filter((b) => b.find('[data-cut-tail]').exists())
    expect(cut).toHaveLength(1)
    expect(cut[0]!.find('[data-cut-tail]').text()).toBe('nota-delta')
    expect(cut[0]!.find('[title]').attributes('title')).toBe('20261031-004154-nota-delta.md')
  })
})
