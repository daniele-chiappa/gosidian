import { beforeEach, describe, expect, it, vi } from 'vitest'
import { AxiosError, AxiosHeaders } from 'axios'
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
  deleteNote: vi.fn(),
}))
vi.mock('@/api/preview', async (orig) => ({
  ...(await orig<typeof import('@/api/preview')>()),
  renderPreviewData: vi.fn(async () => ({ html: '<p>text</p>', views: [], diagramLinks: {} })),
  renderCut: vi.fn(),
  listCutCollections: vi.fn(),
}))
vi.mock('@/api/marks', () => ({ listMarks: vi.fn() }))
// The view's note events and conflict announcements, for a test to fire.
const hooks = vi.hoisted(() => ({
  note: (_p: { path?: string; etag?: string; action?: string }) => {},
  conflict: (_d: { path: string; current_etag: string; current_size: number; current_content_excerpt: string }) => {},
  sessionExpired: () => {},
}))
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
  onApiEvent: (_name: string, fn: typeof hooks.conflict) => {
    hooks.conflict = fn
    return () => {}
  },
  onSessionExpired: (fn: typeof hooks.sessionExpired) => {
    hooks.sessionExpired = fn
    return () => {}
  },
}))
vi.mock('@/composables/useConfirm', async (orig) => ({
  ...(await orig<typeof import('@/composables/useConfirm')>()),
  confirmAction: vi.fn(async () => true),
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
// Immediate: a second cut request from the settings' watcher shows at once.
// The toolbar's width is the test's: 0, as unmeasured, unless it sets one.
const size = vi.hoisted(() => ({ width: { value: 0 } }))
vi.mock('@vueuse/core', async (orig) => {
  const { ref } = await import('vue')
  size.width = ref(0)
  return {
    ...(await orig<typeof import('@vueuse/core')>()),
    useDebounceFn: (fn: () => unknown) => fn,
    useElementSize: () => ({ width: size.width, height: ref(0) }),
  }
})
// The editor as a plain field the test types in through its v-model.
vi.mock('@/components/editor/CodeMirrorEditor.vue', async () => {
  const { defineComponent, h, ref } = await import('vue')
  return {
    __esModule: true,
    default: defineComponent({
      name: 'CodeMirrorEditor',
      props: { modelValue: { type: String, default: '' } },
      emits: ['update:modelValue'],
      setup: (_, { expose }) => {
        const el = ref<HTMLElement | null>(null)
        expose({ focus: () => el.value?.focus() })
        return () => h('div', { 'data-editor': '', tabindex: -1, ref: el })
      },
    }),
  }
})

import { deleteNote, getNote, updateNote } from '@/api/notes'
import { useAuthStore } from '@/stores/auth'
import { DRAFT_STORAGE_PREFIX } from '@/composables/useSessionReset'
import { listCutCollections, renderCut } from '@/api/preview'
import { listMarks } from '@/api/marks'
import CutPreview from '@/components/domain/CutPreview.vue'
import NoteView from '@/views/NoteView.vue'
import { announcement } from '@/composables/useAnnounce'

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

async function mountNote(path = 'p/a.md', attachTo?: HTMLElement, more: Record<string, unknown> = {}) {
  const w = mount(NoteView, {
    props: { path, ...more },
    attachTo,
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

const editor = (w: VueWrapper) => w.findComponent({ name: 'CodeMirrorEditor' })
async function edit(w: VueWrapper, text: string) {
  await button(w, 'Edit').trigger('click')
  await flushPromises()
  editor(w).vm.$emit('update:modelValue', text)
  await flushPromises()
}
const changedElsewhere = (w: VueWrapper) => {
  const banner = w.find('[data-changed-elsewhere]')
  return banner.exists() && banner.text().includes('Changed elsewhere while you were editing.')
}
const etagMismatch = () =>
  new AxiosError('412', '412', undefined, undefined, {
    status: 412,
    statusText: 'Precondition Failed',
    data: { error: { code: 'concurrency.etag_mismatch' } },
    headers: {},
    config: { headers: new AxiosHeaders() },
  })

describe('NoteView, the note events of its own save (BUG-144)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked(getNote).mockReset().mockImplementation(async (p: string) => note(p))
    vi.mocked(updateNote).mockReset()
    vi.mocked(listMarks).mockReset().mockResolvedValue([])
  })

  it('says nothing when the event of its own save comes before the reply', async () => {
    let reply: (n: Note) => void = () => {}
    vi.mocked(updateNote).mockReturnValueOnce(new Promise((r) => (reply = r)))
    const w = await mountNote()
    await edit(w, '# A\n\nnew text')
    await button(w, 'Save').trigger('click')
    await flushPromises()
    const saved = note('p/a.md', '# A\n\nnew text')
    hooks.note({ path: 'p/a.md', etag: `"${saved.etag}"` })
    await flushPromises()
    expect(changedElsewhere(w)).toBe(false)

    reply(saved)
    await flushPromises()
    expect(changedElsewhere(w)).toBe(false)
    expect(getNote).toHaveBeenCalledTimes(1)
  })

  it('shows another writer\'s change once the reply tells it from its own', async () => {
    let reply: (n: Note) => void = () => {}
    vi.mocked(updateNote).mockReturnValueOnce(new Promise((r) => (reply = r)))
    const w = await mountNote()
    await edit(w, '# A\n\nnew text')
    await button(w, 'Save').trigger('click')
    await flushPromises()
    hooks.note({ path: 'p/a.md', etag: '"e-other"' })
    // Typed while the save is on its way: the draft stays unsaved.
    editor(w).vm.$emit('update:modelValue', '# A\n\nnew text, more')
    await flushPromises()
    expect(changedElsewhere(w)).toBe(false)

    reply(note('p/a.md', '# A\n\nnew text'))
    await flushPromises()
    expect(changedElsewhere(w)).toBe(true)
  })

  it('reloads a clean note when another writer\'s change came during the save', async () => {
    let reply: (n: Note) => void = () => {}
    vi.mocked(updateNote).mockReturnValueOnce(new Promise((r) => (reply = r)))
    const w = await mountNote()
    await edit(w, '# A\n\nnew text')
    await button(w, 'Save').trigger('click')
    await flushPromises()
    hooks.note({ path: 'p/a.md', etag: '"e-other"' })
    await flushPromises()
    expect(getNote).toHaveBeenCalledTimes(1)

    reply(note('p/a.md', '# A\n\nnew text'))
    await flushPromises()
    expect(getNote).toHaveBeenCalledTimes(2)
    expect(changedElsewhere(w)).toBe(false)
  })

  it('drops the changed-elsewhere banner once the draft overwrote the change', async () => {
    vi.mocked(updateNote)
      .mockImplementationOnce(async () => {
        hooks.conflict({ path: 'p/a.md', current_etag: 'e-other', current_size: 5, current_content_excerpt: 'other' })
        throw etagMismatch()
      })
      .mockImplementationOnce(async (p: string, body: { content: string }) => note(p, body.content))
    const w = await mountNote()
    await edit(w, '# A\n\nnew text')
    hooks.note({ path: 'p/a.md', etag: '"e-other"' })
    await flushPromises()
    expect(changedElsewhere(w)).toBe(true)
    await button(w, 'Save').trigger('click')
    await flushPromises()
    expect(w.text()).toContain('Changed elsewhere since you opened it.')

    await button(w, 'Overwrite').trigger('click')
    await flushPromises()
    expect(vi.mocked(updateNote).mock.lastCall![1]).toEqual({ content: '# A\n\nnew text' })
    expect(w.text()).not.toContain('Changed elsewhere since you opened it.')
    expect(changedElsewhere(w)).toBe(false)
  })
})

describe('NoteView, its heading (IMP-217, F-03)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked(listMarks).mockReset().mockResolvedValue([])
  })

  it("names the note, by its file while it loads and then by its title", async () => {
    let reply: (n: Note) => void = () => {}
    vi.mocked(getNote).mockReset().mockReturnValueOnce(new Promise((r) => (reply = r)))
    const w = mount(NoteView, {
      props: { path: 'p/alpha.md' },
      global: { plugins: [i18n()], stubs: { MarkdownPreview: true, CutPreview: true, PropertiesPanel: true, RowViews: true } },
    })
    await flushPromises()
    expect(w.find('h1[data-window-heading]').text()).toBe('alpha')
    reply({ ...note('p/alpha.md'), title: 'Alpha, the first' })
    await flushPromises()
    expect(w.find('h1[data-window-heading]').text()).toBe('Alpha, the first')
  })

  it('puts the focus in the editor of a note just made, and nowhere else in another', async () => {
    vi.mocked(getNote).mockReset().mockImplementation(async (p: string) => note(p))
    const made = await mountNote('p/a.md', document.body, { mode: 'edit', editorFocus: true })
    expect(document.activeElement).toBe(made.find('[data-editor]').element)
    made.unmount()
    const other = await mountNote('p/a.md', document.body, { mode: 'edit' })
    expect(document.activeElement).toBe(document.body)
    other.unmount()
  })
})

