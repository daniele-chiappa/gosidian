import { describe, expect, it } from 'vitest'
import { hasDiagrams, hasPipeline, splitViews } from '@/components/views/segments'

describe('splitViews', () => {
  it('cuts the HTML at the top-level view placeholders', () => {
    const html =
      '<h1>Board</h1>\n<p>Open:</p>\n<div class="gosidian-view" data-view="0"><table><tr><td>a</td></tr></table></div>\n' +
      '<p>Text &amp; more</p><div class="gosidian-view" data-view="1"><p>x</p></div>'
    const s = splitViews(html)
    expect(s.map((x) => x.kind)).toEqual(['html', 'view', 'html', 'view'])
    expect(s[0]).toMatchObject({ kind: 'html' })
    expect((s[0] as { html: string }).html).toContain('<h1>Board</h1>')
    expect(s[1]).toMatchObject({ kind: 'view', index: 0 })
    expect((s[1] as { html: string }).html).toContain('<table>')
    expect((s[2] as { html: string }).html).toContain('Text &amp; more')
    expect(s[3]).toMatchObject({ kind: 'view', index: 1, html: '<p>x</p>' })
  })

  it('leaves a placeholder nested in other markup as HTML', () => {
    const s = splitViews(
      '<blockquote><div class="gosidian-view" data-view="0">x</div></blockquote>',
    )
    expect(s).toHaveLength(1)
    expect(s[0]?.kind).toBe('html')
  })
})

describe('splitViews and diagrams (IMP-169)', () => {
  it('cuts a top-level diagram block out, its YAML as written', () => {
    const html =
      '<p>Before</p>\n<pre><code class="language-diagram">kind: flow\nnodes: [a, &quot;b &lt;c&gt;&quot;]\n</code></pre>\n' +
      '<pre><code class="language-yaml">kind: flow\n</code></pre><pre><code class="language-diagram">kind: tree\nroot: R\n</code></pre>'
    expect(hasDiagrams(html)).toBe(true)
    const s = splitViews(html)
    expect(s.map((x) => x.kind)).toEqual(['html', 'diagram', 'html', 'diagram'])
    expect(s[1]).toMatchObject({
      kind: 'diagram',
      index: 0,
      source: 'kind: flow\nnodes: [a, "b <c>"]\n',
    })
    expect(s[3]).toMatchObject({ kind: 'diagram', index: 1, source: 'kind: tree\nroot: R\n' })
    expect((s[2] as { html: string }).html).toContain('language-yaml')
  })

  it('leaves a diagram in a list as its code', () => {
    const s = splitViews(
      '<ul><li><pre><code class="language-diagram">kind: tree\n</code></pre></li></ul>',
    )
    expect(s.map((x) => x.kind)).toEqual(['html'])
  })
})

describe('splitViews: where a diagram is written (IMP-169, F2)', () => {
  it('knows an embed’s note, and counts the blocks of the same text', () => {
    const block = (y: string) => `<pre><code class="language-diagram">${y}</code></pre>`
    const html =
      '<p>Own</p>' +
      block('kind: tree\nroot: A\n') +
      block('kind: tree\nroot: A\n') +
      '<div class="gosidian-embed-start" data-embed="p/other"><a class="wikilink" data-preview-path="p/other.md">other</a></div>' +
      block('kind: tree\nroot: A\n') +
      '<div class="gosidian-embed-end"></div>' +
      '<div class="gosidian-embed-start" data-embed="p/gone"><a class="wikilink unresolved">gone</a></div>' +
      block('kind: tree\nroot: B\n') +
      '<div class="gosidian-embed-end"></div>' +
      block('kind: tree\nroot: B\n')
    const d = splitViews(html).filter((s) => s.kind === 'diagram')
    expect(d.map((s) => s.kind === 'diagram' && [s.origin, s.occurrence])).toEqual([
      [undefined, 0],
      [undefined, 1],
      ['p/other.md', 0],
      [null, 0],
      [undefined, 0],
    ])
  })
})

describe('splitViews: Mermaid blocks and twins (IMP-169, F3)', () => {
  const diagram = '<pre><code class="language-diagram">kind: tree\nroot: A\n</code></pre>'
  const twin =
    '<pre><code class="language-mermaid">%% diagram-twin 1a2b3c4d — made from the diagram above\nflowchart TB\n</code></pre>'
  // The server escapes the > of an arrow, as here.
  const mermaid = '<pre><code class="language-mermaid">flowchart LR\n  a --&gt; b\n</code></pre>'

  it('leaves out a diagram’s twin, right after it, and keeps another Mermaid block as a segment', () => {
    const s = splitViews(`<p>x</p>${diagram}\n${twin}<p>y</p>${mermaid}${mermaid}`)
    expect(s.map((x) => x.kind)).toEqual(['html', 'diagram', 'html', 'mermaid', 'mermaid'])
    expect(s.map((x) => ('html' in x ? x.html : '')).join('')).not.toContain('diagram-twin')
    const m = s.filter((x) => x.kind === 'mermaid')
    expect(m.map((x) => x.kind === 'mermaid' && [x.index, x.source, x.occurrence])).toEqual([
      [0, 'flowchart LR\n  a --> b\n', 0],
      [1, 'flowchart LR\n  a --> b\n', 1],
    ])
  })

  it('shows a twin with no diagram right above it as the Mermaid it is', () => {
    const s = splitViews(`<p>x</p>${diagram}<p>between</p>${twin}`)
    expect(s.map((x) => x.kind)).toEqual(['html', 'diagram', 'html', 'mermaid'])
  })
})

describe('splitViews: a pipeline note’s block (IMP-169, F4)', () => {
  it('cuts a top-level pipeline block out, its YAML as written, and leaves the others', () => {
    const html =
      '<p>Copy.</p><pre><code class="language-pipeline">trigger: { on: manual }\nnodes:\n  - id: a\n    use: note.write\n    with: { text: &quot;{{ trigger.input }}&quot; }\n</code></pre>' +
      '<ul><li><pre><code class="language-pipeline">nodes: []\n</code></pre></li></ul>'
    expect(hasPipeline(html)).toBe(true)
    expect(hasPipeline('<pre><code class="language-yaml">x</code></pre>')).toBe(false)
    const s = splitViews(html)
    expect(s.map((x) => x.kind)).toEqual(['html', 'pipeline', 'html'])
    expect(s[1]).toMatchObject({
      kind: 'pipeline',
      index: 0,
      source:
        'trigger: { on: manual }\nnodes:\n  - id: a\n    use: note.write\n    with: { text: "{{ trigger.input }}" }\n',
    })
    expect(s[1]!.kind === 'pipeline' && s[1]!.origin).toBeUndefined()
    expect((s[2] as { html: string }).html).toContain('language-pipeline')
  })
})
