import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { createPinia, setActivePinia } from 'pinia'
import enUI from '@catalogs/ui.en.json'
import type { Note } from '@/api/notes'

vi.mock('@/api/notes', () => ({ createNote: vi.fn() }))
vi.mock('@/api/attach', () => ({ attachFile: vi.fn() }))
vi.mock('@/stores/tree', () => ({ useTreeStore: () => ({ refresh: vi.fn() }) }))

import { createNote } from '@/api/notes'
import NoteCreateView from '@/views/NoteCreateView.vue'

const made = (path: string): Note => ({ path, title: 'alpha', content: '', etag: 'e1', size: 0, mod_time: '2026-11-01T10:00:00Z' })

function mountForm() {
  const openWindow = vi.fn()
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
  const w = mount(NoteCreateView, {
    props: { path: 'p' },
    global: { plugins: [i18n], provide: { openWindow } },
    attachTo: document.body,
  })
  return { w, openWindow }
}

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