describe('NoteView, a reload of the note shown (IMP-217, F-04)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked(listMarks).mockReset().mockResolvedValue([])
  })

  it('keeps the window as it is until the new version is there, its fields with it', async () => {
    let reply: (n: Note) => void = () => {}
    vi.mocked(getNote)
      .mockReset()
      .mockResolvedValueOnce(note('p/a.md'))
      .mockReturnValueOnce(new Promise((r) => (reply = r)))
    const w = await mountNote()
    // A field saved from the panel changes the note: its event comes back.
    hooks.note({ path: 'p/a.md', etag: '"e-new"' })
    await flushPromises()
    expect(getNote).toHaveBeenCalledTimes(2)
    expect(w.text()).not.toContain('Loading…')
    expect(w.find('properties-panel-stub').exists()).toBe(true)
    reply({ ...note('p/a.md'), etag: 'e-new' })
    await flushPromises()
    expect(w.find('properties-panel-stub').attributes('etag')).toBe('e-new')
  })
})

describe('NoteView, Save in the Tab order (IMP-217, F-04)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked(getNote).mockReset().mockImplementation(async (p: string) => note(p))
    vi.mocked(updateNote).mockReset().mockImplementation(async (p: string, body: { content: string }) => note(p, body.content))
    vi.mocked(listMarks).mockReset().mockResolvedValue([])
  })

  it('keeps Save reachable with nothing to save, and saves nothing then', async () => {
    const w = await mountNote()
    await button(w, 'Edit').trigger('click')
    await flushPromises()
    const save = button(w, 'Save')
    expect(save.attributes('disabled')).toBeUndefined()
    expect(save.attributes('aria-disabled')).toBe('true')
    await save.trigger('click')
    await flushPromises()
    expect(updateNote).not.toHaveBeenCalled()

    editor(w).vm.$emit('update:modelValue', '# A\n\nnew text')
    await flushPromises()
    expect(button(w, 'Save').attributes('aria-disabled')).toBeUndefined()
    await button(w, 'Save').trigger('click')
    await flushPromises()
    expect(updateNote).toHaveBeenCalledTimes(1)
  })
})

