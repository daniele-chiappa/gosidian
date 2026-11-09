import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { AxiosError, AxiosHeaders } from 'axios'
import enUI from '@catalogs/ui.en.json'
import type { Piece } from '@/api/layouts'

vi.mock('@/api/preview', async (orig) => ({
  ...(await orig<typeof import('@/api/preview')>()),
  renderPreviewData: vi.fn(async (md: string) => ({ html: `<p>${md}</p>`, views: [], diagramLinks: {} })),
}))
vi.mock('@/composables/useConfirm', async (orig) => ({
  ...(await orig<typeof import('@/composables/useConfirm')>()),
  confirmAction: vi.fn(async () => true),
}))
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
      props: { modelValue: { type: String, default: '' }, readonly: Boolean },
      emits: ['update:modelValue'],
      setup: (p, { emit, expose }) => {
        expose({ focus: () => {} })
        return () =>
          h('textarea', {
            'data-editor': '',
            readonly: p.readonly,
            value: p.modelValue,
            onInput: (e: Event) => emit('update:modelValue', (e.target as HTMLTextAreaElement).value),
          })
      },
    }),
  }
})

import { confirmAction } from '@/composables/useConfirm'
import PieceEditor from '@/components/domain/PieceEditor.vue'

// A piece of a note edited over its design (IMP-228).

const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
const text: Piece = { n: 3, kind: 'paragraph', text: 'Il testo.', hash: 'h3', line: 9, blocks: 1 }
const image: Piece = {
  n: 4,
  kind: 'image',
  text: '![Schema](/vault-files/p/attachments/a.png)',
  hash: 'h4',
  line: 12,
  blocks: 1,
  image: { src: '/vault-files/p/attachments/a.png', alt: 'Schema', path: 'p/attachments/a.png', described: 'Un disegno' },
}

function mountEditor(piece: Piece, more: Record<string, unknown> = {}) {
  const props = {
    path: 'p/a.md',
    project: 'p',
    piece,
    pick: vi.fn(async () => 'p/attachments/nuova.png' as string | null),
    write: vi.fn(async () => {}),
    reload: vi.fn(async () => null as Piece | null),
    ...more,
  }
  const stubs = { MarkdownPreview: { props: ['html'], template: '<div v-html="html" />' } }
  const w = mount(PieceEditor, { props, global: { plugins: [i18n()], stubs }, attachTo: document.body })
  return { w, props }
}
const conflict = () =>
  new AxiosError('x', '409', undefined, undefined, {
    status: 409,
    data: {},
    statusText: '',
    headers: {},
    config: { headers: new AxiosHeaders() },
  })

