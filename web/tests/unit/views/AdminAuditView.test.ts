import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import type { AuditEntry } from '@/api/admin'

vi.mock('@/api/admin', () => ({ tailAudit: vi.fn() }))
vi.mock('@/composables/useAnnounce', () => ({ announce: vi.fn() }))

import { tailAudit } from '@/api/admin'
import { announce } from '@/composables/useAnnounce'
import AdminAuditView from '@/views/admin/AdminAuditView.vue'

const entry = (action: string): AuditEntry => ({ ts: '2026-11-01T10:00:00Z', source: 'web', actor: 'claude', action, path: 'p/a.md' })

describe('AdminAuditView, the filter said (IMP-217, F-10)', () => {
  beforeEach(() => vi.clearAllMocks())

  it('says how many entries a filter keeps, and not at the first load', async () => {
    vi.mocked(tailAudit)
      .mockResolvedValueOnce([entry('write'), entry('delete'), entry('write')])
      .mockResolvedValueOnce([entry('delete')])
      .mockResolvedValueOnce([])
      .mockResolvedValueOnce([entry('write'), entry('write')])
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
    const w = mount(AdminAuditView, { global: { plugins: [i18n] } })
    await flushPromises()
    expect(announce).not.toHaveBeenCalled()

    const action = w.findAll('input[type="text"]')[1]!
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