describe('NoteView, the save said and kept in sight (IMP-217, F-10)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked(getNote).mockReset().mockImplementation(async (p: string) => note(p))
    vi.mocked(updateNote).mockReset()
    vi.mocked(listMarks).mockReset().mockResolvedValue([])
    size.width.value = 0
    announcement.value = ''
  })

  it("says the save in the shell's region, and the overwrite after a conflict", async () => {
    vi.mocked(updateNote)
      .mockImplementationOnce(async (p: string, body: { content: string }) => note(p, body.content))
      .mockImplementationOnce(async () => {
        hooks.conflict({ path: 'p/a.md', current_etag: 'e-other', current_size: 5, current_content_excerpt: 'other' })
        throw etagMismatch()
      })
      .mockImplementationOnce(async (p: string, body: { content: string }) => note(p, body.content))
    const w = await mountNote()
    await edit(w, '# A\n\nnew text')
    await button(w, 'Save').trigger('click')
    await flushPromises()
    await vi.waitFor(() => expect(announcement.value).toMatch(/^saved at \S/))

    announcement.value = ''
    editor(w).vm.$emit('update:modelValue', '# A\n\nnewer text')
    await flushPromises()
    await button(w, 'Save').trigger('click')
    await flushPromises()
    expect(w.text()).toContain('Changed elsewhere since you opened it.')
    expect(announcement.value).toBe('')
    await button(w, 'Overwrite').trigger('click')
    await flushPromises()
    await vi.waitFor(() => expect(announcement.value).toMatch(/^saved at \S/))
  })

  it('says a change made elsewhere while the note is edited', async () => {
    const w = await mountNote()
    await edit(w, '# A\n\nnew text')
    hooks.note({ path: 'p/a.md', etag: '"e-other"' })
    await flushPromises()
    expect(changedElsewhere(w)).toBe(true)
    await vi.waitFor(() => expect(announcement.value).toBe('Changed elsewhere while you were editing.'))
  })

  it('keeps the save in sight in a narrow window: the time with a check, the whole in its tooltip', async () => {
    vi.mocked(updateNote).mockImplementationOnce(async (p: string, body: { content: string }) => note(p, body.content))
    const w = await mountNote()
    await edit(w, '# A\n\nnew text')
    await button(w, 'Save').trigger('click')
    await flushPromises()
    const wide = w.find('[data-saved-at]')
    expect(wide.text()).toMatch(/^saved at \S/)

    size.width.value = 300
    await flushPromises()
    const narrow = w.find('[data-saved-at]')
    expect(narrow.exists()).toBe(true)
    expect(narrow.attributes('title')).toBe(wide.text())
    expect(narrow.find('[data-saved-time]').text()).toBe(wide.text().replace(/^saved at /, ''))
    // A screen reader hears the whole, not a bare time.
    expect(narrow.find('.sr-only').text()).toBe(wide.text())
  })
})

