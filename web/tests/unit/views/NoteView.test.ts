import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { createPinia, setActivePinia } from 'pinia'
import enUI from '@catalogs/ui.en.json'
import type { Note } from '@/api/notes'
import type { CutView } from '@/api/preview'
import type { MarkSet } from '@/api/marks'

vi.mock('@/api/notes', async (orig) => ({
  ...(await orig<typeof import('@/api/notes')>()),
  getNote: vi.fn(),
  updateNote: vi.fn(),
}))
vi.mock('@/api/preview', async (orig) => ({
  ...(await orig<typeof import('@/api/preview')>()),
  renderPreviewData: vi.fn(async () => ({ html: '<p>text</p>', views: [], diagramLinks: {} })),
  renderCut: vi.fn(),
  listCutCollections: vi.fn(),
}))
vi.mock('@/api/marks', () => ({ listMarks: vi.fn() }))
vi.mock('@/composables/useSSE', () => ({ useSSE: () => ({ on: () => () => {} }) }))
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
// Immediate: a second cut request from the settings' watcher shows at once.
vi.mock('@vueuse/core', async (orig) => ({
  ...(await orig<typeof import('@vueuse/core')>()),
  useDebounceFn: (fn: () => unknown) => fn,
}))
// The editor as a plain field the test types in through its v-model.
vi.mock('@/components/editor/CodeMirrorEditor.vue', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    __esModule: true,
    default: defineComponent({
      name: 'CodeMirrorEditor',
      props: { modelValue: { type: String, default: '' } },
      emits: ['update:modelValue'],
      setup: () => () => h('div', { 'data-editor': '' }),
    }),
  }
})

import { getNote, updateNote } from '@/api/notes'
import { listCutCollections, renderCut } from '@/api/preview'
import { listMarks } from '@/api/marks'
import CutPreview from '@/components/domain/CutPreview.vue'
import NoteView from '@/views/NoteView.vue'

const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })

const note = (path: string, content = '# A\n\ntext'): Note => ({
  path,
  title: path,
  content,
  etag: `e-${content.length}`,
  size: content.length,
  mod_time: '2026-10-15T10:00:00Z',
})

const cut = (sections: number): CutView => ({
  blocks: [],
  sections,
  paragraphs: 0,
  media: { images: 0, icons: 0, described: 0, missing: 0, word_alt: 0 },
  max_chars: 6000,
  stored: 0,
  orphans: 0,
})

const markSet = (set: string): MarkSet => ({ path: 'p/a.md', set, hash: 'h', created: '2026-10-15T10:00:00Z', marks: [], stale: false })

async function mountNote(path = 'p/a.md') {
  const w = mount(NoteView, {
    props: { path },
    global: { plugins: [i18n()], stubs: { MarkdownPreview: true, CutPreview: true, PropertiesPanel: true, RowViews: true } },
  })
  await flushPromises()
  return w
}

async function toggle(w: VueWrapper) {
  await w.find('[data-cut-toggle]').trigger('click')
  await flushPromises()
}

const button = (w: VueWrapper, label: string) => w.findAll('button').find((b) => b.text() === label)!
const shownCut = (w: VueWrapper) => w.findComponent(CutPreview).props('view') as CutView | undefined

