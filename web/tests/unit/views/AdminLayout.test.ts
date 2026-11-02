import { describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'

vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isOwner: true }) }))
vi.mock('@/api/admin', () => ({ exportVault: vi.fn() }))
// The tabs load their data on mount: here they only draw their name.
vi.mock('@/views/admin/AdminUsersView.vue', () => ({ __esModule: true, default: { render: () => 'users tab' } }))
vi.mock('@/views/admin/AdminTeamsView.vue', () => ({ __esModule: true, default: { render: () => 'teams tab' } }))

import AdminLayout from '@/views/admin/AdminLayout.vue'

describe('AdminLayout headings (IMP-217, F-35)', () => {
  it('names the tab it shows in an h2, under the window h1', async () => {
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
    const w = mount(AdminLayout, { global: { plugins: [i18n] } })
    await flushPromises()
    expect(w.find('h1').text()).toBe('Admin')
    expect(w.find('h2').text()).toBe('Users')
    expect(w.text()).toContain('users tab')

    await w.findAll('nav button').find((b) => b.text() === 'Teams')!.trigger('click')
    await flushPromises()
    expect(w.find('h2').text()).toBe('Teams')
    expect(w.text()).toContain('teams tab')
  })
})
