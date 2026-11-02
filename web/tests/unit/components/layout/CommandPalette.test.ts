import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import type { CommandPaletteData } from '@/api/commandPalette'

vi.mock('@/api/commandPalette', () => ({ fetchCommandPalette: vi.fn() }))
const windows = vi.hoisted(() => ({ open: vi.fn((): string => ''), identify: vi.fn() }))
vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => windows,
  usePlanciaFocus: () => ({ focusWindow: vi.fn(async () => true) }),
}))
vi.mock('@/composables/useRecentlyViewed', () => ({ useRecentlyViewed: () => ({ entries: { value: [] } }) }))

import { fetchCommandPalette } from '@/api/commandPalette'
import CommandPalette from '@/components/layout/CommandPalette.vue'
import { planciaKey } from '@/composables/planciaKey'
import { openPalette } from '@/composables/usePalette'

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
      [...document.body.querySelectorAll('li')].map((li) =>
        [...li.children].map((c) => c.textContent?.trim()).join(' ').trim(),
      )
    expect(rows()).toEqual(['note alpha p/alpha.md', 'project alpine 3 notes'])
    const input = document.body.querySelector('input')!
    input.value = 'alpha'
    input.dispatchEvent(new Event('input'))
    await flushPromises()
    // The entry that searches the text is no kind of name.
    expect(rows()).toEqual(['alpha p/alpha.md', 'Search the text for “alpha”'])
    w.unmount()
  })
})

describe('CommandPalette, the focus it gives back (IMP-217, F-04)', () => {
  beforeEach(() => vi.clearAllMocks())

  function opener() {
    const b = document.createElement('button')
    document.body.append(b)
    b.focus()
    return b
  }
  const key = (k: string) => window.dispatchEvent(new KeyboardEvent('keydown', { key: k }))

  it('gives it back to where it was opened from on Esc, Ctrl+K and a click outside', async () => {
    vi.mocked(fetchCommandPalette).mockResolvedValue(data('Alpha'))
    const w = mount(CommandPalette, { global: { plugins: [i18n()] }, attachTo: document.body })
    const from = opener()
    for (const close of [() => key('Escape'), ctrlK, () => document.body.querySelector<HTMLElement>('[role="dialog"]')!.click()]) {
      ctrlK()
      await flushPromises()
      expect(document.activeElement).toBe(document.body.querySelector('[role="dialog"] input'))
      close()
      await flushPromises()
      expect(document.body.querySelector('[role="dialog"]')).toBeNull()
      expect(document.activeElement).toBe(from)
    }
    w.unmount()
    from.remove()
  })

  it('leaves it to the window an entry opens', async () => {
    vi.mocked(fetchCommandPalette).mockResolvedValue(data('Alpha'))
    const w = mount(CommandPalette, { global: { plugins: [i18n()] }, attachTo: document.body })
    const from = opener()
    ctrlK()
    await flushPromises()
    key('Enter')
    await flushPromises()
    expect(document.activeElement).not.toBe(from)
    w.unmount()
    from.remove()
  })
})