describe('NoteView, the cut view (IMP-170)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked(getNote).mockReset().mockImplementation(async (p: string) => note(p))
    vi.mocked(updateNote).mockReset()
    vi.mocked(renderCut).mockReset().mockResolvedValue(cut(1))
    vi.mocked(listCutCollections).mockReset().mockResolvedValue({ collections: [], problems: [] })
    vi.mocked(listMarks).mockReset().mockResolvedValue([])
  })

  it('cuts the note once per toggle, with the collections and the marks read beside it', async () => {
    const w = await mountNote()
    expect(listMarks).toHaveBeenCalledTimes(1)
    await toggle(w)
    expect(renderCut).toHaveBeenCalledTimes(1)
    expect(listCutCollections).toHaveBeenCalledWith('p')
    expect(listMarks).toHaveBeenCalledTimes(2)
    expect(shownCut(w)?.sections).toBe(1)
    expect(w.findComponent(CutPreview).props('notePath')).toBe('p/a.md')

    await toggle(w)
    expect(w.findComponent(CutPreview).exists()).toBe(false)
    await toggle(w)
    expect(renderCut).toHaveBeenCalledTimes(2)
  })

  it('draws the answer to the last rules when an older one lands after it', async () => {
    let releaseOld: (v: CutView) => void = () => {}
    vi.mocked(renderCut)
      .mockReturnValueOnce(new Promise((r) => (releaseOld = r)))
      .mockResolvedValueOnce(cut(2))
    const w = await mountNote()
    await toggle(w)
    await w.find('[data-cut-settings] select:not([data-cut-collection])').setValue('path')
    await flushPromises()
    expect(renderCut).toHaveBeenCalledTimes(2)
    expect(vi.mocked(renderCut).mock.calls[1]![2]).toMatchObject({ context: 'path' })
    releaseOld(cut(7))
    await flushPromises()
    expect(shownCut(w)?.sections).toBe(2)
  })

  it("drops the marks of the note the window showed before", async () => {
    let releaseOld: (v: MarkSet[]) => void = () => {}
    vi.mocked(listMarks)
      .mockReturnValueOnce(new Promise((r) => (releaseOld = r)))
      .mockResolvedValueOnce([markSet('new')])
    const w = await mountNote()
    await w.setProps({ path: 'p/b.md' })
    await flushPromises()
    releaseOld([markSet('old1'), markSet('old2')])
    await flushPromises()
    expect(w.find('[data-cut-marks-dot]').attributes('aria-hidden')).toBe('true')
    // The dot is a colour: its words are in the button, for a screen reader.
    expect(w.find('[data-cut-toggle] [data-cut-marks-text]').text()).toBe('The note has a set of marks')
  })

  it('keeps the rules per project, a number field emptied or out of range back in its range', async () => {
    localStorage.setItem('gosidian.cut.p', JSON.stringify({ collection: 'vec/docs', context: 'path', max_chars: '' }))
    const w = await mountNote()
    await toggle(w)
    expect(vi.mocked(renderCut).mock.calls[0]![2]).toEqual({
      collection: 'vec/docs',
      context: 'path',
      max_chars: 6000,
      inline_max_side: 128,
    })
    // The saved collection stays chosen though the archive does not list it now.
    expect((w.find('[data-cut-collection]').element as HTMLSelectElement).value).toBe('vec/docs')

    const side = w.find('[data-cut-inline-max-side]')
    ;(side.element as HTMLInputElement).value = '0'
    await side.trigger('change')
    await flushPromises()
    expect(vi.mocked(renderCut).mock.lastCall![2]).toMatchObject({ inline_max_side: 0 })

    // The images with the text beside them: sent only when chosen.
    await w.find('[data-cut-media-paragraphs]').setValue('join')
    await flushPromises()
    expect(vi.mocked(renderCut).mock.lastCall![2]).toMatchObject({ media_paragraphs: 'join' })
    await w.find('[data-cut-icon-descriptions]').setValue('first')
    await flushPromises()
    expect(vi.mocked(renderCut).mock.lastCall![2]).toMatchObject({ icon_descriptions: 'first' })

    const max = w.find('[data-cut-max-chars]')
    ;(max.element as HTMLInputElement).value = '50'
    await max.trigger('change')
    await flushPromises()
    expect((max.element as HTMLInputElement).value).toBe('200')
    expect(vi.mocked(renderCut).mock.lastCall![2]).toMatchObject({ max_chars: 200 })
    ;(max.element as HTMLInputElement).value = ''
    await max.trigger('change')
    await flushPromises()
    expect((max.element as HTMLInputElement).value).toBe('6000')
    expect(vi.mocked(renderCut).mock.lastCall![2]).toMatchObject({ max_chars: 6000 })
    expect(JSON.parse(localStorage.getItem('gosidian.cut.p')!)).toMatchObject({
      collection: 'vec/docs',
      max_chars: 6000,
      inline_max_side: 0,
      media_paragraphs: 'join',
      icon_descriptions: 'first',
    })

    // A note of another project: its own rules, the defaults here.
    const calls = vi.mocked(renderCut).mock.calls.length
    await w.setProps({ path: 'q/n.md' })
    await flushPromises()
    expect(vi.mocked(renderCut).mock.calls.length).toBe(calls + 1)
    expect(vi.mocked(renderCut).mock.lastCall![2]).toEqual({ context: 'heading', max_chars: 6000, inline_max_side: 128 })
    expect(listCutCollections).toHaveBeenLastCalledWith('q')
    expect(localStorage.getItem('gosidian.cut.q')).toBeNull()
  })

  it('says why an archive lists no collection, or that the list failed', async () => {
    vi.mocked(listCutCollections).mockResolvedValueOnce({ collections: [], problems: ['vec-pg: unreachable'] })
    const w = await mountNote()
    await toggle(w)
    expect(w.find('[data-cut-collections-problems]').text()).toContain('vec-pg: unreachable')

    vi.mocked(listCutCollections).mockRejectedValueOnce(new Error('down'))
    await toggle(w)
    await toggle(w)
    expect(w.find('[data-cut-collections-problems]').exists()).toBe(false)
    expect(w.find('[data-cut-collections-failed]').text()).toContain('The collections could not be listed')
  })

  it('cuts the saved text again, with its marks, when the edit goes back to view', async () => {
    vi.mocked(updateNote).mockImplementation(async (p: string, body: { content: string }) => note(p, body.content))
    const w = await mountNote()
    await toggle(w)
    expect(renderCut).toHaveBeenCalledTimes(1)
    await button(w, 'Edit').trigger('click')
    await flushPromises()
    w.findComponent({ name: 'CodeMirrorEditor' }).vm.$emit('update:modelValue', '# A\n\nnew text')
    await flushPromises()
    await button(w, 'Save').trigger('click')
    await flushPromises()
    const marks = vi.mocked(listMarks).mock.calls.length
    await button(w, 'View').trigger('click')
    await flushPromises()
    expect(renderCut).toHaveBeenCalledTimes(2)
    expect(vi.mocked(renderCut).mock.lastCall![0]).toBe('# A\n\nnew text')
    expect(listMarks).toHaveBeenCalledTimes(marks + 1)
  })
})
