import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import type { CommandPaletteData } from '@/api/commandPalette'

vi.mock('@/api/commandPalette', () => ({ fetchCommandPalette: vi.fn() }))
vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => ({ open: vi.fn() }),
}))
vi.mock('@/composables/useRecentlyViewed', () => ({ useRecentlyViewed: () => ({ entries: { value: [] } }) }))

import { fetchCommandPalette } from '@/api/commandPalette'
import CommandPalette from '@/components/layout/CommandPalette.vue'

const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
const data = (...titles: string[]): CommandPaletteData => ({
  notes: titles.map((t) => ({ path: `p/${t}.md`, title: t })),
  projects: [],
  tags: [],
})
const ctrlK = () => window.dispatchEvent(new KeyboardEvent('keydown', { key: 'k', ctrlKey: true }))

describe('CommandPalette', () => {
  beforeEach(() => vi.clearAllMocks())

  it('reads the list again at each opening, showing the old one meanwhile', async () => {
    vi.mocked(fetchCommandPalette).mockResolvedValueOnce(data('First'))
    const w = mount(CommandPalette, { global: { plugins: [i18n()] }, attachTo: document.body })
    ctrlK()
    await flushPromises()
    expect(document.body.textContent).toContain('First')
    ctrlK() // closes
    await flushPromises()

    let release: (d: CommandPaletteData) => void = () => {}
    vi.mocked(fetchCommandPalette).mockReturnValueOnce(new Promise((r) => (release = r)))
    ctrlK()
    await flushPromises()
    expect(document.body.textContent).toContain('First')
    release(data('First', 'Created later'))
    await flushPromises()
    expect(document.body.textContent).toContain('Created later')
    expect(fetchCommandPalette).toHaveBeenCalledTimes(2)
    w.unmount()
  })

  it('keeps the entry the keyboard is on when the new list lands', async () => {
    vi.mocked(fetchCommandPalette).mockResolvedValueOnce(data('Bravo', 'Charlie'))
    const w = mount(CommandPalette, { global: { plugins: [i18n()] }, attachTo: document.body })
    ctrlK()
    await flushPromises()
    ctrlK() // closes
    let release: (d: CommandPaletteData) => void = () => {}
    vi.mocked(fetchCommandPalette).mockReturnValueOnce(new Promise((r) => (release = r)))
    ctrlK()
    await flushPromises()
    const input = document.body.querySelector('input')!
    input.value = 'r'
    input.dispatchEvent(new Event('input'))
    await flushPromises()
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown' }))
    await flushPromises()
    const selectedText = () => document.body.querySelector('li.bg-surface-hover')?.textContent ?? ''
    const before = selectedText()
    // A new note that sorts first, in front of the one selected.
    release(data('Arrr', 'Bravo', 'Charlie'))
    await flushPromises()
    expect(selectedText()).toBe(before)
    w.unmount()
  })
  it('names the kind of an entry only when the list mixes kinds', async () => {
    vi.mocked(fetchCommandPalette).mockResolvedValue({
      notes: [{ path: 'p/alpha.md', title: 'alpha' }],
      projects: [{ name: 'alpine', noteCount: 3 }],
      tags: [],
    } as CommandPaletteData)
    const w = mount(CommandPalette, { global: { plugins: [i18n()] }, attachTo: document.body })
    ctrlK()
    await flushPromises()
    const rows = () =>
      [...document.body.querySelectorAll('li')].map((li) => [...li.children].map((c) => c.textContent?.trim()).join(' '))
    expect(rows()).toEqual(['note alpha p/alpha.md', 'project alpine 3 notes'])
    const input = document.body.querySelector('input')!
    input.value = 'alpha'
    input.dispatchEvent(new Event('input'))
    await flushPromises()
    expect(rows()).toEqual(['alpha p/alpha.md'])
    w.unmount()
  })
})
