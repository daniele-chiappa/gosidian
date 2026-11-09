import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import type { GraphEdge, GraphNode } from '@/api/graph'
import GraphList from '@/components/graph/GraphList.vue'

const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
const node = (id: string, label: string, degree: number, extra: Partial<GraphNode> = {}): GraphNode => ({
  id,
  label,
  degree,
  project: 'p',
  ...extra,
})

describe('GraphList (IMP-224, F-32)', () => {
  const nodes = [node('p/a.md', 'Alpha', 1), node('p/b.md', 'Beta', 2), node('p/c.md', 'Gamma', 1, { project: undefined, group: 'g1' })]
  const edges: GraphEdge[] = [
    { source: 'p/a.md', target: 'p/b.md', count: 1 },
    { source: 'p/b.md', target: 'p/c.md', count: 2 },
  ]

  it('says the graph as a table: the most linked first, each with what it links to and from', () => {
    const w = mount(GraphList, { props: { nodes, edges }, global: { plugins: [i18n()] } })
    expect(w.find('caption').text()).toBe('3 nodes and 2 links, the most linked first')
    expect(w.findAll('thead th').map((th) => th.text())).toEqual(['Node', 'Project or group', 'Links', 'Links to', 'Linked from'])
    const rows = w.findAll('tbody tr').map((tr) => tr.findAll('th, td').map((c) => c.text()))
    expect(rows).toEqual([
      ['Beta', 'p', '2', 'Gamma', 'Alpha'],
      ['Alpha', 'p', '1', 'Beta', ''],
      ['Gamma', 'g1', '1', '', 'Beta'],
    ])
    expect(w.findAll('tbody th').every((th) => th.attributes('scope') === 'row')).toBe(true)
  })

  it('opens a node from its name, as a click on the drawing does', async () => {
    const w = mount(GraphList, { props: { nodes, edges }, global: { plugins: [i18n()] } })
    await w.findAll('tbody th button')[1]!.trigger('click')
    expect(w.emitted('select')).toEqual([['p/a.md']])
  })
})
