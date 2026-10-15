import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import type { GraphResponse } from '@/api/graph'

vi.mock('@/api/graph', () => ({ fetchGraph: vi.fn(), fetchGraphSources: vi.fn(async () => []) }))
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

import { fetchGraph, fetchGraphSources } from '@/api/graph'
import GraphView from '@/views/GraphView.vue'

const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })

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
      global: { plugins: [i18n()], stubs: { GraphCanvas: true, Graph3DCanvas: true, SearchSelect: true } },
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

  it("asks for no plugin's graph until its required arguments are given (BUG-128)", async () => {
    vi.mocked(fetchGraph).mockReset().mockResolvedValue(graph(3))
    vi.mocked(fetchGraphSources).mockResolvedValue([
      { source: 'links' },
      {
        source: 'vec-pg/docs',
        input_schema: { type: 'object', properties: { collection: { type: 'string' } }, required: ['collection'] },
      },
    ])
    const w = mount(GraphView, {
      props: { project: 'rag', global: true, source: 'vec-pg/docs' },
      global: { plugins: [i18n()], stubs: { GraphCanvas: true, Graph3DCanvas: true, SearchSelect: true } },
    })
    await flushPromises()
    expect(fetchGraph).not.toHaveBeenCalled()
    expect(w.find('[data-graph-args-needed]').text()).toContain('collection')
    expect(w.find('aside').text()).toContain('collection *')

    // The field saves on change, as when the reader leaves it.
    const input = w.find('aside input[type="text"]')
    expect(input.attributes('required')).toBeDefined()
    expect(input.attributes('aria-required')).toBe('true')
    ;(input.element as HTMLInputElement).value = 'docs'
    await input.trigger('change')
    await flushPromises()
    expect(fetchGraph).toHaveBeenCalledTimes(1)
    expect(vi.mocked(fetchGraph).mock.calls[0]![0]).toMatchObject({ source: 'vec-pg/docs', with: { collection: 'docs' } })
    expect(w.find('[data-graph-args-needed]').exists()).toBe(false)
  })

  it('leaves the limit empty, its placeholder the cap, and asks for no limit until one is given', async () => {
    vi.mocked(fetchGraph).mockReset().mockResolvedValue(graph(3))
    vi.mocked(fetchGraphSources).mockResolvedValue([])
    const w = mount(GraphView, {
      props: { project: 'A', global: true },
      global: { plugins: [i18n()], stubs: { GraphCanvas: true, Graph3DCanvas: true, SearchSelect: true } },
    })
    await flushPromises()
    const limit = w.find('input[placeholder="2000"]')
    expect((limit.element as HTMLInputElement).value).toBe('')
    expect(vi.mocked(fetchGraph).mock.calls.at(-1)![0]?.limit).toBeUndefined()
    await limit.setValue(300)
    await flushPromises()
    expect(vi.mocked(fetchGraph).mock.calls.at(-1)![0]).toMatchObject({ limit: 300 })
    await limit.setValue('')
    await flushPromises()
    expect(vi.mocked(fetchGraph).mock.calls.at(-1)![0]?.limit).toBeUndefined()
  })
})
