import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import type { SearchHit } from '@/api/search'

vi.mock('@/api/search', () => ({ search: vi.fn() }))
const windows = vi.hoisted(() => ({ focused: null as { type: string } | null }))
vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => ({ open: vi.fn(), focused: windows.focused }),
}))
vi.mock('@vueuse/core', () => ({
  // No debounce in the test: each change searches at once.
  useDebounceFn: (fn: () => void) => fn,
}))

vi.mock('@/composables/useAnnounce', () => ({ announce: vi.fn() }))

import { search } from '@/api/search'
import { announce } from '@/composables/useAnnounce'
import { paletteRequest } from '@/composables/usePalette'
import SearchView from '@/views/SearchView.vue'

const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })

const hit = (path: string): SearchHit => ({ path, title: path, snippet: '' })

describe('SearchView (BUG-116, S6-10)', () => {
  beforeEach(() => vi.clearAllMocks())

  it('keeps the answer to the last query when an older one lands after it', async () => {
    let releaseOld: (v: SearchHit[]) => void = () => {}
    vi.mocked(search).mockImplementation(({ q }) =>
      q === 'old' ? new Promise((r) => (releaseOld = r)) : Promise.resolve([hit('new.md')]),
    )
    const w = mount(SearchView, { global: { plugins: [i18n()] } })
    const input = w.find('input[type="search"]')
    await input.setValue('old')
    await input.setValue('new')
    await flushPromises()
    releaseOld([hit('old.md')])
    await flushPromises()
    expect(w.text()).toContain('new.md')
    expect(w.text()).not.toContain('old.md')
  })

  it('marks a partial hit with the words its note lacks, and a full one not', async () => {
    vi.mocked(search).mockResolvedValue([hit('full.md'), { ...hit('half.md'), missing: ['deploy', 'api'] }])
    const w = mount(SearchView, { global: { plugins: [i18n()] } })
    await w.find('input[type="search"]').setValue('friday deploy api')
    await flushPromises()
    const items = w.findAll('li')
    expect(items).toHaveLength(2)
    expect(items[0]!.find('[data-testid="search-missing"]').exists()).toBe(false)
    expect(items[1]!.find('[data-testid="search-missing"]').text()).toBe('Without: deploy, api')
  })

  it('says nothing of a text once the field is cleared', async () => {
    vi.mocked(search).mockResolvedValue([])
    const w = mount(SearchView, { global: { plugins: [i18n()] } })
    const input = w.find('input[type="search"]')
    await input.setValue('nothing')
    await flushPromises()
    expect(w.text()).toContain('No matches for nothing.')
    await input.setValue('')
    await flushPromises()
    expect(w.text()).not.toContain('No matches')
    expect(w.find('[data-testid="search-in-titles"]').exists()).toBe(false)
  })

  it('stays empty when the query is cleared while a search is on its way', async () => {
    let release: (v: SearchHit[]) => void = () => {}
    vi.mocked(search).mockImplementation(() => new Promise((r) => (release = r)))
    const w = mount(SearchView, { global: { plugins: [i18n()] } })
    const input = w.find('input[type="search"]')
    await input.setValue('x')
    await input.setValue('')
    release([hit('late.md')])
    await flushPromises()
    expect(w.text()).not.toContain('late.md')
  })
})

describe('SearchView, the results said (IMP-217, F-10)', () => {
  beforeEach(() => vi.clearAllMocks())

  it('says how many notes a search found, or that none did', async () => {
    vi.mocked(search).mockResolvedValueOnce([hit('a.md'), hit('b.md')]).mockResolvedValueOnce([hit('a.md')]).mockResolvedValueOnce([])
    const w = mount(SearchView, { global: { plugins: [i18n()] } })
    const input = w.find('input[type="search"]')
    await input.setValue('deploy')
    await flushPromises()
    expect(announce).toHaveBeenLastCalledWith('2 results')
    await input.setValue('deploy friday')
    await flushPromises()
    expect(announce).toHaveBeenLastCalledWith('1 result')
    await input.setValue('nothing')
    await flushPromises()
    expect(announce).toHaveBeenLastCalledWith('No matches for nothing.')
    // A query cleared says nothing.
    vi.mocked(announce).mockClear()
    await input.setValue('')
    await flushPromises()
    expect(announce).not.toHaveBeenCalled()
  })
})

