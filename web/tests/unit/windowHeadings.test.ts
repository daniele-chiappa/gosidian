import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'

// Every window of the plancia names itself with an h1 that the focus can
// reach from a script (IMP-217, F-03): an open moves the focus there.
// Read from disk, as the views load on demand; vitest runs from web/.
const registry = readFileSync('src/components/plancia/windowRegistry.ts', 'utf8')
const views = [...registry.matchAll(/import\('@\/([^']+\.vue)'\)/g)].map((m) => `src/${m[1]}`)
// The admin window draws its tabs under its own headings.
const admin = 'src/views/admin/AdminLayout.vue'
const tabs = [...readFileSync(admin, 'utf8').matchAll(/import\('\.\/([^']+\.vue)'\)/g)].map(
  (m) => `src/views/admin/${m[1]}`,
)

const levels = (file: string) => [...readFileSync(file, 'utf8').matchAll(/<h([1-6])\b/g)].map((m) => Number(m[1]))

/** The places where a heading goes more than one level deeper. */
const skips = (seq: number[]) =>
  seq.flatMap((level, i) => (i > 0 && level > seq[i - 1]! + 1 ? [`h${seq[i - 1]} → h${level}`] : []))

describe('window headings (IMP-217, F-03)', () => {
  it('finds the views of the registry', () => {
    expect(views.length).toBeGreaterThan(15)
    expect(tabs.length).toBeGreaterThan(4)
  })

  it.each(views)('%s has one h1 the focus can reach', (file) => {
    const h1s = [...readFileSync(file, 'utf8').matchAll(/<h1\b[^>]*>/g)].map((m) => m[0])
    expect(h1s).toHaveLength(1)
    expect(h1s[0]).toMatch(/\bdata-window-heading\b/)
    expect(h1s[0]).toMatch(/\btabindex="-1"/)
  })
})

// A reader that walks the headings finds no level missing (IMP-217, F-35).
describe('heading levels (IMP-217, F-35)', () => {
  it.each(views)('%s goes down one level at a time', (file) => {
    expect(skips(levels(file))).toEqual([])
  })

  it.each(tabs)('%s goes on from the admin window', (file) => {
    expect(skips([...levels(admin), ...levels(file)])).toEqual([])
  })
})
