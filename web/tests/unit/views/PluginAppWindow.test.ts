import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'

vi.mock('@/api/notes', () => ({ getNote: vi.fn() }))
vi.mock('@/composables/useSSE', () => ({ useSSE: () => ({ on: vi.fn(), off: vi.fn() }) }))
const windows = vi.hoisted(() => ({ open: vi.fn(() => 'win-1'), identify: vi.fn() }))
vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => windows,
}))
vi.mock('@/api/layouts', async (orig) => ({
  ...(await orig<typeof import('@/api/layouts')>()),
  listPluginApps: vi.fn(),
  listPieces: vi.fn(async () => ({ etag: '"n"', pieces: [] })),
  numberPieces: vi.fn(),
  reorderPieces: vi.fn(),
  addPiece: vi.fn(),
  openLayout: vi.fn(),
  saveLayout: vi.fn(),
  exportLayout: vi.fn(),
  listImages: vi.fn(),
  readVaultFile: vi.fn(),
}))

vi.mock('@/api/attach', () => ({ attachFile: vi.fn() }))
vi.mock('@/api/history', async (orig) => ({
  ...(await orig<typeof import('@/api/history')>()),
  getVersions: vi.fn(async () => ({ items: [], valid: null, can_admin: true })),
  tagVersion: vi.fn(async () => ({ tag: 'note/rivista/n1.md/V1', label: 'V1' })),
  setValidVersion: vi.fn(),
}))
vi.mock('@/stores/access', async (orig) => ({
  ...(await orig<typeof import('@/stores/access')>()),
  useAccessStore: () => ({ canWrite: () => true }),
}))

import { getNote, type Note } from '@/api/notes'
import { setValidVersion, tagVersion } from '@/api/history'
import { attachFile } from '@/api/attach'
import {
  listImages,
  exportLayout,
  listPieces,
  listPluginApps,
  openLayout,
  readVaultFile,
  saveLayout,
  type PluginApp,
} from '@/api/layouts'
import PluginAppWindow from '@/views/PluginAppWindow.vue'

// A note in the app a plugin offers for its type (IMP-226).

const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
const layoutApp: PluginApp = {
  plugin: 'layout',
  name: 'layout',
  title: 'Impaginazione',
  url: 'https://layout.example.com/app/',
  origin: 'https://layout.example.com',
  note_type: 'layout',
  bridge: 'layout/2',
  engine: 'designcraft v0.17.0',
  status: 'ok',
}
const note = (content: string): Note => ({
  path: 'rivista/n1.md',
  title: 'n1',
  content,
  etag: 'E',
  size: 1,
  mod_time: '',
})

function mountWindow() {
  return mount(PluginAppWindow, {
    props: { path: 'rivista/n1.md' },
    global: { plugins: [i18n()] },
    attachTo: document.body,
  })
}

// happy-dom loads no page in an iframe: the frame's window is the test's.
function frameWindow(w: ReturnType<typeof mountWindow>): Window {
  const win = { postMessage: vi.fn() } as unknown as Window
  Object.defineProperty(w.find('iframe').element, 'contentWindow', { value: win })
  return win
}

