import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'

vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isOwner: true }) }))
vi.mock('@/api/admin', () => ({ exportVault: vi.fn() }))
// The tabs load their data on mount: here they only draw their name.
vi.mock('@/views/admin/AdminUsersView.vue', () => ({ __esModule: true, default: { render: () => 'users tab' } }))
vi.mock('@/views/admin/AdminTeamsView.vue', () => ({ __esModule: true, default: { render: () => 'teams tab' } }))
// The audit draws the filter it gets, and a click changes it.
vi.mock('@/views/admin/AdminAuditView.vue', async () => {
  const { defineComponent, h } = await import('vue')
  return {
    __esModule: true,
    default: defineComponent({
      props: { filter: { type: String, default: '' } },
      emits: ['update:filter'],
      setup: (props, { emit }) => () =>
        h('button', { 'data-fake-audit': '', onClick: () => emit('update:filter', 'actor=claude') }, `audit ${props.filter}`),
    }),
  }
})
const { store, win } = vi.hoisted(() => {
  const win = { id: 'w1', type: 'admin', key: 'admin', title: '', props: {} as Record<string, unknown> }
  // As plancia's, identify merges the props it is given into the window's.
  const identify = (_id: string, _key: string, props: Record<string, unknown>) => Object.assign(win.props, props)
  return { win, store: { windows: [win], identify: vi.fn(identify), setTitle: vi.fn() } }
})
vi.mock('plancia', async (orig) => ({ ...(await orig<typeof import('plancia')>()), useWindowsStore: () => store }))

import type { WindowInstance } from 'plancia'
import { codec } from '@/composables/planciaKey'
import AdminLayout from '@/views/admin/AdminLayout.vue'

/** The window's token in the address. */
const address = () => decodeURIComponent(codec.encode(win as unknown as WindowInstance) ?? '')

const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
const tab = (w: ReturnType<typeof mount>, name: string) => w.findAll('nav button').find((b) => b.text() === name)!

describe('AdminLayout headings (IMP-217, F-35)', () => {
  it('names the tab it shows in an h2, under the window h1', async () => {
    const w = mount(AdminLayout, { global: { plugins: [i18n()] } })
    await flushPromises()
    expect(w.find('h1').text()).toBe('Admin')
    expect(w.find('h2').text()).toBe('Users')
    expect(w.text()).toContain('users tab')

    await tab(w, 'Teams').trigger('click')
    await flushPromises()
    expect(w.find('h2').text()).toBe('Teams')
    expect(w.text()).toContain('teams tab')
  })
})

describe('AdminLayout, the tab shown (IMP-224, F-36 and F-30)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    win.props = {}
  })

  it("says which tab is the current one, and puts it in the window's address and title", async () => {
    const w = mount(AdminLayout, { props: { section: 'teams' }, global: { plugins: [i18n()] } })
    await flushPromises()
    expect(w.findAll('nav button[aria-current="page"]').map((b) => b.text())).toEqual(['Teams'])

    await tab(w, 'Users').trigger('click')
    expect(w.findAll('nav button[aria-current="page"]').map((b) => b.text())).toEqual(['Users'])
    expect(address()).toBe('admin:users')
    expect(store.setTitle).toHaveBeenLastCalledWith('w1', 'Admin · Users')
  })

  it("keeps the audit's filter in the address while the audit shows, and while another tab does", async () => {
    const w = mount(AdminLayout, { props: { section: 'audit', audit: 'page=2' }, global: { plugins: [i18n()] } })
    await flushPromises()
    expect(w.text()).toContain('audit page=2')

    await w.find('[data-fake-audit]').trigger('click')
    expect(address()).toBe('admin:audit?actor=claude')
    await tab(w, 'Users').trigger('click')
    expect(address()).toBe('admin:users')
    await tab(w, 'Audit').trigger('click')
    await flushPromises()
    expect(w.text()).toContain('audit actor=claude')
    expect(address()).toBe('admin:audit?actor=claude')
  })
})