describe('CommandPalette, the text looked for in the notes (IMP-218, F-06)', () => {
  beforeEach(() => vi.clearAllMocks())

  const type = async (text: string) => {
    const input = document.body.querySelector<HTMLInputElement>('[role="dialog"] input')!
    input.value = text
    input.dispatchEvent(new Event('input'))
    await flushPromises()
  }
  const rows = () =>
    [...document.body.querySelectorAll('[role="dialog"] li, [role="dialog"] p')].map((el) => el.textContent?.trim())

  it('ends the list with an entry that searches the text, once there is a text', async () => {
    vi.mocked(fetchCommandPalette).mockResolvedValue(data('Deploy', 'Deploy notes'))
    const w = mount(CommandPalette, { global: { plugins: [i18n()] }, attachTo: document.body })
    ctrlK()
    await flushPromises()
    expect(rows()).toHaveLength(2)
    await type('deploy')
    expect(rows()).toHaveLength(3)
    expect(rows().at(-1)).toBe('Search the text for “deploy”')
    // No name has it: the notes' text still may.
    await type('nothing named so')
    expect(rows()).toEqual(['No matches. Press Esc to close.', 'Search the text for “nothing named so”'])
    w.unmount()
  })

  it('opens Search with the text, and hands it to the Search already open', async () => {
    vi.mocked(fetchCommandPalette).mockResolvedValue(data('Deploy'))
    windows.open.mockReturnValue('w-search')
    const w = mount(CommandPalette, { global: { plugins: [i18n()] }, attachTo: document.body })
    ctrlK()
    await flushPromises()
    await type('  friday deploy ')
    document.body.querySelector<HTMLElement>('[role="dialog"] li:last-child')!.click()
    await flushPromises()
    expect(document.body.querySelector('[role="dialog"]')).toBeNull()
    const key = planciaKey('search')
    expect(windows.open).toHaveBeenCalledWith(
      expect.objectContaining({ type: 'search', key, props: expect.objectContaining({ q: 'friday deploy' }) }),
    )
    // open only focuses a Search already there: the text goes to it, with
    // when it was asked, so the same text asked again searches again.
    expect(windows.identify).toHaveBeenCalledWith('w-search', key, { q: 'friday deploy', qAt: expect.any(Number) })

    // Nothing named so: the entry is the first, and Enter takes it.
    windows.identify.mockClear()
    ctrlK()
    await flushPromises()
    await type('zzz')
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter' }))
    await flushPromises()
    expect(windows.identify).toHaveBeenCalledWith('w-search', key, { q: 'zzz', qAt: expect.any(Number) })
    w.unmount()
  })
})

