import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import type { GraphResponse } from '@/api/graph'

vi.mock('@/api/graph', () => ({ fetchGraph: vi.fn(), fetchGraphSources: vi.fn(async () => []) }))
vi.mock('@/api/projects', () => ({ listProjects: vi.fn(async () => []) }))
vi.mock('@/api/tags', () => ({ listTags: vi.fn(async () => []) }))
vi.mock('@/api/noteTitles', () => ({ suggestNoteTitles: vi.fn(async () => []) }))
vi.mock('@/stores/ui', () => ({
  useUIStore: () => ({ graphMode: '2d', setGraphMode: vi.fn(), graphDefaultNodes: 2000, graphMaxNodes: 5000 }),
}))
const { store } = vi.hoisted(() => {
  const store = {
    windows: [] as { id: string; type: string; key: string; title: string; props: Record<string, unknown> }[],
    open: vi.fn(),
    // As plancia's, identify merges the props it is given into the window's.
    identify: vi.fn((id: string, _key: string, props: Record<string, unknown>) => {
      const win = store.windows.find((w) => w.id === id)
      if (win) Object.assign(win.props, props)
    }),
    setTitle: vi.fn(),
  }
  return { store }
})
vi.mock('plancia', async (orig) => ({ ...(await orig<typeof import('plancia')>()), useWindowsStore: () => store }))
vi.mock('@vueuse/core', async (orig) => ({
  ...(await orig<typeof import('@vueuse/core')>()),
  useDebounceFn: (fn: () => void) => fn,
}))

import type { WindowInstance } from 'plancia'
import { fetchGraph, fetchGraphSources } from '@/api/graph'
import { listProjects } from '@/api/projects'
import { codec } from '@/composables/planciaKey'
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

describe('GraphView, its heading (IMP-217, F-03)', () => {
  it('names itself in its heading: the links of a note, or the graph of a project (IMP-217, F-03)', async () => {
    vi.mocked(fetchGraph).mockResolvedValue(graph(1))
    const stubs = { GraphCanvas: true, Graph3DCanvas: true, SearchSelect: true }
    const ego = mount(GraphView, { props: { focus: 'p/x.md', depth: 1 }, global: { plugins: [i18n()], stubs } })
    expect(ego.find('h1[data-window-heading]').text()).toBe('Links · x')
    const project = mount(GraphView, { props: { project: 'A', global: true }, global: { plugins: [i18n()], stubs } })
    expect(project.find('h1[data-window-heading]').text()).toBe('Graph · A')
    await flushPromises()
  })
})

describe('GraphView, its filters in the address (BUG-158)', () => {
  afterEach(() => {
    store.windows = []
  })

  it('takes a filter out of the address when it is cleared', async () => {
    vi.mocked(fetchGraph).mockReset().mockResolvedValue(graph(3))
    vi.mocked(fetchGraphSources).mockResolvedValue([{ source: 'links' }])
    const win = { id: 'g1', type: 'graph', key: 'graph:project:claude', title: '', props: { project: 'claude', global: true } }
    store.windows = [win]
    const address = () => decodeURIComponent(codec.encode(win as unknown as WindowInstance) ?? '')
    const w = mount(GraphView, {
      props: { project: 'claude', global: true },
      global: { plugins: [i18n()], stubs: { GraphCanvas: true, Graph3DCanvas: true, SearchSelect: true } },
    })
    await flushPromises()
    const [, minDegree, limit] = w.findAll('input[type="number"]')
    await minDegree!.setValue(2)
    await limit!.setValue(50)
    await flushPromises()
    expect(address()).toBe('graph:p=claude&m=2&l=50')

    await minDegree!.setValue(0)
    await limit!.setValue('')
    await flushPromises()
    expect(address()).toBe('graph:p=claude')

    // Every project, chosen by hand: a reload must not put the most recent
    // one back.
    await w.findAll('button').find((b) => b.text() === 'Reset')!.trigger('click')
    await flushPromises()
    expect(address()).toBe('graph:p=*')
  })

  it('opens on every project when the address says so, not on the most recent one', async () => {
    vi.mocked(fetchGraph).mockReset().mockResolvedValue(graph(3))
    vi.mocked(listProjects).mockResolvedValueOnce([{ name: 'recent', note_count: 1, mod_time: '2026-11-04T10:00:00Z' }] as never)
    const win = { id: 'g1', type: 'graph', key: 'graph', title: '', props: { allProjects: true, global: true } }
    store.windows = [win]
    mount(GraphView, {
      props: { allProjects: true, global: true },
      global: { plugins: [i18n()], stubs: { GraphCanvas: true, Graph3DCanvas: true, SearchSelect: true } },
    })
    await flushPromises()
    expect(fetchGraph).toHaveBeenCalledTimes(1)
    expect(vi.mocked(fetchGraph).mock.calls[0]![0]).toMatchObject({ project: undefined })
    expect(decodeURIComponent(codec.encode(win as unknown as WindowInstance) ?? '')).toBe('graph:p=*')
  })
})

describe('GraphView, the graph as text (IMP-224, F-32)', () => {
  it('names the drawing with its counts, and shows it as a list beside 2D and 3D', async () => {
    vi.mocked(fetchGraph).mockReset().mockResolvedValue({
      nodes: [
        { id: 'p/a.md', label: 'Alpha', degree: 1, project: 'p' },
        { id: 'p/b.md', label: 'Beta', degree: 1, project: 'p' },
      ],
      edges: [{ source: 'p/a.md', target: 'p/b.md', count: 1 }],
      stats: { node_count: 2, edge_count: 1 },
    } as unknown as GraphResponse)
    const w = mount(GraphView, {
      props: { project: 'p', global: true },
      global: { plugins: [i18n()], stubs: { GraphCanvas: true, Graph3DCanvas: true, SearchSelect: true } },
    })
    await flushPromises()
    expect(w.find('graph-canvas-stub').attributes('label')).toBe('Graph: 2 nodes, 1 links. The List shows them as text.')
    const views = () => w.findAll('[data-graph-views] button')
    expect(views().map((b) => [b.text(), b.attributes('aria-pressed')])).toEqual([
      ['2D', 'true'],
      ['3D', 'false'],
      ['List', 'false'],
    ])

    await views()[2]!.trigger('click')
    expect(w.find('graph-canvas-stub').exists()).toBe(false)
    expect(w.find('[data-graph-list]').text()).toContain('Alpha')
    expect(views().map((b) => b.attributes('aria-pressed'))).toEqual(['false', 'false', 'true'])

    await views()[0]!.trigger('click')
    expect(w.find('graph-canvas-stub').exists()).toBe(true)
  })
})

