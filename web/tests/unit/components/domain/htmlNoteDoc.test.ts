import { describe, expect, it } from 'vitest'
import { buildSrcdoc, INJECTED_CSP } from '@/components/domain/htmlNoteDoc'

const meta = `<meta http-equiv="Content-Security-Policy" content="${INJECTED_CSP}">`
const before = (doc: string, what: string) => doc.indexOf(meta) >= 0 && doc.indexOf(meta) < doc.indexOf(what)

describe('buildSrcdoc (BUG-117, S7-5)', () => {
  it('puts the CSP before a <header>, which is no <head>', () => {
    const doc = buildSrcdoc('<header><h1>Report</h1></header><script>x()</script>', '')
    expect(doc.startsWith(`<!DOCTYPE html>${meta}`)).toBe(true)
    expect(before(doc, '<header>')).toBe(true)
    expect(before(doc, '<script>x()')).toBe(true)
  })

  it('puts the CSP before a <head> hidden in a comment', () => {
    const doc = buildSrcdoc('<!-- <head> --><img src="https://evil.example/x.png">', '')
    expect(before(doc, '<!-- <head>')).toBe(true)
  })

  it('keeps a doctype first, and the note document after the CSP', () => {
    const doc = buildSrcdoc('<!doctype html><html lang="it"><head><title>T</title></head><body>b</body></html>', 'n1')
    expect(doc.startsWith(`<!doctype html>${meta}<script nonce="n1">`)).toBe(true)
    expect(before(doc, '<html lang="it">')).toBe(true)
  })

  it('adds no doctype to a document written without one', () => {
    const doc = buildSrcdoc('<html><body>quirks</body></html>', '')
    expect(doc.startsWith(meta)).toBe(true)
  })

  it('stamps the nonce on the note scripts and drops the frontmatter', () => {
    const doc = buildSrcdoc('<!--\n---\ntitle: x\n---\n-->\n<p>a</p><script>go()</script>', 'n2')
    expect(doc).not.toContain('title: x')
    expect(doc).toContain('<script nonce="n2">go()')
  })
})
