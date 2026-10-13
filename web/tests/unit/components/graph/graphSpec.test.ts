import { describe, expect, it } from 'vitest'
import { graphBlock, graphNote, graphQuery, parseGraphBlock } from '@/components/graph/graphSpec'
import { splitViews } from '@/components/views/segments'

describe('graph blocks (IMP-183)', () => {
  it('reads a plugin source with its arguments', () => {
    const { spec, problem } = parseGraphBlock('source: vec-pg/chunks\nwith: {collection: procedure, k: 8}\ncolor: doc\nmode: 3d\n')
    expect(problem).toBeUndefined()
    expect(spec).toMatchObject({ source: 'vec-pg/chunks', mode: '3d', color: 'doc' })
    // The note's project, unless the block names one; color among the arguments.
    expect(graphQuery(spec!, 'rag')).toEqual({ source: 'vec-pg/chunks', project: 'rag', with: { collection: 'procedure', k: 8, color: 'doc' } })
  })

  it('reads the links with their filters, by default', () => {
    const { spec } = parseGraphBlock('project: gosidian\ntag: adr\nfocus: gosidian/hot.md\n')
    expect(spec?.source).toBe('links')
    expect(graphQuery(spec!, 'x')).toMatchObject({ project: 'gosidian', tag: 'adr', focus: 'gosidian/hot.md', depth: 2 })
    expect(parseGraphBlock('').spec?.source).toBe('links')
  })

  it('says why a block does not read', () => {
    for (const [src, why] of [
      ['colour: doc', 'no field'],
      ['mode: 4d', '2d or 3d'],
      ['source: Vec/x', '<plugin>/<graph>'],
      ['with: 3', 'map'],
      ['- a', 'map of fields'],
    ] as const) {
      expect(parseGraphBlock(src).problem).toContain(why)
    }
  })

  it('writes a note that reads back', () => {
    const note = graphNote('Mappa', { source: 'vec-pg/docs', with: { collection: 'procedure' }, mode: '2d', depth: 0 })
    expect(note).toContain('type: graph')
    const block = note.split('```graph\n')[1]!.split('```')[0]!
    expect(parseGraphBlock(block).spec).toMatchObject({ source: 'vec-pg/docs', with: { collection: 'procedure' }, mode: '2d' })
    expect(graphBlock({ source: 'links', with: {} })).toBe('source: links\n')
  })

  it('cuts a rendered note at its graph blocks', () => {
    const html = '<p>a</p><pre><code class="language-graph">source: vec-pg/chunks\n</code></pre><p>b</p>'
    const segs = splitViews(html)
    expect(segs.map((s) => s.kind)).toEqual(['html', 'graph', 'html'])
    expect(segs[1]).toMatchObject({ kind: 'graph', source: 'source: vec-pg/chunks\n' })
  })
})
