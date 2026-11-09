import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import itUI from '@catalogs/ui.it.json'
import type { MCPToken } from '@/api/admin'

vi.mock('@/api/admin', () => ({
  listMCPTokens: vi.fn(),
  createMCPToken: vi.fn(),
  revokeMCPToken: vi.fn(),
  setMCPTokenOptIn: vi.fn(),
  listUsers: vi.fn().mockResolvedValue([]),
}))
vi.mock('@/composables/useConfirm', () => ({ confirmAction: vi.fn() }))

import { listMCPTokens } from '@/api/admin'
import AdminTokensView from '@/views/admin/AdminTokensView.vue'

const token: MCPToken = {
  id: 't1',
  name: 'bench',
  scopes: ['read', 'write'],
  created_at: '2026-11-03T10:00:00Z',
  self_improve_opt_in: false,
}

describe('AdminTokensView', () => {
  it("names a token's scopes in the reader's language, the code in the tooltip (IMP-220, F-20)", async () => {
    vi.mocked(listMCPTokens).mockResolvedValue([token])
    const i18n = createI18n({ legacy: false, locale: 'it', fallbackLocale: 'en', messages: { it: itUI, en: enUI } })
    const w = mount(AdminTokensView, { global: { plugins: [i18n] } })
    await flushPromises()
    const chips = w.findAll('[data-scope]')
    expect(chips.map((c) => c.text())).toEqual(['lettura', 'scrittura'])
    expect(chips.map((c) => c.attributes('title'))).toEqual(['read', 'write'])
  })
})
