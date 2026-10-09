import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'

vi.mock('@/api/tags', () => ({ listTags: vi.fn(), notesByTag: vi.fn() }))
vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => ({ open: vi.fn() }),
}))

import { listTags, notesByTag } from '@/api/tags'
import TagsView from '@/views/TagsView.vue'

const NOTES_FAILED = 'The notes of the tag could not be read'
const LOAD_FAILED = 'The tags could not be read'

const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })

describe('TagsView (S6-12)', () => {
  beforeEach(() => vi.clearAllMocks())

  it('keeps the tag list when the notes of a tag fail', async () => {
    vi.mocked(listTags).mockResolvedValue([
      { tag: 'alpha', count: 1 },
      { tag: 'beta', count: 2 },
    ])
    vi.mocked(notesByTag).mockRejectedValueOnce(new Error('boom'))
    const w = mount(TagsView, { global: { plugins: [i18n()] } })
    await flushPromises()
    await w.findAll('aside button')[0]!.trigger('click')
    await flushPromises()
    expect(w.text()).toContain(NOTES_FAILED)
    expect(w.text()).toContain('#beta')

    vi.mocked(notesByTag).mockResolvedValueOnce([{ path: 'p/b.md', title: 'B note' }])
    await w.findAll('aside button')[1]!.trigger('click')
    await flushPromises()
    expect(w.text()).not.toContain(NOTES_FAILED)
    expect(w.text()).toContain('B note')
  })

  it('reads the list again from its error', async () => {
    vi.mocked(listTags).mockRejectedValueOnce(new Error('down')).mockResolvedValueOnce([{ tag: 'alpha', count: 1 }])
    const w = mount(TagsView, { global: { plugins: [i18n()] } })
    await flushPromises()
    expect(w.text()).toContain(LOAD_FAILED)
    await w.find('aside button').trigger('click')
    await flushPromises()
    expect(w.text()).toContain('#alpha')
    expect(w.text()).not.toContain(LOAD_FAILED)
  })

  it('shows the notes of the last tag picked when an older answer lands later', async () => {
    vi.mocked(listTags).mockResolvedValue([
      { tag: 'alpha', count: 1 },
      { tag: 'beta', count: 1 },
    ])
    let releaseOld: (v: { path: string; title: string }[]) => void = () => {}
    vi.mocked(notesByTag).mockImplementation((tag: string) =>
      tag === 'alpha'
        ? new Promise((r) => (releaseOld = r))
        : Promise.resolve([{ path: 'p/new.md', title: 'New note' }]),
    )
    const w = mount(TagsView, { global: { plugins: [i18n()] } })
    await flushPromises()
    await w.findAll('aside button')[0]!.trigger('click')
    await w.findAll('aside button')[1]!.trigger('click')
    await flushPromises()
    releaseOld([{ path: 'p/old.md', title: 'Old note' }])
    await flushPromises()
    expect(w.text()).toContain('New note')
    expect(w.text()).not.toContain('Old note')
  })

  it('reads the notes of a tag again from their error', async () => {
    vi.mocked(listTags).mockResolvedValue([{ tag: 'alpha', count: 1 }])
    vi.mocked(notesByTag)
      .mockRejectedValueOnce(new Error('boom'))
      .mockResolvedValueOnce([{ path: 'p/a.md', title: 'A note' }])
    const w = mount(TagsView, { global: { plugins: [i18n()] } })
    await flushPromises()
    await w.find('aside button').trigger('click')
    await flushPromises()
    await w.findAll('section button').find((b) => b.text() === 'Try again')!.trigger('click')
    await flushPromises()
    expect(w.text()).toContain('A note')
    expect(w.text()).not.toContain(NOTES_FAILED)
  })
})
