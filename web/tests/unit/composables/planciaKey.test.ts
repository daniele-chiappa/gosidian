import { describe, expect, it } from 'vitest'
import type { WindowInstance } from 'plancia'
import { codec, planciaKey, base } from '@/composables/planciaKey'

// codec.encode reads only `type`/`props` from a window; build a minimal stub.
const win = (type: string, props: Record<string, unknown> = {}): WindowInstance =>
  ({ type, props }) as WindowInstance

describe('plancia codec round-trip', () => {
  it('encodes a note path as note:<encoded> and decodes back', () => {
    const tok = codec.encode(win('note', { path: 'gosidian/hot.md' }))
    expect(tok).toBe('note:' + encodeURIComponent('gosidian/hot.md'))
    expect(codec.decode(tok!)).toEqual({
      type: 'note',
      key: 'note:gosidian/hot.md',
      title: 'hot',
      props: { path: 'gosidian/hot.md' },
    })
  })

  it('round-trips paths with special chars (colon, comma, space, unicode)', () => {
    for (const path of ['a/b,c.md', 'weird: note.md', 'spazî/notà èè.md', 'x:y:z.md']) {
      const tok = codec.encode(win('note', { path }))!
      // The delimiter is the first ':'; the encoded arg carries no raw ':'.
      expect(tok.indexOf(':')).toBe('note'.length)
      expect(codec.decode(tok)).toMatchObject({ type: 'note', props: { path } })
    }
  })

  it('singletons serialise to the bare type and decode with their title', () => {
    expect(codec.encode(win('settings'))).toBe('settings')
    expect(codec.encode(win('search'))).toBe('search')
    expect(codec.decode('settings')).toEqual({
      type: 'settings',
      key: 'settings',
      title: 'Settings',
      props: {},
    })
  })

  it('titles an admin section by the catalogue, an unknown one by its name (review)', () => {
    expect(codec.decode('admin:teams')?.title).toBe('Admin · Teams')
    expect(codec.decode('admin:foo')?.title).toBe('Admin · foo')
  })

  it('graph focus uses the focus prop; bare graph is the global graph', () => {
    expect(codec.encode(win('graph'))).toBe('graph')
    const ego = codec.encode(win('graph', { focus: 'p/n.md', depth: 1 }))!
    expect(ego).toBe('graph:' + encodeURIComponent('p/n.md'))
    expect(codec.decode(ego)).toEqual({
      type: 'graph',
      key: 'graph:p/n.md',
      title: '↳ n',
      props: { focus: 'p/n.md', depth: 1 },
    })
  })

  it('legacy edit:<path> deep-link normalises to a note window in edit mode', () => {
    const spec = codec.decode('edit:' + encodeURIComponent('a/b.md'))
    expect(spec).toEqual({
      type: 'note',
      key: 'note:a/b.md',
      title: 'b',
      props: { path: 'a/b.md', mode: 'edit' },
    })
    // URL normalises: a note window (however opened) always re-encodes to note:.
    expect(codec.encode(win('note', { path: 'a/b.md', mode: 'edit' }))).toBe(
      'note:' + encodeURIComponent('a/b.md'),
    )
  })

  it('decode rejects empty input', () => {
    expect(codec.decode('')).toBeNull()
    expect(codec.decode('   ')).toBeNull()
  })

  it('planciaKey matches the decoded de-dup convention', () => {
    expect(planciaKey('note', 'a/b.md')).toBe('note:a/b.md')
    expect(planciaKey('settings')).toBe('settings')
    // The codec keys windows the same way the app keys its opens.
    expect(codec.key({ type: 'note', props: { path: 'a/b.md' } })).toBe('note:a/b.md')
  })

  it('base strips the directory and .md suffix', () => {
    expect(base('gosidian/hot.md')).toBe('hot')
    expect(base('top.md')).toBe('top')
    expect(base('no-ext')).toBe('no-ext')
  })
})