describe('PieceEditor (IMP-228)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('opens the text of a piece, with the piece as it reads beside it', async () => {
    const { w } = mountEditor(text)
    await flushPromises()
    expect((w.find('[data-editor]').element as HTMLTextAreaElement).value).toBe('Il testo.')
    expect(w.find('[data-piece-preview]').text()).toContain('Il testo.')
    expect(w.find('[data-piece-editor]').attributes('aria-label')).toBe('Piece 3')
    w.unmount()
  })

  it('reads as the text changes', async () => {
    const { w } = mountEditor(text)
    await flushPromises()
    await w.find('[data-editor]').setValue('Un altro testo.')
    await flushPromises()
    expect(w.find('[data-piece-preview]').text()).toContain('Un altro testo.')
    w.unmount()
  })

  it('saves what changed, and closes', async () => {
    const { w, props } = mountEditor(text)
    await flushPromises()
    expect(w.find('[data-piece-save]').attributes('disabled')).toBeDefined()
    await w.find('[data-editor]').setValue('Un altro testo.')
    await w.find('[data-piece-save]').trigger('click')
    await flushPromises()
    expect(props.write).toHaveBeenCalledWith(text, 'Un altro testo.')
    expect(w.emitted('close')).toHaveLength(1)
    w.unmount()
  })

  it('ends the text with an image chosen', async () => {
    const { w, props } = mountEditor(text)
    await flushPromises()
    await w.find('[data-piece-image]').trigger('click')
    await flushPromises()
    await w.find('[data-piece-save]').trigger('click')
    await flushPromises()
    expect(props.write).toHaveBeenCalledWith(text, 'Il testo.\n\n![](/vault-files/p/attachments/nuova.png)\n')
    w.unmount()
  })

  it('does nothing when no image is chosen', async () => {
    const { w, props } = mountEditor(text, { pick: vi.fn(async () => null) })
    await flushPromises()
    await w.find('[data-piece-image]').trigger('click')
    await flushPromises()
    expect(w.find('[data-piece-save]').attributes('disabled')).toBeDefined()
    expect(props.write).not.toHaveBeenCalled()
    w.unmount()
  })

  it('shows an image with its description, to change', async () => {
    const { w, props } = mountEditor(image)
    await flushPromises()
    expect(w.find('[data-editor]').exists()).toBe(false)
    expect(w.find('[data-piece-picture]').attributes('src')).toBe('/vault-files/p/attachments/a.png')
    expect(w.find('[data-piece-described]').text()).toBe('Un disegno')
    await w.find('[data-piece-alt]').setValue('Lo schema [nuovo]')
    await w.find('[data-piece-save]').trigger('click')
    await flushPromises()
    expect(props.write).toHaveBeenCalledWith(image, '![Lo schema  nuovo](/vault-files/p/attachments/a.png)')
    w.unmount()
  })

  it('replaces an image, keeping its description', async () => {
    const { w, props } = mountEditor(image)
    await flushPromises()
    await w.find('[data-piece-replace]').trigger('click')
    await flushPromises()
    expect(w.find('[data-piece-picture]').attributes('src')).toBe('/vault-files/p/attachments/nuova.png')
    await w.find('[data-piece-save]').trigger('click')
    await flushPromises()
    expect(props.write).toHaveBeenCalledWith(image, '![Schema](/vault-files/p/attachments/nuova.png)')
    w.unmount()
  })

  it('keeps an embed an embed', async () => {
    const embed: Piece = { ...image, text: '![[foto.png|Foto]]', image: { src: 'foto.png', alt: 'Foto' } }
    const { w, props } = mountEditor(embed)
    await flushPromises()
    await w.find('[data-piece-alt]').setValue('La foto')
    await w.find('[data-piece-save]').trigger('click')
    await flushPromises()
    expect(props.write).toHaveBeenCalledWith(embed, '![[foto.png|La foto]]')
    w.unmount()
  })

  it('says a piece changed meanwhile, keeps the text, and loads it on request', async () => {
    const now: Piece = { ...text, text: 'Scritto da un altro.', hash: 'h9' }
    const { w, props } = mountEditor(text, {
      write: vi.fn(async () => Promise.reject(conflict())),
      reload: vi.fn(async () => now),
    })
    await flushPromises()
    await w.find('[data-editor]').setValue('Il mio testo.')
    await w.find('[data-piece-save]').trigger('click')
    await flushPromises()
    expect(w.emitted('close')).toBeUndefined()
    expect(w.find('[data-piece-error]').text()).toContain('changed since')
    expect((w.find('[data-editor]').element as HTMLTextAreaElement).value).toBe('Il mio testo.')
    await w.find('[data-piece-reload]').trigger('click')
    await flushPromises()
    expect(props.reload).toHaveBeenCalledWith(3)
    expect((w.find('[data-editor]').element as HTMLTextAreaElement).value).toBe('Scritto da un altro.')
    expect(w.find('[data-piece-error]').exists()).toBe(false)
    w.unmount()
  })

  it('asks before losing what was written, and only then', async () => {
    const { w } = mountEditor(text)
    await flushPromises()
    await w.find('[data-piece-cancel]').trigger('click')
    await flushPromises()
    expect(confirmAction).not.toHaveBeenCalled()
    expect(w.emitted('close')).toHaveLength(1)
    await w.find('[data-editor]').setValue('Altro.')
    await w.find('[data-piece-editor]').trigger('keydown', { key: 'Escape' })
    await flushPromises()
    expect(confirmAction).toHaveBeenCalledTimes(1)
    w.unmount()
  })

  it('reads a version, with nothing to save', async () => {
    const { w } = mountEditor(text, { readonly: true })
    await flushPromises()
    expect(w.find('[data-piece-save]').exists()).toBe(false)
    expect(w.find('[data-piece-image]').exists()).toBe(false)
    expect(w.find('[data-editor]').attributes('readonly')).toBeDefined()
    w.unmount()
  })

  it('goes to the note at its line', async () => {
    const { w } = mountEditor(text)
    await flushPromises()
    await w.find('[data-piece-note]').trigger('click')
    expect(w.emitted('openNote')?.[0]).toEqual([9])
    w.unmount()
  })
})

describe('PieceEditor, the words of a frame (IMP-230)', () => {
  beforeEach(() => vi.mocked(confirmAction).mockClear())

  it('starts from the words the design changed, ready to save', async () => {
    const { w, props } = mountEditor(text, { proposed: 'Il testo nuovo.' })
    await flushPromises()
    expect((w.find('[data-editor]').element as HTMLTextAreaElement).value).toBe('Il testo nuovo.')
    expect(w.find('[data-piece-from-design]').exists()).toBe(true)
    await w.find('[data-piece-save]').trigger('click')
    await flushPromises()
    expect(props.write).toHaveBeenCalledWith(text, 'Il testo nuovo.')
    w.unmount()
  })

  it('shows the words of the design beside a piece changed on both sides, and takes them on request', async () => {
    const { w, props } = mountEditor(text, { design: 'Le parole del design.', putBack: vi.fn(async () => {}) })
    await flushPromises()
    expect((w.find('[data-editor]').element as HTMLTextAreaElement).value).toBe('Il testo.')
    expect(w.find('[data-piece-design]').text()).toContain('Le parole del design.')
    await w.find('[data-piece-use-design]').trigger('click')
    await w.find('[data-piece-save]').trigger('click')
    await flushPromises()
    expect(props.write).toHaveBeenCalledWith(text, 'Le parole del design.')
    w.unmount()
  })

  it("keeps the note's text: the frame takes it again", async () => {
    const putBack = vi.fn(async () => {})
    const { w, props } = mountEditor(text, { design: 'Le parole del design.', putBack })
    await flushPromises()
    await w.find('[data-piece-keep-note]').trigger('click')
    await flushPromises()
    expect(putBack).toHaveBeenCalled()
    expect(props.write).not.toHaveBeenCalled()
    expect(w.emitted('close')).toHaveLength(1)
    w.unmount()
  })
})
