import { describe, expect, it, vi, beforeEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { AxiosError, AxiosHeaders } from 'axios'
import { defineComponent, h } from 'vue'
import enUI from '@catalogs/ui.en.json'
// Loaded before the tests: the mocks below import it again, then at once.
import 'plancia-diagrams'

// The editor is plancia-diagrams' (its own tests edit with it): here a
// stand-in that changes the source, to test what the window does with it.
vi.mock('@/components/domain/diagramView', async () => {
  const lib = await import('plancia-diagrams')
  return {
    findDiagramBlocks: lib.findDiagramBlocks,
    replaceDiagramBlock: lib.replaceDiagramBlock,
    writeDiagramBlock: lib.writeDiagramBlock,
    findTwin: lib.findTwin,
    twinText: lib.twinText,
    parseDiagram: lib.parseDiagram,
    toMermaid: lib.toMermaid,
    DiagramView: defineComponent({
      props: { source: String },
      setup: (p) => () => h('div', { class: 'view' }, p.source),
    }),
    DiagramEditor: defineComponent({
      props: { source: String, labels: Object, linkSuggest: Function, height: Number },
      emits: ['update:source'],
      setup:
        (p, { emit }) =>
        () =>
          h(
            'button',
            { class: 'stand-in', onClick: () => emit('update:source', `${p.source}# edited\n`) },
            p.source,
          ),
    }),
  }
})

const NOTE =
  '---\ntitle: N\n---\n\n```diagram\nkind: tree\nroot: First\n```\n\ntext\n\n```diagram\nkind: tree\nroot: Second\n```\n'
let remote = { content: NOTE, etag: 'e1' }
const updates: { content: string; ifMatch?: string }[] = []
let refuse = false
vi.mock('@/api/notes', () => ({
  getNote: async (path: string) => ({ path, title: 'N', size: 0, mod_time: '', ...remote }),
  updateNote: async (path: string, body: { content: string; ifMatch?: string }) => {
    if (refuse) {
      refuse = false
      throw new AxiosError('412', '412', undefined, undefined, {
        status: 412,
        statusText: 'Precondition Failed',
        data: { error: { code: 'concurrency.etag_mismatch' } },
        headers: {},
        config: { headers: new AxiosHeaders() },
      })
    }
    updates.push(body)
    remote = { content: body.content, etag: `e${updates.length + 1}` }
    return { path, title: 'N', size: 0, mod_time: '', ...remote }
  },
}))
vi.mock('@/api/search', () => ({ search: async () => [] }))
vi.mock('@/stores/access', () => ({ useAccessStore: () => ({ canWrite: () => true }) }))
vi.mock('@/composables/useSSE', () => ({ useSSE: () => ({ on: () => () => {} }) }))
vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => ({ open: vi.fn() }),
}))

import DiagramWindow from '@/views/DiagramWindow.vue'

const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
const mountAt = async (index: number, notes?: string[]) => {
  const w = mount(DiagramWindow, {
    props: { path: 'p/n.md', index, notes },
    global: { plugins: [i18n()], provide: { openWindow: vi.fn() } },
  })
  // Until the note is loaded and the editor (or the note of a missing block) shows.
  await vi.waitFor(() => expect(w.find('.stand-in, [data-diagram-missing]').exists()).toBe(true), {
    timeout: 10000,
  })
  return w
}

/** Lets the lazy modules and the awaited calls settle: a few rounds, without counting on time. */
async function settle() {
  for (let i = 0; i < 5; i++) {
    await flushPromises()
    await new Promise((r) => setTimeout(r, 0))
  }
}

describe('DiagramWindow (IMP-169, F2)', () => {
  beforeEach(() => {
    remote = { content: NOTE, etag: 'e1' }
    updates.length = 0
    refuse = false
  })

  it('edits the index-th block and saves it in place, with the etag', async () => {
    const w = await mountAt(1)
    expect(w.find('.stand-in').text()).toContain('root: Second')
    await w.find('.stand-in').trigger('click')
    expect(w.emitted('dirty')?.at(-1)).toEqual([true])
    await w.find('[data-diagram-save]').trigger('click')
    await settle()
    expect(updates).toEqual([
      { content: NOTE.replace('root: Second\n', 'root: Second\n# edited\n'), ifMatch: 'e1' },
    ])
    expect(w.emitted('dirty')?.at(-1)).toEqual([false])
  })

  it('puts the diagram in the new version of a note that changed meanwhile', async () => {
    const w = await mountAt(0)
    await w.find('.stand-in').trigger('click')
    // Someone else changed the text around it.
    remote = { content: NOTE.replace('text', 'other text'), etag: 'e9' }
    refuse = true
    await w.find('[data-diagram-save]').trigger('click')
    await settle()
    expect(w.find('[data-diagram-conflict]').exists()).toBe(true)
    await w.find('[data-diagram-overwrite]').trigger('click')
    await settle()
    expect(updates.at(-1)).toEqual({
      content: NOTE.replace('text', 'other text').replace(
        'root: First\n',
        'root: First\n# edited\n',
      ),
      ifMatch: 'e9',
    })
    expect(w.find('[data-diagram-conflict]').exists()).toBe(false)
  })

  it('says when the block is no longer there', async () => {
    const w = await mountAt(5)
    expect(w.find('[data-diagram-missing]').exists()).toBe(true)
  })

  it('writes a Mermaid twin under the block when asked, again at each save, and takes it away', async () => {
    const w = await mountAt(1)
    await w.find('[data-diagram-twin]').setValue(true)
    expect(w.emitted('dirty')?.at(-1)).toEqual([true])
    await w.find('[data-diagram-save]').trigger('click')
    await settle()
    const one = updates.at(-1)!.content
    expect(one).toContain(
      '```diagram\nkind: tree\nroot: Second\n```\n\n```mermaid\n%% diagram-twin ',
    )
    expect(one).toContain('flowchart TB\n  n0(["Second"])\n```\n')
    expect(w.emitted('dirty')?.at(-1)).toEqual([false])
    // A change to the diagram writes the twin again: one twin, the new text.
    await w.find('.stand-in').trigger('click')
    await w.find('[data-diagram-save]').trigger('click')
    await settle()
    expect(updates.at(-1)!.content.match(/```mermaid/g)).toHaveLength(1)
    // Turned off, it goes; the first block never had one.
    await w.find('[data-diagram-twin]').setValue(false)
    await w.find('[data-diagram-save]').trigger('click')
    await settle()
    expect(updates.at(-1)!.content).not.toContain('```mermaid')
    expect(updates.at(-1)!.content).toContain('root: Second\n# edited\n```\n')
  })

  it('reads the twin a note has, and says what a conversion left out', async () => {
    remote = {
      content: NOTE.replace(
        'root: Second\n```\n',
        'root: Second\n```\n\n```mermaid\n%% diagram-twin 00000000 — x\nflowchart TB\n```\n',
      ),
      etag: 'e1',
    }
    const w = await mountAt(1, ['styles are not kept'])
    expect((w.find('[data-diagram-twin]').element as HTMLInputElement).checked).toBe(true)
    expect(w.find('[data-diagram-notes]').text()).toContain('styles are not kept')
  })
})
