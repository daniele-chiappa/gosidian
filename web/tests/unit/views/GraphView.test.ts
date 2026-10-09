import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import type { GraphResponse } from '@/api/graph'

vi.mock('@/api/graph', () => ({ fetchGraph: vi.fn() }))
vi.mock('@/api/projects', () => ({ listProjects: vi.fn(async () => []) }))
vi.mock('@/api/tags', () => ({ listTags: vi.fn(async () => []) }))
vi.mock('@/api/noteTitles', () => ({ suggestNoteTitles: vi.fn(async () => []) }))
vi.mock('@/stores/ui', () => ({ useUIStore: () => ({ graphMode: '2d', setGraphMode: vi.fn() }) }))
vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => ({ windows: [], open: vi.fn(), identify: vi.fn(), setTitle: vi.fn() }),
}))
vi.mock('@vueuse/core', async (orig) => ({
  ...(await orig<typeof import('@vueuse/core')>()),
  useDebounceFn: (fn: () => void) => fn,
}))

import { fetchGraph } from '@/api/graph'
import GraphView from '@/views/GraphView.vue'

const graph = (count: number): GraphResponse =>
  ({ nodes: [], edges: [], stats: { node_count: count, edge_count: 0 } }) as unknown as GraphResponse

describe('GraphView (BUG-116, S6-10)', () => {
  it('draws the answer to the last filters when an older one lands after it', async () => {
    let releaseOld: (v: GraphResponse) => void = () => {}
    vi.mocked(fetchGraph)
      .mockReturnValueOnce(new Promise((r) => (releaseOld = r)))
      .mockResolvedValueOnce(graph(42))
    const w = mount(GraphView, {
      props: { project: 'A', global: true },
      global: { stubs: { GraphCanvas: true, Graph3DCanvas: true, SearchSelect: true } },
    })
    await flushPromises()
    const minDegree = w.findAll('input[type="number"]')[1]!
    await minDegree.setValue(2)
    await flushPromises()
    releaseOld(graph(7))
    await flushPromises()
    expect(fetchGraph).toHaveBeenCalledTimes(2)
    expect(w.text()).toContain('Nodes: 42')
  })
})
