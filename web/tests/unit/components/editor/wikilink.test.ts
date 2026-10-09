import { describe, expect, it } from 'vitest'
import { wikilinkClosing } from '@/components/editor/wikilink'

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