describe('SearchView, the focus at mount', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    windows.focused = null
  })

  it('takes it in its field as the active window, as /search opens it', () => {
    windows.focused = { type: 'search' }
    const w = mount(SearchView, { global: { plugins: [i18n()] } })
    expect(w.find('input[type="search"]').attributes()).toHaveProperty('autofocus')
  })

  it('leaves it to the active window when a reload brings it back beside', () => {
    windows.focused = { type: 'note' }
    const w = mount(SearchView, { global: { plugins: [i18n()] } })
    expect(w.find('input[type="search"]').attributes()).not.toHaveProperty('autofocus')
  })
})

describe('SearchView, a text handed to it (IMP-218, F-06)', () => {
  beforeEach(() => vi.clearAllMocks())

  it('searches the text the palette hands to it while open, the same one again too', async () => {
    vi.mocked(search).mockResolvedValue([hit('a.md')])
    const w = mount(SearchView, { props: { q: 'first', qAt: 1 }, global: { plugins: [i18n()] } })
    await flushPromises()
    const input = w.find<HTMLInputElement>('input[type="search"]')
    expect(input.element.value).toBe('first')
    await w.setProps({ q: 'second', qAt: 2 })
    await flushPromises()
    expect(input.element.value).toBe('second')
    expect(search).toHaveBeenLastCalledWith(expect.objectContaining({ q: 'second' }))
    // Typed over, then asked again: the text asked wins.
    await input.setValue('typed')
    await w.setProps({ q: 'second', qAt: 3 })
    await flushPromises()
    expect(input.element.value).toBe('second')
    expect(search).toHaveBeenLastCalledWith(expect.objectContaining({ q: 'second' }))
  })
})

describe('SearchView, what a search found (IMP-218, F-26)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    paletteRequest.value = null
  })

  it('says how many notes it found above them, and the project of each', async () => {
    vi.mocked(search).mockResolvedValue([
      { path: 'alpha/notes/same.md', title: 'Same', snippet: '' },
      { path: 'beta/same.md', title: 'Same', snippet: '' },
    ])
    const w = mount(SearchView, { global: { plugins: [i18n()] } })
    await w.find('input[type="search"]').setValue('same')
    await flushPromises()
    const count = w.find('[data-testid="search-count"]')
    expect(count.text()).toBe('2 results')
    // Above the list, where the eye starts.
    expect(count.element.compareDocumentPosition(w.find('ul').element) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    // Two notes titled alike, told apart by their project, always in the same place.
    expect(w.findAll('[data-testid="search-project"]').map((e) => e.text())).toEqual(['alpha', 'beta'])
  })

  it('with nothing found gives tips, the project one only with a project filter', async () => {
    vi.mocked(search).mockResolvedValue([])
    const w = mount(SearchView, { global: { plugins: [i18n()] }, attachTo: document.body })
    const input = w.find('input[type="search"]')
    await input.setValue('nothing')
    await flushPromises()
    expect(w.text()).toContain('No matches for nothing.')
    expect(w.text()).toContain('Check how the words are spelled.')
    expect(w.find('[data-testid="search-clear-project"]').exists()).toBe(false)
    expect(w.find('[data-testid="search-count"]').exists()).toBe(false)

    const project = w.findAll('input')[1]!
    await project.setValue('alpha')
    await flushPromises()
    expect(search).toHaveBeenLastCalledWith(expect.objectContaining({ q: 'nothing', project: 'alpha' }))
    const clear = w.find('[data-testid="search-clear-project"]')
    expect(clear.text()).toBe('Remove the project filter')
    await clear.trigger('click')
    await flushPromises()
    expect((project.element as HTMLInputElement).value).toBe('')
    expect(search).toHaveBeenLastCalledWith(expect.objectContaining({ q: 'nothing', project: undefined }))
    // The button went: the focus goes to the field of the text, not to the page.
    expect(document.activeElement).toBe(input.element)
    w.unmount()
  })

  it('looks for the text in the names, in the palette, which Ctrl+K opens', async () => {
    vi.mocked(search).mockResolvedValue([])
    const w = mount(SearchView, { global: { plugins: [i18n()] } })
    await w.find('input[type="search"]').setValue('nota-b')
    await flushPromises()
    const button = w.find('[data-testid="search-in-titles"]')
    expect(button.text()).toBe('Search the titles Ctrl+K')
    await button.trigger('click')
    expect(paletteRequest.value).toEqual({ q: 'nota-b' })
  })
})
