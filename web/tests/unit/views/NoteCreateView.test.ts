import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { createPinia, setActivePinia } from 'pinia'
import { AxiosError, AxiosHeaders } from 'axios'
import enUI from '@catalogs/ui.en.json'
import type { Note } from '@/api/notes'

vi.mock('@/api/notes', () => ({ createNote: vi.fn() }))
vi.mock('@/api/attach', () => ({ attachFile: vi.fn() }))
const query = vi.hoisted(() => ({ run: vi.fn(async (_body: unknown) => ({ notes: [] as unknown[], count: 0 })) }))
vi.mock('@/api/query', () => ({ runQuery: query.run }))
// The app a plugin offers for a page layout (IMP-226): none unless a test says.
const apps = vi.hoisted(() => ({ forType: vi.fn(async (_type: string): Promise<unknown> => null) }))
vi.mock('@/composables/pluginApps', () => ({ appForType: apps.forType }))
vi.mock('@/stores/tree', async (orig) => ({
  ...(await orig<typeof import('@/stores/tree')>()),
  useTreeStore: () => ({ refresh: vi.fn() }),
}))

import { createNote } from '@/api/notes'
import NoteCreateView from '@/views/NoteCreateView.vue'

const made = (path: string): Note => ({ path, title: 'alpha', content: '', etag: 'e1', size: 0, mod_time: '2026-11-01T10:00:00Z' })

function mountForm(more: Record<string, unknown> = {}) {
  const openWindow = vi.fn()
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
  const w = mount(NoteCreateView, {
    props: { path: 'p', ...more },
    global: { plugins: [i18n], provide: { openWindow } },
    attachTo: document.body,
  })
  return { w, openWindow }
}

describe('NoteCreateView, a page layout (IMP-226)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked(createNote).mockReset()
    apps.forType.mockReset().mockResolvedValue({ note_type: 'layout', status: 'ok' })
  })

  it('makes a note type: layout and opens it in its app', async () => {
    vi.mocked(createNote).mockResolvedValue(made('p/rivista.md'))
    const { w, openWindow } = mountForm()
    await flushPromises()
    expect(apps.forType).toHaveBeenCalledWith('layout')
    await w.find('[data-kind-layout]').trigger('click')
    await w.find('input[type="text"]').setValue('rivista')
    await w.find('form').trigger('submit')
    await flushPromises()
    const [path, content] = vi.mocked(createNote).mock.calls[0]!
    expect(path).toBe('p/rivista.md')
    expect(content).toContain('type: layout')
    expect(openWindow).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'app', key: 'app:p/rivista.md', props: { path: 'p/rivista.md' } }),
    )
    w.unmount()
  })

  it('offers it only when an app opens it', async () => {
    apps.forType.mockResolvedValue(null)
    const { w } = mountForm({ startKind: 'layout' })
    await flushPromises()
    expect(w.find('[data-kind-layout]').exists()).toBe(false)
    vi.mocked(createNote).mockResolvedValue(made('p/alpha.md'))
    await w.find('input[type="text"]').setValue('alpha')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(vi.mocked(createNote).mock.calls[0]![1]).not.toContain('type: layout')
    w.unmount()
  })

  it('starts on the page chosen, or on a size of its own (IMP-232)', async () => {
    vi.mocked(createNote).mockResolvedValue(made('p/rivista.md'))
    const { w } = mountForm({ startKind: 'layout' })
    await flushPromises()
    await w.find('input[type="text"]').setValue('rivista')
    await w.find('[data-layout-page]').setValue('A5')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(vi.mocked(createNote).mock.calls[0]![1]).toContain('layout_page: A5')
    w.unmount()
    const other = mountForm({ startKind: 'layout' }).w
    await flushPromises()
    await other.find('input[type="text"]').setValue('rivista')
    await other.find('[data-layout-page]').setValue('custom')
    await other.find('[data-layout-width]').setValue('170')
    await other.find('[data-layout-height]').setValue('240')
    await other.find('form').trigger('submit')
    await flushPromises()
    expect(vi.mocked(createNote).mock.calls.at(-1)![1]).toContain('layout_page: "170x240mm"')
    other.unmount()
  })

  it('starts from another page layout, which it lists, and then has no page of its own', async () => {
    query.run.mockResolvedValue({ notes: [{ path: 'p/modello.md', title: 'Modello', modified: '' }], count: 1 })
    vi.mocked(createNote).mockResolvedValue(made('p/rivista.md'))
    const { w } = mountForm({ startKind: 'layout' })
    await flushPromises()
    expect(query.run).toHaveBeenCalledWith(expect.objectContaining({ where: [{ field: 'type', op: 'eq', value: 'layout' }] }))
    await w.find('input[type="text"]').setValue('rivista')
    await w.find('[data-layout-template]').setValue('p/modello.md')
    expect(w.find('[data-layout-page]').exists()).toBe(false)
    await w.find('form').trigger('submit')
    await flushPromises()
    const content = vi.mocked(createNote).mock.calls[0]![1]
    expect(content).toContain('layout_template: p/modello.md')
    expect(content).not.toContain('layout_page')
    w.unmount()
  })

  it('starts on it when asked', async () => {
    const { w } = mountForm({ startKind: 'layout' })
    await flushPromises()
    expect(w.find('[data-kind-layout]').classes()).toContain('bg-accent')
    w.unmount()
  })
})

