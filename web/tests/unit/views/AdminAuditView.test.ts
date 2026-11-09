import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import itUI from '@catalogs/ui.it.json'
import type { AuditEntry } from '@/api/admin'

vi.mock('@/api/admin', () => ({ tailAudit: vi.fn() }))
vi.mock('@/composables/useAnnounce', () => ({ announce: vi.fn() }))

import { tailAudit } from '@/api/admin'
import { announce } from '@/composables/useAnnounce'
import AdminAuditView from '@/views/admin/AdminAuditView.vue'

const entry = (action: string): AuditEntry => ({ ts: '2026-11-01T10:00:00Z', source: 'web', actor: 'claude', action, path: 'p/a.md' })
const page = (items: AuditEntry[], total = items.length) => ({ items, total })

describe('AdminAuditView, the filter said (IMP-217, F-10)', () => {
  beforeEach(() => vi.clearAllMocks())

  it('says how many entries a filter keeps, and not at the first load', async () => {
    vi.mocked(tailAudit)
      .mockResolvedValueOnce(page([entry('write'), entry('delete'), entry('write')]))
      .mockResolvedValueOnce(page([entry('delete')]))
      .mockResolvedValueOnce(page([]))
      .mockResolvedValueOnce(page([entry('write'), entry('write')]))
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
    const w = mount(AdminAuditView, { global: { plugins: [i18n] } })
    await flushPromises()
    expect(announce).not.toHaveBeenCalled()

    const action = w.find('select[data-audit-action]')
    await action.setValue('delete')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(announce).toHaveBeenLastCalledWith('1 entry')
    await action.setValue('rename')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(announce).toHaveBeenLastCalledWith('No audit entry matches the filters.')
    await w.findAll('button').find((b) => b.text() === 'Reset')!.trigger('click')
    await flushPromises()
    expect(announce).toHaveBeenLastCalledWith('2 entries')
  })
})

describe('AdminAuditView, the actions in words (IMP-220, F-25)', () => {
  beforeEach(() => vi.clearAllMocks())

  // The phrases come with their own catalogue, imported once the page
  // opens: the menu's groups say it is in.
  const mountView = async (entries: AuditEntry[]) => {
    vi.mocked(tailAudit).mockResolvedValue(page(entries))
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
    const w = mount(AdminAuditView, { global: { plugins: [i18n] } })
    await flushPromises()
    await vi.waitFor(() => expect(w.find('select[data-audit-action] optgroup').exists()).toBe(true))
    return w
  }

  it('says what an entry did, its code in the tooltip', async () => {
    const w = await mountView([entry('spa_token_revoke'), entry('zz_unknown')])
    const cells = w.findAll('tbody tr').map((r) => r.findAll('td')[3]!)
    expect(cells[0]!.text()).toBe('signed out of the web UI')
    expect(cells[0]!.attributes('title')).toBe('spa_token_revoke')
    // An action this web UI does not know shows as its code.
    expect(cells[1]!.text()).toBe('zz_unknown')
  })

  it('filters by an action picked from a menu of groups', async () => {
    const w = await mountView([])
    const select = w.find('select[data-audit-action]')
    const first = select.find('option')
    expect(first.attributes('value')).toBe('')
    expect(first.text()).toBe('all')
    const groups = select.findAll('optgroup').map((g) => g.attributes('label'))
    expect(groups).toContain('Notes and files')
    expect(groups).toContain('Sign-ins')
    const opt = select.find('option[value="spa_token_revoke"]')
    expect(opt.text()).toBe('signed out of the web UI')
    expect(opt.attributes('title')).toBe('spa_token_revoke')
    await select.setValue('spa_token_revoke')
    await w.find('form').trigger('submit')
    await flushPromises()
    expect(tailAudit).toHaveBeenLastCalledWith(expect.objectContaining({ action: 'spa_token_revoke' }))
  })

  it("loads the phrases only on the audit's page, in the reader's language", async () => {
    // The first load's catalogue does not hold them: a Go test on the
    // catalogues sees to it (IMP-220).
    vi.mocked(tailAudit).mockResolvedValue(page([entry('spa_token_revoke')]))
    const i18n = createI18n({ legacy: false, locale: 'it', fallbackLocale: 'en', messages: { en: enUI, it: itUI } })
    const w = mount(AdminAuditView, { global: { plugins: [i18n] } })
    await flushPromises()
    await vi.waitFor(() => expect(w.find('tbody tr td:nth-child(4)').text()).toBe('uscita dalla web UI'))
  })
})

