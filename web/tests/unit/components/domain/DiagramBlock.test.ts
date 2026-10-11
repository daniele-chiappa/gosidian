import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { defineComponent, h } from 'vue'
import enUI from '@catalogs/ui.en.json'
// Loaded before the tests: the mocks below import it again, then at once.
import 'plancia-diagrams'

// The drawing itself is plancia-diagrams' (its own tests draw it): here a
// stand-in that shows the source and sends a link, to test what gosidian
// does around it.
const exportSVG = vi.fn(() => '<svg xmlns="http://www.w3.org/2000/svg"/>')
vi.mock('@/components/domain/diagramView', async () => ({
  findDiagramBlocks: (await import('plancia-diagrams')).findDiagramBlocks,
  parseDiagram: (await import('plancia-diagrams')).parseDiagram,
  toMermaid: (await import('plancia-diagrams')).toMermaid,
  toCanvas: (await import('plancia-diagrams')).toCanvas,
  fromMermaid: (await import('plancia-diagrams')).fromMermaid,
  DiagramView: defineComponent({
    props: { source: String, labels: Object },
    emits: ['link'],
    setup(props, { emit, expose, slots }) {
      expose({ exportSVG })
      return () =>
        h('div', { class: 'stand-in', 'data-labels': JSON.stringify(props.labels) }, [
          slots.actions?.({ drawable: !props.source?.startsWith('kind: venn') }),
          h('button', {
            id: 'note',
            onClick: () => emit('link', { target: 'p/plans/a#Steps', kind: 'note', label: 'A' }),
          }),
          h('button', {
            id: 'lost',
            onClick: () => emit('link', { target: 'nowhere', kind: 'note', label: 'N' }),
          }),
          h('button', {
            id: 'web',
            onClick: () => emit('link', { target: 'https://example.com', kind: 'url', label: 'W' }),
          }),
          props.source,
        ])
    },
  }),
}))
const saved: { blob: Blob; name: string }[] = []
vi.mock('@/api/download', () => ({
  saveBlob: (blob: Blob, name: string) => saved.push({ blob, name }),
}))
const notes: Record<string, string> = {}
const writes: { path: string; content: string; ifMatch?: string }[] = []
vi.mock('@/api/notes', () => ({
  getNote: async (path: string) => ({
    path,
    title: path,
    content: notes[path] ?? '',
    etag: 'e1',
    size: 0,
    mod_time: '',
  }),
  updateNote: async (path: string, body: { content: string; ifMatch?: string }) => {
    writes.push({ path, ...body })
    notes[path] = body.content
    return { path, title: path, content: body.content, etag: 'e2', size: 0, mod_time: '' }
  },
}))
vi.mock('@/stores/access', () => ({
  useAccessStore: () => ({ canWrite: (p: string) => !p.startsWith('ro/') }),
}))
const store = { open: vi.fn(), identify: vi.fn() }
vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => store,
}))

import MarkdownPreview from '@/components/domain/MarkdownPreview.vue'

const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
const html =
  '<p>Text</p><pre><code class="language-diagram">kind: flow\ntitle: "Arrival: docs"\nnodes: [a]\n</code></pre>'

describe('DiagramBlock (IMP-169)', () => {
  it('draws a diagram block of a note, with the words of the app', async () => {
    const w = mount(MarkdownPreview, {
      props: { html },
      global: { plugins: [i18n()], provide: { openWindow: vi.fn() } },
    })
    await vi.waitFor(() => expect(w.find('.stand-in').exists()).toBe(true), { timeout: 10000 })
    expect(w.find('.stand-in').text()).toContain('kind: flow')
    expect(JSON.parse(w.find('.stand-in').attributes('data-labels')!)).toMatchObject({
      open: 'Open',
      zoomIn: 'Zoom in',
      problems: 'This diagram has problems:',
    })
    expect(w.find('pre').exists()).toBe(false)
  })

  it('opens a link of the diagram as the note it resolves to, an address in a tab', async () => {
    const openWindow = vi.fn(() => 'w1')
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    const w = mount(MarkdownPreview, {
      props: { html, diagramLinks: { 'p/plans/a#Steps': 'p/plans/a.md' } },
      global: { plugins: [i18n()], provide: { openWindow } },
    })
    await vi.waitFor(() => expect(w.find('.stand-in').exists()).toBe(true), { timeout: 10000 })
    await w.find('#note').trigger('click')
    expect(openWindow).toHaveBeenCalledWith(
      expect.objectContaining({
        type: 'note',
        props: expect.objectContaining({ path: 'p/plans/a.md', anchor: 'Steps' }),
      }),
    )
    await w.find('#lost').trigger('click')
    expect(openWindow).toHaveBeenCalledTimes(1)
    await w.find('#web').trigger('click')
    expect(open).toHaveBeenCalledWith('https://example.com', '_blank', 'noopener,noreferrer')
    open.mockRestore()
  })

  it('shows the code, and exports the drawing under its title', async () => {
    const w = mount(MarkdownPreview, {
      props: { html },
      global: { plugins: [i18n()], provide: { openWindow: vi.fn() } },
    })
    await vi.waitFor(() => expect(w.find('.stand-in').exists()).toBe(true), { timeout: 10000 })
    await w.find('[data-diagram-export]').trigger('click')
    expect(w.findAll('[data-diagram-export-as]').map((b) => b.attributes('data-diagram-export-as'))).toEqual([
      'svg',
      'mermaid',
      'canvas',
    ])
    await w.find('[data-diagram-export-as="svg"]').trigger('click')
    await vi.waitFor(() => expect(saved.at(-1)?.name).toBe('arrival-docs.svg'))
    expect(saved.at(-1)?.blob.type).toBe('image/svg+xml')
    await w.find('[data-diagram-export]').trigger('click')
    await w.find('[data-diagram-export-as="mermaid"]').trigger('click')
    await vi.waitFor(() => expect(saved.at(-1)?.name).toBe('arrival-docs.mmd'))
    expect(await saved.at(-1)!.blob.text()).toBe('flowchart TB\n  accTitle: Arrival#58; docs\n  a["a"]\n')
    await w.find('[data-diagram-export]').trigger('click')
    await w.find('[data-diagram-export-as="canvas"]').trigger('click')
    await vi.waitFor(() => expect(saved.at(-1)?.name).toBe('arrival-docs.canvas'))
    expect(JSON.parse(await saved.at(-1)!.blob.text()).nodes[0]).toMatchObject({ id: 'a', type: 'text', text: 'a' })
    await w.find('[data-diagram-code]').trigger('click')
    expect(w.find('pre code').text()).toContain('title: "Arrival: docs"')
    expect(w.find('.stand-in').exists()).toBe(false)
    expect(w.find('[data-diagram-export]').exists()).toBe(false)
  })
})

