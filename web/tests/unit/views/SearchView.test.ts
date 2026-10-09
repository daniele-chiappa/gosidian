import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import type { SearchHit } from '@/api/search'

vi.mock('@/api/search', () => ({ search: vi.fn() }))
vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => ({ open: vi.fn() }),
}))
vi.mock('@vueuse/core', () => ({
  // No debounce in the test: each change searches at once.
  useDebounceFn: (fn: () => void) => fn,
}))

import { search } from '@/api/search'
import SearchView from '@/views/SearchView.vue'

const hit = (path: string): SearchHit => ({ path, title: path, snippet: '' })

describe('SearchView (BUG-116, S6-10)', () => {
  beforeEach(() => vi.clearAllMocks())

  it('keeps the answer to the last query when an older one lands after it', async () => {
    let releaseOld: (v: SearchHit[]) => void = () => {}
    vi.mocked(search).mockImplementation(({ q }) =>
      q === 'old' ? new Promise((r) => (releaseOld = r)) : Promise.resolve([hit('new.md')]),
    )
    const w = mount(SearchView)
    const input = w.find('input[type="search"]')
    await input.setValue('old')
    await input.setValue('new')
    await flushPromises()
    releaseOld([hit('old.md')])
    await flushPromises()
    expect(w.text()).toContain('new.md')
    expect(w.text()).not.toContain('old.md')
  })

  it('stays empty when the query is cleared while a search is on its way', async () => {
    let release: (v: SearchHit[]) => void = () => {}
    vi.mocked(search).mockImplementation(() => new Promise((r) => (release = r)))
    const w = mount(SearchView)
    const input = w.find('input[type="search"]')
    await input.setValue('x')
    await input.setValue('')
    release([hit('late.md')])
    await flushPromises()
    expect(w.text()).not.toContain('late.md')
  })
})