describe('AdminAuditView, the newest first, in pages (IMP-224, F-30)', () => {
  beforeEach(() => vi.clearAllMocks())

  // The pages' texts come with the audit's catalogue.
  const mountView = async (filter?: string) => {
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
    const w = mount(AdminAuditView, { props: { filter }, global: { plugins: [i18n] } })
    await flushPromises()
    await vi.waitFor(() => expect(w.find('[data-audit-pages]').exists()).toBe(true))
    return w
  }

  it('asks for the newest first, and turns the pages', async () => {
    vi.mocked(tailAudit).mockResolvedValue(page([entry('write'), entry('delete')], 250))
    const w = await mountView()
    expect(tailAudit).toHaveBeenLastCalledWith(expect.objectContaining({ order: 'desc', offset: 0, limit: 100 }))
    expect(w.find('th[aria-sort]').attributes('aria-sort')).toBe('descending')
    const nav = w.find('[data-audit-pages]')
    expect(nav.attributes('aria-label')).toBe('Pages of the audit')
    expect(nav.text()).toContain('1–2 of 250')
    expect(w.find('[data-audit-previous]').attributes('aria-disabled')).toBe('true')

    await w.find('[data-audit-next]').trigger('click')
    await flushPromises()
    expect(tailAudit).toHaveBeenLastCalledWith(expect.objectContaining({ offset: 100 }))
    expect(announce).toHaveBeenLastCalledWith('101–102 of 250')
    expect(w.emitted('update:filter')!.at(-1)).toEqual(['page=2'])
    expect(w.find('[data-audit-previous]').attributes('aria-disabled')).toBeUndefined()

    // On the last page Next stays in place, and does nothing.
    await w.find('[data-audit-next]').trigger('click')
    await flushPromises()
    expect(w.find('[data-audit-next]').attributes('aria-disabled')).toBe('true')
    const calls = vi.mocked(tailAudit).mock.calls.length
    await w.find('[data-audit-next]').trigger('click')
    await flushPromises()
    expect(tailAudit).toHaveBeenCalledTimes(calls)
  })

  it('sorts by time from the oldest, back on the first page, with the filter of the address', async () => {
    vi.mocked(tailAudit).mockResolvedValue(page([entry('write')]))
    const w = await mountView('actor=claude&page=2')
    expect(tailAudit).toHaveBeenLastCalledWith(expect.objectContaining({ actor: 'claude', order: 'desc', offset: 100 }))
    expect((w.find('input[type="text"]').element as HTMLInputElement).value).toBe('claude')

    await w.find('[data-audit-order]').trigger('click')
    await flushPromises()
    expect(tailAudit).toHaveBeenLastCalledWith(expect.objectContaining({ actor: 'claude', order: 'asc', offset: 0 }))
    expect(w.find('th[aria-sort]').attributes('aria-sort')).toBe('ascending')
    expect(w.find('[data-audit-order]').attributes('title')).toBe('Oldest first')
    expect(announce).toHaveBeenLastCalledWith('Oldest first')
    expect(w.emitted('update:filter')!.at(-1)).toEqual(['actor=claude&order=asc'])
  })

  it('shows the last page when the address asks for one past the end', async () => {
    vi.mocked(tailAudit).mockImplementation(async (q) => (q!.offset! >= 120 ? page([], 120) : page([entry('write')], 120)))
    const w = await mountView('limit=50&page=5')
    expect(tailAudit).toHaveBeenLastCalledWith(expect.objectContaining({ limit: 50, offset: 100 }))
    expect(w.emitted('update:filter')!.at(-1)).toEqual(['limit=50&page=3'])
    expect(w.find('[data-audit-pages]').text()).toContain('101–101 of 120')
  })
})
