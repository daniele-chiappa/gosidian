import { describe, expect, it } from 'vitest'
import { filterClass, filterStyle } from '@/components/domain/sanitizePreview'

// The sanitizer itself is checked in a real browser (tests/e2e/
// sanitize.local.spec.ts): happy-dom does not run DOMPurify faithfully.
describe('filterStyle (BUG-117, S7-4)', () => {
  it('keeps the color, weight, slant, alignment and decoration of the text', () => {
    expect(filterStyle('color: red; font-weight: bold')).toBe('color: red; font-weight: bold')
    expect(filterStyle('background: #ffee00')).toBe('background-color: #ffee00')
    expect(filterStyle('text-align:center;text-decoration: underline wavy rgb(1, 2, 3)')).toBe(
      'text-align: center; text-decoration: underline wavy rgb(1, 2, 3)',
    )
  })

  it('drops what places, sizes, layers or fetches', () => {
    expect(filterStyle('position: fixed; inset: 0; color: red')).toBe('color: red')
    expect(filterStyle('z-index: 9999; width: 100vw; height: 100vh')).toBe('')
    expect(filterStyle('background: url(https://evil.example/x.png)')).toBe('')
    expect(filterStyle('background-color: image-set("x.png" 1x)')).toBe('')
    expect(filterStyle('color: red !important')).toBe('')
    expect(filterStyle('color: \\72 ed')).toBe('')
    expect(filterStyle('color: var(--color-bg)')).toBe('')
  })
})

describe('filterClass (BUG-117, S7-4)', () => {
  it("keeps the renderer's classes", () => {
    expect(filterClass('wikilink unresolved')).toBe('wikilink unresolved')
    expect(filterClass('gosidian-count gosidian-count-error')).toBe('gosidian-count gosidian-count-error')
    expect(filterClass('callout callout-warning')).toBe('callout callout-warning')
    expect(filterClass('chroma')).toBe('chroma')
    expect(filterClass('kd')).toBe('kd')
    expect(filterClass('language-go')).toBe('language-go')
  })

  it("drops the app's classes, which position and cover", () => {
    expect(filterClass('fixed inset-0 z-50')).toBe('')
    expect(filterClass('absolute top-0 left-0 w-screen h-screen bg-bg')).toBe('')
    expect(filterClass('tag fixed')).toBe('tag')
  })
})