describe('PluginAppWindow', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(listPluginApps).mockResolvedValue([layoutApp])
    vi.mocked(getNote).mockResolvedValue(note('---\ntype: layout\n---\n'))
  })

  it("frames the app of the note's type, with the sandbox every app gets", async () => {
    const w = mountWindow()
    await flushPromises()
    const frame = w.find('iframe[data-app-frame]')
    expect(frame.exists()).toBe(true)
    const src = new URL(frame.attributes('src')!)
    expect(src.origin + src.pathname).toBe('https://layout.example.com/app/')
    expect(src.searchParams.get('host')).toBe('gosidian')
    expect(src.searchParams.get('origin')).toBe(window.location.origin)
    expect(src.searchParams.get('lang')).toBe('en')
    expect(frame.attributes('sandbox')).toBe(
      'allow-scripts allow-same-origin allow-downloads allow-popups',
    )
    expect(frame.attributes('allow')).toBe('fullscreen; clipboard-read; clipboard-write')
    expect(w.emitted('title')?.[0]).toEqual(['n1 · editor'])
    w.unmount()
  })

  it('lays out a note of any type with the app of the layout bridge', async () => {
    vi.mocked(listPluginApps).mockResolvedValue([layoutApp])
    vi.mocked(getNote).mockResolvedValue(note('---\ntype: plan\n---\n# Testo\n'))
    const w = mountWindow()
    await flushPromises()
    expect(w.find('iframe').exists()).toBe(true)
    expect(w.find('[data-app-problem]').exists()).toBe(false)
    w.unmount()
  })

  it('says why it frames nothing', async () => {
    for (const [apps, content, says] of [
      [
        [{ ...layoutApp, note_type: 'bot', bridge: 'bot/1' }],
        '---\ntype: chat\n---\n',
        'No app opens the notes of type “chat”',
      ],
      [[{ ...layoutApp, bridge: 'layout/9' }], '---\ntype: layout\n---\n', 'bridge layout/9'],
      [[{ ...layoutApp, status: 'changed' }], '---\ntype: layout\n---\n', 'status: changed'],
    ] as const) {
      vi.mocked(listPluginApps).mockResolvedValue([...apps])
      vi.mocked(getNote).mockResolvedValue(note(content))
      const w = mountWindow()
      await flushPromises()
      expect(w.find('iframe').exists()).toBe(false)
      expect(w.find('[data-app-problem]').text()).toContain(says)
      w.unmount()
    }
  })

  it('opens the note when its frame says ready, and only then', async () => {
    vi.mocked(openLayout).mockResolvedValue({ bytes: null, etag: '"E"', missing: [] })
    const w = mountWindow()
    await flushPromises()
    const frameWin = frameWindow(w)
    window.dispatchEvent(
      new MessageEvent('message', {
        data: { bridge: 'layout/2', type: 'ready' },
        origin: 'https://evil.example.com',
        source: frameWin,
      }),
    )
    await flushPromises()
    expect(openLayout).not.toHaveBeenCalled()
    window.dispatchEvent(
      new MessageEvent('message', {
        data: { bridge: 'layout/2', type: 'ready' },
        origin: layoutApp.origin,
        source: frameWin,
      }),
    )
    await flushPromises()
    expect(openLayout).toHaveBeenCalledWith('rivista/n1.md')
    w.unmount()
  })

  describe('the bar', () => {
    async function ready() {
      vi.mocked(openLayout).mockResolvedValue({ bytes: null, etag: '"E"', missing: [] })
      const w = mountWindow()
      await flushPromises()
      const frameWin = frameWindow(w)
      const before = w.find('[data-app-actions]').exists()
      window.dispatchEvent(
        new MessageEvent('message', {
          data: { bridge: 'layout/2', type: 'ready', engine: 'designcraft 0.17.0' },
          origin: layoutApp.origin,
          source: frameWin,
        }),
      )
      await flushPromises()
      const sent = () => vi.mocked(frameWin.postMessage).mock.lastCall?.[0]
      return { w, before, sent, frameWin }
    }

    it('opens a piece over the design, from where the note is a click away (IMP-228)', async () => {
      vi.mocked(listPieces).mockResolvedValueOnce({
        etag: '"n"',
        pieces: [{ n: 2, kind: 'paragraph', text: 'Due.', hash: 'h', line: 7, blocks: 1 }],
      })
      const { w, frameWin } = await ready()
      window.dispatchEvent(
        new MessageEvent('message', {
          data: { bridge: 'layout/2', type: 'open_piece', n: '2', line: 7 },
          origin: layoutApp.origin,
          source: frameWin,
        }),
      )
      await flushPromises()
      expect(w.find('[data-piece-editor]').exists()).toBe(true)
      expect(windows.open).not.toHaveBeenCalled()
      await w.find('[data-piece-note]').trigger('click')
      await flushPromises()
      expect(w.find('[data-piece-editor]').exists()).toBe(false)
      expect(windows.identify).toHaveBeenCalledWith(
        'win-1',
        expect.any(String),
        expect.objectContaining({ mode: 'edit', line: 7 }),
      )
      w.unmount()
    })

    it('comes with the editor, not before', async () => {
      const { w, before } = await ready()
      expect(before).toBe(false)
      expect(w.find('[data-app-actions]').exists()).toBe(true)
      w.unmount()
    })

    it('saves and places an image through the editor', async () => {
      const { w, sent } = await ready()
      await w.find('[data-app-save]').trigger('click')
      expect(sent()).toEqual({ bridge: 'layout/2', type: 'save' })
      await w.find('[data-app-place]').trigger('click')
      expect(sent()).toEqual({ bridge: 'layout/2', type: 'run', id: 'app.placeDialog' })
      w.unmount()
    })

    it('exports to the format chosen', async () => {
      const { w, sent } = await ready()
      await w.find('[data-app-export] [data-overflow-button]').trigger('click')
      const items = w.findAll('[data-app-export] [role="menuitem"]')
      expect(items.map((i) => i.text())).toEqual([
        'PDF',
        'PDF in the vault',
        'PNG (page)',
        'IDML',
        'EPUB',
        'HTML',
        'PowerPoint',
      ])
      await items[2]!.trigger('click')
      expect(sent()).toEqual({ bridge: 'layout/2', type: 'run', id: 'app.exportPng' })
      w.unmount()
    })

    it('keeps a PDF beside the note, and says so with the way to open it (IMP-229)', async () => {
      vi.mocked(exportLayout).mockResolvedValueOnce({ path: 'rivista/attachments/p.pdf', label: 'draft', etag: '"x"' })
      const { w, sent, frameWin } = await ready()
      await w.find('[data-app-export] [data-overflow-button]').trigger('click')
      await w.findAll('[data-app-export] [role="menuitem"]')[1]!.trigger('click')
      expect(sent()).toEqual({ bridge: 'layout/2', type: 'run', id: 'app.exportPdf', to_host: true })
      const bytes = new ArrayBuffer(4)
      window.dispatchEvent(
        new MessageEvent('message', {
          data: { bridge: 'layout/2', type: 'export', name: 'n1.pdf', mime: 'application/pdf', bytes },
          origin: layoutApp.origin,
          source: frameWin,
        }),
      )
      await flushPromises()
      expect(exportLayout).toHaveBeenCalledWith('rivista/n1.md', 'n1.pdf', bytes, undefined)
      const notice = w.find('[data-app-exported]')
      expect(notice.text()).toContain('PDF draft kept beside the note.')
      expect(notice.find('a').attributes('href')).toBe('/vault-files/rivista/attachments/p.pdf')
      w.unmount()
    })

    it('shows the references of the project, from the plugin, and goes back', async () => {
      const { w } = await ready()
      const button = w.find('[data-app-references]')
      ;(button.element as HTMLElement).focus()
      await button.trigger('click')
      await flushPromises()
      const dialog = w.find('[data-app-about]')
      expect(dialog.attributes('role')).toBe('dialog')
      const frame = dialog.find('iframe')
      expect(frame.attributes('src')).toBe('https://layout.example.com/app/about')
      expect(frame.attributes('sandbox')).toBe('allow-popups allow-popups-to-escape-sandbox')
      expect(document.activeElement).toBe(dialog.find('[data-about-close]').element)
      await dialog.trigger('keydown', { key: 'Escape' })
      await flushPromises()
      expect(w.find('[data-app-about]').exists()).toBe(false)
      expect(document.activeElement).toBe(button.element)
      w.unmount()
    })
  })

  describe('a note changed since the editor opened it', () => {
    async function refused() {
      vi.mocked(openLayout).mockResolvedValue({ bytes: null, etag: '"E"', missing: [] })
      vi.mocked(saveLayout).mockRejectedValue(
        Object.assign(new Error('note was modified'), { response: { status: 412 } }),
      )
      const w = mountWindow()
      await flushPromises()
      const frameWin = frameWindow(w)
      const posted: Record<string, unknown>[] = []
      frameWin.postMessage = ((msg: Record<string, unknown>) =>
        posted.push(msg)) as typeof frameWin.postMessage
      const send = async (win: Window, data: Record<string, unknown>) => {
        window.dispatchEvent(
          new MessageEvent('message', {
            data: { bridge: 'layout/2', ...data },
            origin: layoutApp.origin,
            source: win,
          }),
        )
        await flushPromises()
      }
      await send(frameWin, { type: 'ready' })
      await send(frameWin, { type: 'dirty', dirty: true })
      await send(frameWin, { type: 'save', name: 'n1.designcraft', bytes: new ArrayBuffer(1) })
      return { w, frameWin, posted, send }
    }

    it('says so and offers the two ways out', async () => {
      const { w } = await refused()
      expect(w.find('[data-app-conflict]').attributes('role')).toBe('alert')
      expect(w.find('[data-app-conflict]').text()).toContain('changed since the editor opened it')
      expect(w.find('[data-app-overwrite]').exists()).toBe(true)
      expect(w.find('[data-app-reload]').exists()).toBe(true)
      w.unmount()
    })

    it('overwrites on request, asking the editor for the document', async () => {
      const { w, posted, frameWin, send } = await refused()
      vi.mocked(saveLayout).mockResolvedValue({ etag: '"E2"' })
      await w.find('[data-app-overwrite]').trigger('click')
      expect(posted.at(-1)).toMatchObject({ bridge: 'layout/2', type: 'save' })
      await send(frameWin, { type: 'save', name: 'n1.designcraft', bytes: new ArrayBuffer(2) })
      expect(vi.mocked(saveLayout).mock.lastCall?.[2]).toBe('')
      expect(w.find('[data-app-conflict]').exists()).toBe(false)
      w.unmount()
    })

    it('reloads on request, a new frame that opens the note again', async () => {
      const { w, frameWin, send } = await refused()
      const old = w.find('iframe').element
      await w.find('[data-app-reload]').trigger('click')
      await flushPromises()
      const fresh = w.find('iframe').element
      expect(fresh).not.toBe(old)
      const freshWin = frameWindow(w)
      expect(w.find('[data-app-conflict]').exists()).toBe(false)
      expect(w.find('[data-unsaved]').exists()).toBe(false)
      vi.mocked(openLayout).mockClear()
      await send(frameWin, { type: 'ready' })
      expect(openLayout).not.toHaveBeenCalled()
      await send(freshWin, { type: 'ready' })
      expect(openLayout).toHaveBeenCalledWith('rivista/n1.md')
      w.unmount()
    })
  })

  it('shows the images of the project when the editor places one', async () => {
    vi.mocked(listImages).mockResolvedValue([
      { path: 'rivista/attachments/a.png', size: 10, modified: '', image: true },
      { path: 'rivista/attachments/b.jpg', size: 20, modified: '', image: true },
    ])
    vi.mocked(readVaultFile).mockResolvedValue(new Uint8Array([1]).buffer)
    const w = mountWindow()
    await flushPromises()
    const frameWin = frameWindow(w)
    const posted: unknown[] = []
    frameWin.postMessage = ((msg: unknown) => posted.push(msg)) as typeof frameWin.postMessage
    window.dispatchEvent(
      new MessageEvent('message', {
        data: { bridge: 'layout/2', type: 'pick', id: 3, purpose: 'place' },
        origin: layoutApp.origin,
        source: frameWin,
      }),
    )
    await flushPromises()
    expect(listImages).toHaveBeenCalledWith('rivista')
    const choices = w.findAll('[data-pick-image]')
    expect(choices.map((c) => c.attributes('data-path'))).toEqual([
      'rivista/attachments/a.png',
      'rivista/attachments/b.jpg',
    ])
    expect(document.activeElement).toBe(choices[0]!.element)
    await choices[1]!.trigger('click')
    await flushPromises()
    expect(readVaultFile).toHaveBeenCalledWith('rivista/attachments/b.jpg')
    expect(w.find('[data-app-picker]').exists()).toBe(false)
    expect(posted.at(-1)).toMatchObject({
      bridge: 'layout/2',
      type: 'file',
      id: 3,
      name: 'rivista/attachments/b.jpg',
    })
    w.unmount()
  })
  it('takes an image from this computer among the ones it offers (IMP-228)', async () => {
    vi.mocked(listImages).mockResolvedValue([])
    vi.mocked(attachFile).mockResolvedValue({ path: 'rivista/attachments/nuova.png', filename: 'nuova.png', markdown: '' })
    vi.mocked(readVaultFile).mockResolvedValue(new Uint8Array([1]).buffer)
    const w = mountWindow()
    await flushPromises()
    const frameWin = frameWindow(w)
    const posted: unknown[] = []
    frameWin.postMessage = ((msg: unknown) => posted.push(msg)) as typeof frameWin.postMessage
    window.dispatchEvent(
      new MessageEvent('message', {
        data: { bridge: 'layout/2', type: 'pick', id: 4, purpose: 'place' },
        origin: layoutApp.origin,
        source: frameWin,
      }),
    )
    await flushPromises()
    const input = w.find('[data-pick-upload]')
    const file = new File([new Uint8Array([1])], 'nuova.png', { type: 'image/png' })
    Object.defineProperty(input.element, 'files', { value: [file], configurable: true })
    await input.trigger('change')
    await flushPromises()
    expect(attachFile).toHaveBeenCalledWith(file, 'rivista')
    expect(w.find('[data-app-picker]').exists()).toBe(false)
    expect(posted.at(-1)).toMatchObject({ type: 'file', id: 4, name: 'rivista/attachments/nuova.png' })
    w.unmount()
  })

  it('opens a version to read: no save, nothing to keep (IMP-201)', async () => {
    const tag = 'note/rivista/n1.md/V3'
    const w = mount(PluginAppWindow, {
      props: { path: 'rivista/n1.md', version: tag },
      global: { plugins: [i18n()] },
      attachTo: document.body,
    })
    await flushPromises()
    frameWindow(w)
    vi.mocked(openLayout).mockResolvedValue({ bytes: null, etag: '"e"', missing: [] })
    window.dispatchEvent(
      new MessageEvent('message', {
        data: { bridge: 'layout/2', type: 'ready' },
        origin: layoutApp.origin,
        source: w.find('iframe').element.contentWindow,
      }),
    )
    await flushPromises()
    expect(openLayout).toHaveBeenCalledWith('rivista/n1.md', tag)
    expect(w.find('[data-read-only]').text()).toBe('Version V3, read-only')
    expect(listPieces).toHaveBeenCalledWith('rivista/n1.md', tag)
    expect(w.find('[data-app-save]').exists()).toBe(false)
    expect(w.find('[data-app-place]').exists()).toBe(false)
    expect(saveLayout).not.toHaveBeenCalled()
    // Its PDF is that version's, beside the note (IMP-229).
    vi.mocked(exportLayout).mockResolvedValueOnce({ path: 'rivista/attachments/v3.pdf', label: 'V3', etag: '"x"' })
    const bytes = new ArrayBuffer(4)
    window.dispatchEvent(
      new MessageEvent('message', {
        data: { bridge: 'layout/2', type: 'export', name: 'n1.pdf', bytes },
        origin: layoutApp.origin,
        source: w.find('iframe').element.contentWindow,
      }),
    )
    await flushPromises()
    expect(exportLayout).toHaveBeenCalledWith('rivista/n1.md', 'n1.pdf', bytes, tag)
    expect(w.find('[data-app-exported]').text()).toContain('PDF V3 kept beside the note.')
    w.unmount()
  })

  describe('the words a frame changed in the design (IMP-230)', () => {
    const two = { n: 2, kind: 'paragraph' as const, text: 'Due.', hash: 'h', line: 7, blocks: 1 }
    const ready = async (w: ReturnType<typeof mountWindow>) => {
      await flushPromises()
      const frame = frameWindow(w)
      const say = async (data: Record<string, unknown>) => {
        window.dispatchEvent(new MessageEvent('message', { data: { bridge: 'layout/2', ...data }, origin: layoutApp.origin, source: frame }))
        await flushPromises()
      }
      await say({ type: 'ready' })
      return { frame, say }
    }
    beforeEach(() => {
      vi.mocked(openLayout).mockResolvedValue({ bytes: null, etag: '"e"', missing: [] })
      vi.mocked(listPieces).mockResolvedValue({ etag: '"n"', pieces: [two] })
    })

    it('says, once the design is saved, which pieces the note lacks, and brings them all', async () => {
      vi.mocked(saveLayout).mockResolvedValue({ etag: '"e2"' })
      const w = mountWindow()
      const { frame, say } = await ready(w)
      await say({ type: 'edited', pieces: '[["2","Inizio del pezzo"]]' })
      expect(w.find('[data-app-edited]').exists()).toBe(false)
      await say({ type: 'save', bytes: new ArrayBuffer(4) })
      expect(w.find('[data-app-edited]').text()).toContain('Inizio del pezzo')
      await w.find('[data-app-bring-all]').trigger('click')
      expect(frame.postMessage).toHaveBeenCalledWith({ bridge: 'layout/2', type: 'bring_all' }, layoutApp.origin, [])
      await say({ type: 'edited', pieces: '[]' })
      expect(w.find('[data-app-edited]').exists()).toBe(false)
      w.unmount()
    })

    it('opens the words of a frame over the design, to look at before they go', async () => {
      const w = mountWindow()
      const { say } = await ready(w)
      await say({ type: 'bring_piece', n: '2', hash: 'h', frame: 41, text: 'Due nuovo.', exact: false })
      expect(w.find('[data-piece-editor]').exists()).toBe(true)
      expect(w.find('[data-piece-from-design]').exists()).toBe(true)
      w.unmount()
    })

    it("shows a piece changed on both sides, and keeps the note's text on request", async () => {
      const w = mountWindow()
      const { frame, say } = await ready(w)
      await say({ type: 'bring_piece', n: '2', hash: 'old', frame: 41, design: 'Le parole del design.', conflict: true })
      expect(w.find('[data-piece-design]').text()).toContain('Le parole del design.')
      await w.find('[data-piece-keep-note]').trigger('click')
      await flushPromises()
      expect(frame.postMessage).toHaveBeenCalledWith({ bridge: 'layout/2', type: 'put_back', n: '2' }, layoutApp.origin, [])
      expect(w.find('[data-piece-editor]').exists()).toBe(false)
      w.unmount()
    })
  })

  describe('the versions of the note (IMP-228)', () => {
    const ready = async (w: ReturnType<typeof mountWindow>) => {
      await flushPromises()
      const frame = frameWindow(w)
      window.dispatchEvent(new MessageEvent('message', { data: { bridge: 'layout/2', type: 'ready' }, origin: layoutApp.origin, source: frame }))
      await flushPromises()
      return frame
    }

    it('says what readers see, working on the draft', async () => {
      vi.mocked(getNote).mockResolvedValue({ ...note('---\ntype: layout\n---\n'), valid: { tag: 't', label: 'V3', same: false } })
      vi.mocked(openLayout).mockResolvedValue({ bytes: null, etag: '"e"', missing: [] })
      const w = mountWindow()
      await ready(w)
      expect(w.find('[data-version-button]').text()).toContain('Pinned V3')
      expect(w.find('[data-readers-see]').text()).toBe('Readers see V3')
      w.unmount()
    })

    it('saves the design first, and pins what shows', async () => {
      vi.mocked(openLayout).mockResolvedValue({ bytes: null, etag: '"e"', missing: [] })
      vi.mocked(saveLayout).mockResolvedValue({ etag: '"e2"' })
      const w = mountWindow()
      const frame = await ready(w)
      const say = (data: Record<string, unknown>) =>
        window.dispatchEvent(new MessageEvent('message', { data: { bridge: 'layout/2', ...data }, origin: layoutApp.origin, source: frame }))
      say({ type: 'dirty', dirty: true })
      await flushPromises()
      await w.find('[data-version-button]').trigger('click')
      await flushPromises()
      expect(w.find('[data-version-pin]').text()).toBe('Save and pin')
      await w.find('[data-version-form]').trigger('submit')
      await flushPromises()
      expect(frame.postMessage).toHaveBeenCalledWith({ bridge: 'layout/2', type: 'save' }, layoutApp.origin, [])
      expect(tagVersion).not.toHaveBeenCalled()
      say({ type: 'save', bytes: new ArrayBuffer(4) })
      await flushPromises()
      expect(saveLayout).toHaveBeenCalled()
      expect(tagVersion).toHaveBeenCalledWith('rivista/n1.md', 'V1', '')
      expect(setValidVersion).toHaveBeenCalledWith('rivista/n1.md', 'note/rivista/n1.md/V1')
      w.unmount()
    })

    it('goes from a version to the design to work on', async () => {
      vi.mocked(openLayout).mockResolvedValue({ bytes: null, etag: '"e"', missing: [] })
      const w = mount(PluginAppWindow, {
        props: { path: 'rivista/n1.md', version: 'note/rivista/n1.md/V3' },
        global: { plugins: [i18n()] },
        attachTo: document.body,
      })
      await ready(w as ReturnType<typeof mountWindow>)
      await w.find('[data-app-edit-latest]').trigger('click')
      expect(w.emitted('close')).toHaveLength(1)
      expect(windows.open).toHaveBeenCalledWith(expect.objectContaining({ type: 'app', props: { path: 'rivista/n1.md' } }))
      w.unmount()
    })
  })
})
