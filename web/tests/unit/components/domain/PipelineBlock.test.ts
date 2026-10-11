import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { defineComponent, h } from 'vue'
import enUI from '@catalogs/ui.en.json'

// The drawing is plancia-diagrams' (its own tests draw it): here a stand-in
// that shows the source and what it was given, with the actions gosidian
// puts on it.
vi.mock('@/components/domain/diagramView', () => ({
  PipelineView: defineComponent({
    props: { source: String, catalog: Array, labels: Object },
    setup: (p, { slots }) => () =>
      h('div', { class: 'stand-in', 'data-catalog': JSON.stringify(p.catalog), 'data-labels': JSON.stringify(p.labels) }, [
        slots.actions?.(),
        p.source,
      ]),
  }),
}))
vi.mock('@/api/pipelines', async (orig) => ({
  ...(await orig<typeof import('@/api/pipelines')>()),
  getPipelineNodes: async () => ({ pipelines_enabled: false, nodes: [{ type: 'note.write', group: 'base' }] }),
}))
vi.mock('@/stores/access', () => ({
  useAccessStore: () => ({ canWrite: (p: string) => !p.startsWith('ro/') }),
}))
vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => ({ open: vi.fn(), identify: vi.fn() }),
}))

import MarkdownPreview from '@/components/domain/MarkdownPreview.vue'

const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
// happy-dom's parser drops a <pre> that starts the HTML; browsers keep it.
const html =
  '<p>It copies.</p><pre><code class="language-pipeline">trigger: { on: manual }\nnodes:\n  - id: a\n    use: note.write\n</code></pre>'

describe('PipelineBlock (IMP-169, F4)', () => {
  it('draws a pipeline note’s block with the node types, and its code on demand', async () => {
    const w = mount(MarkdownPreview, {
      props: { html, notePath: 'p/pipelines/copy.md', diagramsEditable: true },
      global: { plugins: [i18n()], provide: { openWindow: vi.fn() } },
    })
    await vi.waitFor(() => expect(w.find('.stand-in').exists()).toBe(true), { timeout: 10000 })
    expect(w.find('.stand-in').text()).toContain('use: note.write')
    await vi.waitFor(() =>
      expect(JSON.parse(w.find('.stand-in').attributes('data-catalog')!)).toEqual([
        { type: 'note.write', group: 'base' },
      ]),
    )
    expect(JSON.parse(w.find('.stand-in').attributes('data-labels')!)).toMatchObject({
      trigger: expect.any(String),
    })
    expect(w.find('pre').exists()).toBe(false)
    await w.find('[data-pipeline-code]').trigger('click')
    expect(w.find('pre code').text()).toContain('use: note.write')
    expect(w.find('.stand-in').exists()).toBe(false)
    await w.find('[data-pipeline-code]').trigger('click')
    expect(w.find('.stand-in').exists()).toBe(true)
  })

  it('opens the pipeline window from Edit, for who may write the note only', async () => {
    const openWindow = vi.fn(() => 'w1')
    const w = mount(MarkdownPreview, {
      props: { html, notePath: 'p/pipelines/copy.md', diagramsEditable: true },
      global: { plugins: [i18n()], provide: { openWindow } },
    })
    await vi.waitFor(() => expect(w.find('[data-pipeline-edit]').exists()).toBe(true), { timeout: 10000 })
    await w.find('[data-pipeline-edit]').trigger('click')
    expect(openWindow).toHaveBeenCalledWith({
      type: 'pipeline',
      key: 'pipeline:p/pipelines/copy.md',
      title: 'Pipeline · copy',
      props: { path: 'p/pipelines/copy.md' },
    })
    const ro = mount(MarkdownPreview, {
      props: { html, notePath: 'ro/pipelines/copy.md', diagramsEditable: true },
      global: { plugins: [i18n()], provide: { openWindow } },
    })
    await vi.waitFor(() => expect(ro.find('.stand-in').exists()).toBe(true), { timeout: 10000 })
    expect(ro.find('[data-pipeline-edit]').exists()).toBe(false)
  })
})
