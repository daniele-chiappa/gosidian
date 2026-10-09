import { describe, expect, it } from 'vitest'
import { linkTarget, resolveRelative } from '@/components/domain/noteLinks'

describe('linkTarget (BUG-117, S7-3)', () => {
  const at = 'proj/docs/plan.md'

  it('opens a relative link to a note in a window', () => {
    expect(linkTarget('design.md', at)).toEqual({ kind: 'note', path: 'proj/docs/design.md' })
    expect(linkTarget('../y.md#Part%20two', at)).toEqual({ kind: 'note', path: 'proj/y.md', anchor: 'Part two' })
    expect(linkTarget('my%20note', at)).toEqual({ kind: 'note', path: 'proj/docs/my note.md' })
    expect(linkTarget('/notes/other/z.md', at)).toEqual({ kind: 'note', path: 'other/z.md' })
  })

  it('sends attachments, web pages and other addresses to a new tab', () => {
    expect(linkTarget('attachments/a.pdf', at)).toEqual({ kind: 'tab', url: '/vault-files/proj/docs/attachments/a.pdf' })
    expect(linkTarget('https://example.com', at)).toEqual({ kind: 'tab', url: 'https://example.com' })
    expect(linkTarget('/search?q=x', at)).toEqual({ kind: 'tab', url: '/search?q=x' })
  })

  it('follows nothing that leaves the vault or runs code', () => {
    expect(linkTarget('../../../etc.md', at)).toEqual({ kind: 'none' })
    expect(linkTarget('javascript:alert(1)', at)).toEqual({ kind: 'none' })
  })

  it('resolves against the vault root without a note', () => {
    expect(resolveRelative(undefined, 'a/b.md')).toBe('a/b.md')
    expect(resolveRelative('root.md', '../x')).toBeNull()
  })
})

describe('linkTarget, other schemes (review of BUG-117)', () => {
  it('leaves mailto:, tel: and an app scheme to the browser', () => {
    expect(linkTarget('mailto:a@example.com')).toEqual({ kind: 'browser', url: 'mailto:a@example.com' })
    expect(linkTarget('tel:+3902')).toEqual({ kind: 'browser', url: 'tel:+3902' })
    expect(linkTarget('obsidian://open?vault=v')).toEqual({ kind: 'browser', url: 'obsidian://open?vault=v' })
    expect(linkTarget('data:text/html,x')).toEqual({ kind: 'none' })
  })
  it('opens a protocol-relative link in a new tab', () => {
    expect(linkTarget('//example.com/x')).toEqual({ kind: 'tab', url: '//example.com/x' })
  })
})