describe('NoteView, the draft kept in the browser (BUG-142)', () => {
  const KEY = `${DRAFT_STORAGE_PREFIX}u1.p/a.md`
  const kept = () => JSON.parse(localStorage.getItem(KEY) ?? 'null')
  const keep = (text: string, etag: string) =>
    localStorage.setItem(KEY, JSON.stringify({ text, etag, at: Date.now() - 60_000 }))
  const offer = (w: VueWrapper) => w.findAll('[role=alert]').find((el) => el.text().includes('Unsaved changes from'))

  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    const auth = useAuthStore()
    auth.token = 'gsp_test'
    auth.user = { id: 'u1', username: 'claude', role: 'member' }
    vi.mocked(getNote).mockReset().mockImplementation(async (p: string) => note(p))
    vi.mocked(updateNote).mockReset()
    vi.mocked(deleteNote).mockReset().mockResolvedValue(undefined)
    vi.mocked(listMarks).mockReset().mockResolvedValue([])
  })

  it('keeps the draft while it is typed, with the version it was written on, and drops it once saved', async () => {
    vi.mocked(updateNote).mockImplementationOnce(async (p: string, body: { content: string }) => note(p, body.content))
    const w = await mountNote()
    await edit(w, '# A\n\nnew text')
    expect(kept()).toMatchObject({ text: '# A\n\nnew text', etag: note('p/a.md').etag })

    await button(w, 'Save').trigger('click')
    await flushPromises()
    expect(kept()).toBeNull()
  })

  it('keeps the draft at once when the session ends', async () => {
    const w = await mountNote()
    await edit(w, '# A\n\nnew text')
    localStorage.removeItem(KEY)
    hooks.sessionExpired()
    expect(kept()).toMatchObject({ text: '# A\n\nnew text' })
    w.unmount()
  })

  it('keeps nothing without an account', async () => {
    useAuthStore().user = null
    const w = await mountNote()
    await edit(w, '# A\n\nnew text')
    expect(localStorage.length).toBe(0)
    w.unmount()
  })

  it('offers a kept draft when the note opens, and saves it on the version it was written on', async () => {
    keep('# A\n\nmine', 'e-old')
    vi.mocked(updateNote).mockImplementationOnce(async () => {
      throw etagMismatch()
    })
    const w = await mountNote()
    expect(offer(w)).toBeDefined()

    await button(w, 'Restore').trigger('click')
    await flushPromises()
    expect(offer(w)).toBeUndefined()
    expect(editor(w).props('modelValue')).toBe('# A\n\nmine')
    // The note is no longer the version the draft was written on.
    expect(changedElsewhere(w)).toBe(true)

    await button(w, 'Save').trigger('click')
    await flushPromises()
    expect(vi.mocked(updateNote).mock.lastCall).toEqual(['p/a.md', { content: '# A\n\nmine', ifMatch: 'e-old' }])
    // Refused: the draft stays kept, on its version.
    expect(kept()).toMatchObject({ text: '# A\n\nmine', etag: 'e-old' })
  })

  it('restores a draft of the saved version without a word of a change elsewhere', async () => {
    const saved = note('p/a.md')
    keep('# A\n\nmine', saved.etag)
    vi.mocked(updateNote).mockImplementationOnce(async (p: string, body: { content: string }) => note(p, body.content))
    const w = await mountNote()
    await button(w, 'Restore').trigger('click')
    await flushPromises()
    expect(changedElsewhere(w)).toBe(false)

    await button(w, 'Save').trigger('click')
    await flushPromises()
    expect(vi.mocked(updateNote).mock.lastCall).toEqual(['p/a.md', { content: '# A\n\nmine', ifMatch: saved.etag }])
    expect(kept()).toBeNull()
  })

  // The banner's buttons go with it: the focus must not fall to the page,
  // where Ctrl+S is the browser's and typing goes nowhere.
  it('puts the focus in the editor on Restore, where typing and Ctrl+S go on', async () => {
    const saved = note('p/a.md')
    keep('# A\n\nmine', saved.etag)
    vi.mocked(updateNote).mockImplementationOnce(async (p: string, body: { content: string }) => note(p, body.content))
    const w = await mountNote('p/a.md', document.body)
    await button(w, 'Restore').trigger('click')
    await flushPromises()
    expect(document.activeElement).toBe(w.find('[data-editor]').element)

    window.dispatchEvent(new KeyboardEvent('keydown', { key: 's', ctrlKey: true }))
    await flushPromises()
    expect(vi.mocked(updateNote).mock.lastCall).toEqual(['p/a.md', { content: '# A\n\nmine', ifMatch: saved.etag }])
    w.unmount()
  })

  it('keeps the focus in the window on Discard', async () => {
    keep('# A\n\nmine', 'e-old')
    const w = await mountNote('p/a.md', document.body)
    const discard = button(w, 'Discard')
    ;(discard.element as HTMLElement).focus()
    await discard.trigger('click')
    await flushPromises()
    expect(document.activeElement).not.toBe(document.body)
    expect(w.element.contains(document.activeElement)).toBe(true)
    w.unmount()
  })

  it('drops the kept draft on Discard', async () => {
    keep('# A\n\nmine', 'e-old')
    const w = await mountNote()
    await button(w, 'Discard').trigger('click')
    await flushPromises()
    expect(offer(w)).toBeUndefined()
    expect(kept()).toBeNull()
  })

  it('drops a kept draft that is the saved text, without asking', async () => {
    keep(note('p/a.md').content, 'e-old')
    const w = await mountNote()
    expect(offer(w)).toBeUndefined()
    expect(kept()).toBeNull()
  })

  it('drops the draft for the saved version', async () => {
    const w = await mountNote()
    await edit(w, '# A\n\nnew text')
    expect(kept()).not.toBeNull()
    hooks.note({ path: 'p/a.md', etag: '"e-other"' })
    await flushPromises()
    await button(w, 'Load the saved version').trigger('click')
    await flushPromises()
    expect(getNote).toHaveBeenCalledTimes(2)
    expect(kept()).toBeNull()
  })

  it('drops the draft of a deleted note', async () => {
    const w = await mountNote()
    await edit(w, '# A\n\nnew text')
    expect(kept()).not.toBeNull()
    await button(w, 'Delete').trigger('click')
    await flushPromises()
    expect(deleteNote).toHaveBeenCalledWith('p/a.md')
    expect(kept()).toBeNull()
  })
})
