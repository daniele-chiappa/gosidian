import { describe, expect, it } from 'vitest'
import type { WindowInstance } from 'plancia'
import { codec, codecTypes, planciaKey, base, noteHref, retitle } from '@/composables/planciaKey'
import { windowRegistry } from '@/components/plancia/windowRegistry'

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

  it("keeps one admin window, its section and the audit's filter in the address (IMP-224)", () => {
    const tok = codec.encode(win('admin', { section: 'audit', audit: 'actor=claude&order=asc' }))!
    expect(tok).toBe('admin:' + encodeURIComponent('audit?actor=claude&order=asc'))
    expect(codec.decode(tok)).toEqual({
      type: 'admin',
      key: 'admin',
      title: 'Admin · Audit',
      props: { section: 'audit', audit: 'actor=claude&order=asc' },
    })
    expect(codec.decode('admin:teams')?.key).toBe('admin')
    expect(codec.key({ type: 'admin', props: { section: 'tokens' } })).toBe('admin')
    expect(codec.encode(win('admin', { section: 'users' }))).toBe('admin:users')
  })

  it('graph focus uses the focus prop; bare graph is the global graph', () => {
    expect(codec.encode(win('graph'))).toBe('graph')
    const ego = codec.encode(win('graph', { focus: 'p/n.md', depth: 1 }))!
    expect(ego).toBe('graph:' + encodeURIComponent('p/n.md'))
    expect(codec.decode(ego)).toEqual({
      type: 'graph',
      key: 'graph:p/n.md',
      title: 'Links · n',
      props: { focus: 'p/n.md', depth: 1 },
    })
  })

  it('graph of every project, chosen by hand, keeps it in the address (BUG-158)', () => {
    const tok = codec.encode(win('graph', { global: true, allProjects: true, minDegree: 2 }))!
    expect(decodeURIComponent(tok)).toBe('graph:p=*&m=2')
    expect(codec.decode(tok)).toEqual({
      type: 'graph',
      key: 'graph',
      title: 'Graph',
      props: { global: true, allProjects: true, minDegree: 2 },
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

  it("a project's members window keeps its project (BUG-154)", () => {
    const tok = codec.encode(win('project-members', { project: 'agile lab' }))!
    expect(tok).toBe('project-members:' + encodeURIComponent('agile lab'))
    expect(codec.decode(tok)).toEqual({
      type: 'project-members',
      key: 'project-members:agile lab',
      title: 'Access · agile lab',
      props: { project: 'agile lab' },
    })
    // Two projects, two windows.
    expect(codec.key({ type: 'project-members', props: { project: 'b' } })).toBe(
      'project-members:b',
    )
  })

  it("a note in a plugin's app keeps its path, one window a note (IMP-226)", () => {
    const tok = codec.encode(win('app', { path: 'rivista/numero 1.md' }))!
    expect(tok).toBe('app:' + encodeURIComponent('rivista/numero 1.md'))
    expect(codec.decode(tok)).toEqual({
      type: 'app',
      key: 'app:rivista/numero 1.md',
      title: 'numero 1 · editor',
      props: { path: 'rivista/numero 1.md' },
    })
    expect(codec.key({ type: 'app', props: { path: 'r/b.md' } })).toBe(planciaKey('app', 'r/b.md'))
  })

  it("a creation window keeps its folder, keyed as the tree's + opens it (BUG-154)", () => {
    const props = { path: 'p/docs' }
    expect(codec.key({ type: 'create', props })).toBe(planciaKey('create', 'p/docs'))
    expect(codec.decode(codec.encode(win('create', props))!)).toEqual({
      type: 'create',
      key: 'create:p/docs',
      title: 'New note · docs',
      props,
    })
  })

  it("a creation window keeps its folder and name, keyed as a missing note's link opens it (BUG-154)", () => {
    for (const props of [
      { path: 'p/docs', name: 'idea: nuova' },
      { path: '', name: 'top' },
    ]) {
      const key = planciaKey('create', `${props.path}/${props.name}`)
      expect(codec.key({ type: 'create', props })).toBe(key)
      expect(codec.decode(codec.encode(win('create', props))!)).toEqual({
        type: 'create',
        key,
        title: `New note · ${props.path.split('/').pop() || '/'}`,
        props,
      })
    }
    // Same folder, no name: another window.
    expect(codec.key({ type: 'create', props: { path: 'p/docs' } })).not.toBe(
      codec.key({ type: 'create', props: { path: 'p/docs', name: 'x' } }),
    )
  })

  it('knows every window type the plancia can open (BUG-154)', () => {
    // A type the codec does not list comes back from the address as a
    // singleton without its props.
    expect(Object.keys(windowRegistry).filter((t) => !codecTypes.includes(t))).toEqual([])
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

  it('base strips the directory and the .md or .html suffix', () => {
    expect(base('gosidian/hot.md')).toBe('hot')
    expect(base('top.md')).toBe('top')
    expect(base('no-ext')).toBe('no-ext')
    expect(base('p/report.HTML')).toBe('report')
    expect(base('p/data.csv')).toBe('data.csv')
  })

  it('noteHref escapes each segment of the path', () => {
    expect(noteHref('p/a b/c#d.md')).toBe('/notes/p/a%20b/c%23d.md')
  })
})

describe('window titles in a new language (BUG-155)', () => {
  const titled = (type: string, props: Record<string, unknown>, title: string): WindowInstance =>
    ({ type, props, title }) as WindowInstance

  it('translates again a title the address gives', () => {
    expect(retitle(titled('settings', {}, 'Settings'), 'en', 'it')).toBe('Impostazioni')
    expect(retitle(titled('project-members', { project: 'p' }, 'Access · p'), 'en', 'it')).toBe(
      'Accesso · p',
    )
    expect(retitle(titled('graph', { global: true, project: 'p' }, 'Graph · p'), 'en', 'it')).toBe(
      'Grafo · p',
    )
  })

  it("keeps the note's name in a «kind · name» title and translates the kind", () => {
    const links = titled('graph', { focus: 'p/nota-alfa.md', depth: 1 }, 'Links · Nota alfa')
    expect(retitle(links, 'en', 'it')).toBe('Legami · Nota alfa')
    expect(
      retitle(titled('history', { path: 'p/nota-alfa.md' }, 'History · Nota alfa'), 'en', 'it'),
    ).toBe('Cronologia · Nota alfa')
  })

  it('leaves alone a title a view gave', () => {
    expect(retitle(titled('note', { path: 'p/nota-alfa.md' }, 'Nota alfa'), 'en', 'it')).toBeNull()
    expect(
      retitle(titled('graph', { focus: 'p/a.md', depth: 1 }, 'Legami · a'), 'en', 'it'),
    ).toBeNull()
  })

  it('names a history window from the address after its kind', () => {
    expect(codec.decode(codec.encode(win('history', { path: 'p/nota-alfa.md' }))!)?.title).toBe(
      'History · nota-alfa',
    )
  })
})