describe('CommandPalette as a combobox (IMP-218, F-05)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    // jsdom has no layout: the call is what there is to see.
    Element.prototype.scrollIntoView = vi.fn()
  })

  const input = () => document.body.querySelector<HTMLInputElement>('[role="dialog"] input')!
  const type = async (text: string) => {
    input().value = text
    input().dispatchEvent(new Event('input'))
    await flushPromises()
  }
  const press = async (key: string, shiftKey = false) => {
    const e = new KeyboardEvent('keydown', { key, shiftKey, cancelable: true })
    window.dispatchEvent(e)
    await flushPromises()
    return e.defaultPrevented
  }
  const options = () => [...document.body.querySelectorAll<HTMLElement>('[role="listbox"] [role="option"]')]
  const active = () => document.getElementById(input().getAttribute('aria-activedescendant') ?? '')

  it('names the dialog and its field, and ties the field to its list', async () => {
    vi.mocked(fetchCommandPalette).mockResolvedValue(data('Alpha', 'Beta'))
    const w = mount(CommandPalette, { global: { plugins: [i18n()] }, attachTo: document.body })
    ctrlK()
    await flushPromises()
    const dialog = document.body.querySelector('[role="dialog"]')!
    const title = document.getElementById(dialog.getAttribute('aria-labelledby') ?? '')
    expect(title?.textContent?.trim()).toBe('Jump to a note, a project or a tag')
    expect(input().getAttribute('aria-labelledby')).toBe(title!.id)
    expect(input().getAttribute('role')).toBe('combobox')
    expect(input().getAttribute('aria-autocomplete')).toBe('list')
    expect(input().getAttribute('aria-expanded')).toBe('true')
    const list = document.getElementById(input().getAttribute('aria-controls') ?? '')
    expect(list?.getAttribute('role')).toBe('listbox')
    expect(options().map((o) => o.getAttribute('aria-selected'))).toEqual(['true', 'false'])
    expect(active()).toBe(options()[0])
    w.unmount()
  })

  it('says what is chosen as the arrows, Home and End move, and keeps it in view', async () => {
    vi.mocked(fetchCommandPalette).mockResolvedValue(data('Alpha', 'Beta', 'Gamma'))
    const w = mount(CommandPalette, { global: { plugins: [i18n()] }, attachTo: document.body })
    ctrlK()
    await flushPromises()
    await type('a')
    expect(options()).toHaveLength(4) // three names and the text search
    // Before the arrows, Home and End are the caret's, in the text.
    expect(await press('End')).toBe(false)
    expect(active()).toBe(options()[0])
    expect(await press('ArrowDown')).toBe(true)
    expect(active()).toBe(options()[1])
    expect(options()[1]!.getAttribute('aria-selected')).toBe('true')
    expect(options()[1]!.scrollIntoView).toHaveBeenLastCalledWith({ block: 'nearest' })
    expect(vi.mocked(Element.prototype.scrollIntoView).mock.contexts.at(-1)).toBe(options()[1])
    expect(await press('End')).toBe(true)
    expect(active()).toBe(options()[3])
    expect(vi.mocked(Element.prototype.scrollIntoView).mock.contexts.at(-1)).toBe(options()[3])
    expect(await press('Home')).toBe(true)
    expect(active()).toBe(options()[0])
    // A new text starts over: Home is the caret's again.
    await type('al')
    expect(await press('Home')).toBe(false)
    w.unmount()
  })

  it('keeps Tab and Shift+Tab in its field, its one control', async () => {
    vi.mocked(fetchCommandPalette).mockResolvedValue(data('Alpha'))
    const w = mount(CommandPalette, { global: { plugins: [i18n()] }, attachTo: document.body })
    ctrlK()
    await flushPromises()
    expect(await press('Tab')).toBe(true)
    expect(document.activeElement).toBe(input())
    // A click on the panel left the focus on the page: Tab brings it back.
    input().blur()
    expect(await press('Tab', true)).toBe(true)
    expect(document.activeElement).toBe(input())
    w.unmount()
  })

  it('says that no name matches beside the list, not in it', async () => {
    vi.mocked(fetchCommandPalette).mockResolvedValue(data('Alpha'))
    const w = mount(CommandPalette, { global: { plugins: [i18n()] }, attachTo: document.body })
    ctrlK()
    await flushPromises()
    await type('zzz')
    expect(options().map((o) => o.textContent?.trim())).toEqual(['Search the text for “zzz”'])
    expect(document.body.querySelector('[role="listbox"]')?.textContent).not.toContain('No matches')
    expect(document.body.querySelector('[role="dialog"]')?.textContent).toContain('No matches')
    w.unmount()
  })

  it('has no list to point to while it has nothing', async () => {
    vi.mocked(fetchCommandPalette).mockResolvedValue(data())
    const w = mount(CommandPalette, { global: { plugins: [i18n()] }, attachTo: document.body })
    ctrlK()
    await flushPromises()
    expect(document.body.querySelector('[role="listbox"]')).toBeNull()
    expect(input().getAttribute('aria-expanded')).toBe('false')
    expect(input().hasAttribute('aria-activedescendant')).toBe(false)
    w.unmount()
  })
})

describe('CommandPalette, opened by another view (IMP-218, F-26)', () => {
  beforeEach(() => vi.clearAllMocks())

  it('opens with the text it is handed, and gives the focus back there on Esc', async () => {
    vi.mocked(fetchCommandPalette).mockResolvedValue(data('Alpha', 'Beta'))
    const w = mount(CommandPalette, { global: { plugins: [i18n()] }, attachTo: document.body })
    const from = document.createElement('button')
    document.body.append(from)
    from.focus()
    openPalette('alp')
    await flushPromises()
    const input = document.body.querySelector<HTMLInputElement>('[role="dialog"] input')!
    expect(input.value).toBe('alp')
    expect(document.activeElement).toBe(input)
    const options = [...document.body.querySelectorAll('[role="option"]')].map((o) => o.textContent?.trim())
    expect(options).toEqual(['Alphap/Alpha.md', 'Search the text for “alp”'])
    // The same text again opens it again.
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await flushPromises()
    expect(document.activeElement).toBe(from)
    openPalette('alp')
    await flushPromises()
    expect(document.body.querySelector('[role="dialog"]')).not.toBeNull()
    w.unmount()
    from.remove()
  })
})