describe('DiagramBlock of a kind not drawn yet (IMP-169)', () => {
  it('has no export', async () => {
    const w = mount(MarkdownPreview, {
      props: {
        // happy-dom's parser drops a <pre> that starts the HTML; browsers keep it.
        html: '<p>Sets</p><pre><code class="language-diagram">kind: venn\nsets: [A, B]\n</code></pre>',
      },
      global: { plugins: [i18n()], provide: { openWindow: vi.fn() } },
    })
    await vi.waitFor(() => expect(w.find('.stand-in').exists()).toBe(true), { timeout: 10000 })
    expect(w.find('[data-diagram-code]').exists()).toBe(true)
    expect(w.find('[data-diagram-export]').exists()).toBe(false)
  })
})

describe('DiagramBlock Edit (IMP-169, F2)', () => {
  const yaml = 'kind: tree\nroot: R\n'
  const block = `<pre><code class="language-diagram">${yaml}</code></pre>`

  it('shows Edit only for who may write the note, and opens the window on the block’s index', async () => {
    // The note has a nested sample, then two blocks of the same text: the sample is no block, so the second drawn one is index 1.
    notes['p/n.md'] =
      '# N\n\n````md\n```diagram\nkind: venn\n```\n````\n\n```diagram\n' +
      yaml +
      '```\n\n```diagram\n' +
      yaml +
      '```\n'
    const openWindow = vi.fn(() => 'w1')
    const w = mount(MarkdownPreview, {
      props: { html: `<p>x</p>${block}${block}`, notePath: 'p/n.md', diagramsEditable: true },
      global: { plugins: [i18n()], provide: { openWindow } },
    })
    // Both drawings, each loaded on its own.
    await vi.waitFor(() => expect(w.findAll('[data-diagram-edit]')).toHaveLength(2), { timeout: 10000 })
    const edits = w.findAll('[data-diagram-edit]')
    await edits[1]!.trigger('click')
    await vi.waitFor(() => expect(openWindow).toHaveBeenCalled(), { timeout: 10000 })
    expect(openWindow).toHaveBeenCalledWith(
      expect.objectContaining({
        type: 'diagram',
        key: 'diagram:p/n.md#1',
        props: { path: 'p/n.md', index: 1 },
      }),
    )
    const ro = mount(MarkdownPreview, {
      props: { html: `<p>x</p>${block}`, notePath: 'ro/n.md', diagramsEditable: true },
      global: { plugins: [i18n()], provide: { openWindow } },
    })
    await vi.waitFor(() => expect(ro.find('.stand-in').exists()).toBe(true), { timeout: 10000 })
    expect(ro.find('[data-diagram-edit]').exists()).toBe(false)
  })
})

describe('A Mermaid block of a note (IMP-169, F3)', () => {
  it('converts to a diagram in place, for who may write the note, and opens it', async () => {
    notes['p/m.md'] = '# M\n\n```diagram\nkind: tree\nroot: R\n```\n\n```mermaid\nflowchart LR\n  a --> b\n```\n\nAfter.\n'
    const openWindow = vi.fn(() => 'w1')
    const html =
      '<p>x</p><pre><code class="language-mermaid">flowchart LR\n  a --&gt; b\n</code></pre>'
    const w = mount(MarkdownPreview, {
      props: { html, notePath: 'p/m.md', diagramsEditable: true },
      global: { plugins: [i18n()], provide: { openWindow } },
    })
    await w.find('[data-mermaid-convert]').trigger('click')
    await vi.waitFor(() => expect(openWindow).toHaveBeenCalled(), { timeout: 10000 })
    expect(writes.at(-1)).toEqual({
      path: 'p/m.md',
      content:
        '# M\n\n```diagram\nkind: tree\nroot: R\n```\n\n```diagram\nkind: flow\ndirection: LR\nnodes: [a, b]\nedges:\n  - a -> b\n```\n\nAfter.\n',
      ifMatch: 'e1',
    })
    expect(openWindow).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'diagram', props: { path: 'p/m.md', index: 1, notes: [] } }),
    )
    const ro = mount(MarkdownPreview, {
      props: { html, notePath: 'ro/m.md', diagramsEditable: true },
      global: { plugins: [i18n()], provide: { openWindow } },
    })
    expect(ro.find('[data-mermaid-convert]').exists()).toBe(false)
    expect(ro.find('pre code').text()).toContain('a --> b')
  })
})
