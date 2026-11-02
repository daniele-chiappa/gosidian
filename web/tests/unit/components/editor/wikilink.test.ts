import { beforeEach, describe, expect, it, vi } from 'vitest'
import { CompletionContext } from '@codemirror/autocomplete'
import { EditorState } from '@codemirror/state'

vi.mock('@/api/noteTitles', () => ({ suggestNoteTitles: vi.fn() }))

import { suggestNoteTitles } from '@/api/noteTitles'
import { wikilinkClosing, wikilinkSource } from '@/components/editor/wikilink'

describe('wikilinkClosing (BUG-117, S7-6)', () => {
  it('adds no ]] where closeBrackets already put them', () => {
    expect(wikilinkClosing(']] and more')).toEqual({ insert: '', skip: 2 })
  })
  it('completes a half-closed link', () => {
    expect(wikilinkClosing('] x')).toEqual({ insert: ']', skip: 1 })
  })
  it('closes an open link', () => {
    expect(wikilinkClosing(' text')).toEqual({ insert: ']]', skip: 0 })
    expect(wikilinkClosing('')).toEqual({ insert: ']]', skip: 0 })
  })
})

describe('wikilinkSource (IMP-218, F-27)', () => {
  const at = (doc: string, explicit = false) => new CompletionContext(EditorState.create({ doc }), doc.length, explicit)
  beforeEach(() => vi.clearAllMocks())

  it('leaves the filter and the order to the server: the title, the path beside', async () => {
    vi.mocked(suggestNoteTitles).mockResolvedValue([
      { title: 'Nota beta', path: 'agile-lab/notes/nota-beta.md' },
      { title: '', path: 'agile-lab/pagina.html' },
    ])
    const r = await wikilinkSource(at('see [[nota-b'))
    expect(suggestNoteTitles).toHaveBeenCalledWith('nota-b', 10)
    // CodeMirror filtered the server's hits again on their label, the
    // title: "Nota beta" does not contain "nota-b" and was dropped.
    expect(r?.filter).toBe(false)
    expect(r?.from).toBe(6)
    expect(r?.options.map((o) => [o.label, o.detail])).toEqual([
      ['Nota beta', 'agile-lab/notes/nota-beta.md'],
      ['agile-lab/pagina.html', undefined],
    ])
  })

  it('asks nothing outside an open link, or before a letter unless asked', async () => {
    expect(await wikilinkSource(at('see [[a]] b'))).toBeNull()
    expect(await wikilinkSource(at('see [['))).toBeNull()
    expect(suggestNoteTitles).not.toHaveBeenCalled()
    vi.mocked(suggestNoteTitles).mockResolvedValue([])
    await wikilinkSource(at('see [[', true))
    expect(suggestNoteTitles).toHaveBeenCalledWith('', 10)
  })
})
