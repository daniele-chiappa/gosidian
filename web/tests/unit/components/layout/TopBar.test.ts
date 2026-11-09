import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'

const windows = vi.hoisted(() => ({ open: vi.fn((): string => '') }))
vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => windows,
  usePlanciaFocus: () => ({ focusWindow: vi.fn(async () => true) }),
}))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn() }) }))

import TopBar from '@/components/layout/TopBar.vue'
import { roleLabel } from '@/api/access'
import { useAuthStore } from '@/stores/auth'

const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })

describe('TopBar, the Search button (IMP-218, F-06)', () => {
  it('opens Search and shows no shortcut: Ctrl+K opens the palette, not Search', async () => {
    const w = mount(TopBar, {
      global: { plugins: [createPinia(), i18n()], stubs: { InsightsBadge: true, UploadsBadge: true } },
    })
    // After the sidebar's button.
    const button = w.findAll('button')[1]!
    expect(button.text()).toBe('Search')
    expect(button.find('kbd').exists()).toBe(false)
    await button.trigger('click')
    expect(windows.open).toHaveBeenCalledWith(expect.objectContaining({ type: 'search' }))
  })
})

describe('TopBar on a narrow screen (IMP-221, F-15)', () => {
  // Signed in as claude, a member.
  const mountBar = (props: Record<string, unknown>) => {
    const pinia = createPinia()
    setActivePinia(pinia)
    useAuthStore().user = { id: 'u1', username: 'claude', role: 'member' } as never
    return mount(TopBar, {
      props,
      global: { plugins: [pinia, i18n()], stubs: { InsightsBadge: true, UploadsBadge: true } },
    })
  }

  it("has the sidebar's button, which says whether the drawer is open, and no choice of view", async () => {
    const w = mountBar({ narrow: true, sidebarOpen: false })
    const button = w.find('[data-sidebar-toggle]')
    expect(button.attributes()).toMatchObject({ 'aria-label': 'Sidebar', 'aria-expanded': 'false' })
    await button.trigger('click')
    expect(w.emitted('toggle-sidebar')).toHaveLength(1)
    await w.setProps({ sidebarOpen: true })
    expect(button.attributes('aria-expanded')).toBe('true')
    expect(w.find('button[aria-label="Strip"]').exists()).toBe(false)
  })

  it("puts the account's name and role in a menu, with Sign out, and leaves them off the bar", async () => {
    const w = mountBar({ narrow: true })
    expect(w.text()).not.toContain('Sign out')
    expect(w.text()).not.toContain(roleLabel('member'))
    const menu = w.find('[data-overflow-button]')
    expect(menu.attributes('aria-label')).toBe(`Account menu: claude, ${roleLabel('member')}`)
    await menu.trigger('click')
    expect(w.find('[data-overflow-item="logout"]').text()).toBe('Sign out')
  })

  it('keeps the wide bar as it is, with the choice of view and the sidebar\'s button, which says Ctrl+\\ (IMP-229)', () => {
    const w = mountBar({ sidebarOpen: true })
    const button = w.find('[data-sidebar-toggle]')
    expect(button.attributes()).toMatchObject({
      'aria-label': 'Sidebar',
      'aria-expanded': 'true',
      'aria-keyshortcuts': 'Control+Backslash',
      title: 'Sidebar (Ctrl+\\)',
    })
    expect(w.find('button[aria-label="Strip"]').exists()).toBe(true)
    expect(w.find('[data-overflow-button]').exists()).toBe(false)
    expect(w.text()).toContain('Sign out')
  })
})
