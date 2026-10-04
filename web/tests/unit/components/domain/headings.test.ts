import { describe, expect, it } from 'vitest'
import { findHeading, slug } from '@/components/domain/headings'

function doc(html: string): HTMLElement {
  const root = document.createElement('div')
  root.innerHTML = html
  return root
}

const note = doc(`
  <h1 id="decisions">Decisions</h1>
  <h2 id="adr-010-hot-reload">ADR-010 — Hot reload</h2>
  <h2 id="adr-026-access-model">ADR-026 — Access model</h2>
  <h3 id="adr-026-addendum">ADR-026 — addendum</h3>
  <h2 id="oq-1">OQ-1: first</h2>
  <h2 id="oq-1-b">OQ-1: second</h2>
  <h2 id="local-part">Local part</h2>
`)
const text = (el: HTMLElement | null) => el?.textContent ?? null

describe('findHeading', () => {
  it('finds a heading by its text, ignoring case', () => {
    expect(text(findHeading(note, 'local PART'))).toBe('Local part')
  })
  it('finds a heading by an ID at its start', () => {
    expect(text(findHeading(note, 'ADR-010'))).toBe('ADR-010 — Hot reload')
  })
  it('prefers the one heading of the highest level', () => {
    expect(text(findHeading(note, 'ADR-026'))).toBe('ADR-026 — Access model')
  })
  it('gives up when two headings of the same level start alike', () => {
    expect(findHeading(note, 'OQ-1')).toBeNull()
  })
  it('finds a heading by the anchor id of a link', () => {
    expect(text(findHeading(note, 'local-part'))).toBe('Local part')
    expect(text(findHeading(note, 'adr-010'))).toBe('ADR-010 — Hot reload')
  })
  it('finds nothing for an unknown anchor', () => {
    expect(findHeading(note, 'nowhere')).toBeNull()
    expect(findHeading(note, '  ')).toBeNull()
  })
})

describe('slug', () => {
  it('matches the server', () => {
    expect(slug('ADR-010 — Hot reload')).toBe('adr-010-hot-reload')
    expect(slug('  Trailing!!')).toBe('trailing')
  })
})
