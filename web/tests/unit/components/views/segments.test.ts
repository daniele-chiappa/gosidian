import { describe, expect, it } from 'vitest'
import { splitViews } from '@/components/views/segments'

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