describe('NoteCreateView, where the focus goes (IMP-217, F-04)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked(createNote).mockReset()
  })

  it('opens the new note in the editor, with the focus there', async () => {
    vi.mocked(createNote).mockResolvedValue(made('p/alpha.md'))
    const { w, openWindow } = mountForm()
    await w.find('input[type="text"]').setValue('alpha')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(openWindow).toHaveBeenCalledWith({
      type: 'note',
      key: 'note:p/alpha.md',
      title: 'alpha',
      props: { path: 'p/alpha.md', mode: 'edit', editorFocus: true },
    })
    w.unmount()
  })

  it('puts the focus back on the name, with the reason tied to it, when the server refuses', async () => {
    vi.mocked(createNote).mockRejectedValue(new Error('exists'))
    const { w, openWindow } = mountForm()
    const nameField = w.find('input[type="text"]')
    await nameField.setValue('alpha')
    const submit = w.find('button[type="submit"]').element as HTMLButtonElement
    submit.focus()
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(openWindow).not.toHaveBeenCalled()
    expect(document.activeElement).toBe(nameField.element)
    const reason = w.find('[role="alert"]')
    expect(reason.attributes('id')).toBeTruthy()
    expect(nameField.attributes('aria-describedby')).toBe(reason.attributes('id'))
    expect(nameField.attributes('aria-invalid')).toBe('true')
    w.unmount()
  })

  it('puts the focus on the image field when no image was chosen', async () => {
    const { w } = mountForm()
    await w.findAll('button').find((b) => b.text() === 'Image')!.trigger('click')
    await w.find('input[type="text"]').setValue('photo')
    await w.find('form').trigger('submit')
    await flushPromises()
    const fileField = w.find('input[type="file"]')
    expect(document.activeElement).toBe(fileField.element)
    expect(fileField.attributes('aria-describedby')).toBe(w.find('[role="alert"]').attributes('id'))
    expect(w.find('input[type="text"]').attributes('aria-invalid')).toBeUndefined()
    w.unmount()
  })
})

describe('NoteCreateView, a name already taken (IMP-220, F-24)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked(createNote).mockReset()
  })

  it('says which file exists, under the name, and not the server\'s English', async () => {
    vi.mocked(createNote).mockRejectedValue(
      new AxiosError('conflict', '409', undefined, undefined, {
        status: 409,
        statusText: 'Conflict',
        headers: {},
        config: { headers: new AxiosHeaders() },
        data: { error: { code: 'resource.conflict', message: 'note already exists' } },
      }),
    )
    const { w } = mountForm()
    const nameField = w.find('input[type="text"]')
    await nameField.setValue('board')
    await w.find('form').trigger('submit')
    await flushPromises()
    const reason = w.find('[role="alert"]')
    expect(reason.text()).toBe('p/board.md already exists: choose another name.')
    expect(document.activeElement).toBe(nameField.element)
    expect(nameField.attributes('aria-describedby')).toBe(reason.attributes('id'))
    // Right under the name's field, before the title's.
    const label = nameField.element.closest('label')!
    expect(label.nextElementSibling).toBe(reason.element)
    w.unmount()
  })
})
