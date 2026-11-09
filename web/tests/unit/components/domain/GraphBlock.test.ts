import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import type { GraphResponse } from '@/api/graph'

vi.mock('@/api/graph', () => ({ fetchGraph: vi.fn() }))
vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => ({ windows: [], open: vi.fn() }),
}))

import { fetchGraph } from '@/api/graph'
import GraphBlock from '@/components/domain/GraphBlock.vue'

const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })

describe('GraphBlock, the graph as text (IMP-224, F-32)', () => {
  it('names the drawing with its counts, and shows it as a list', async () => {
    vi.mocked(fetchGraph).mockResolvedValue({
      nodes: [
        { id: 'p/a.md', label: 'Alpha', degree: 1, project: 'p' },
        { id: 'p/b.md', label: 'Beta', degree: 1, project: 'p' },
      ],
      edges: [{ source: 'p/a.md', target: 'p/b.md', count: 1 }],
      stats: { node_count: 2, edge_count: 1 },
    } as unknown as GraphResponse)
    const w = mount(GraphBlock, {
      props: { source: 'source: links\nproject: p\n', notePath: 'p/graphs/g.md' },
      global: { plugins: [i18n()], stubs: { GraphCanvas: true, Graph3DCanvas: true } },
    })
    await flushPromises()
    expect(w.find('graph-canvas-stub').attributes('label')).toBe('Graph: 2 nodes, 1 links. The List shows them as text.')
    const list = w.find('[data-graph-list-toggle]')
    expect(list.attributes('aria-pressed')).toBe('false')
    await list.trigger('click')
    expect(list.attributes('aria-pressed')).toBe('true')
    expect(w.find('graph-canvas-stub').exists()).toBe(false)
    expect(w.find('[data-graph-list]').text()).toContain('Beta')
  })
})
