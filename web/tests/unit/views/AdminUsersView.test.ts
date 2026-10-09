import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import type { AdminUser } from '@/api/admin'
import type { AccessView } from '@/api/access'

vi.mock('@/api/admin', () => ({
  listUsers: vi.fn(),
  disableUser: vi.fn(),
  updateUserRole: vi.fn(),
  updateUserTOTPPolicy: vi.fn(),
  resetUserTOTP: vi.fn(),
  createUser: vi.fn(),
  updateUserFlags: vi.fn(),
  createPersonalProject: vi.fn(),
}))
vi.mock('@/api/password', () => ({ resetUserPassword: vi.fn() }))
vi.mock('@/composables/useConfirm', () => ({ confirmAction: vi.fn() }))
vi.mock('@/api/access', async (orig) => ({
  ...(await orig<typeof import('@/api/access')>()),
  getUserAccess: vi.fn(),
}))

import { listUsers, updateUserRole } from '@/api/admin'
import { getUserAccess } from '@/api/access'
import { confirmAction } from '@/composables/useConfirm'
import AdminUsersView from '@/views/admin/AdminUsersView.vue'

const user = (id: string, role = 'member'): AdminUser => ({
  id,
  username: id,
  role,
  created_at: '2026-10-01T00:00:00Z',
  restricted: false,
  can_create_projects: true,
  projects_readable: 1,
  projects_writable: 1,
})
const access = (project: string): AccessView => ({
  user_id: 'x',
  role: 'member',
  restricted: false,
  can_create_projects: true,
  projects: [{ name: project, visibility: 'internal', level: 'read', via: ['internal'] }],
})

function mountView() {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
  return mount(AdminUsersView, { global: { plugins: [i18n] } })
}

const viewButton = (w: ReturnType<typeof mountView>, i: number) =>
  w.findAll('button').filter((b) => ['View', 'Hide'].includes(b.text()))[i]!

describe('AdminUsersView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(listUsers).mockResolvedValue([user('alice'), user('bob')])
  })

  it("shows the access of the account last opened, not the slower one's (BUG-116, S6-8)", async () => {
    let releaseAlice: (v: AccessView) => void = () => {}
    vi.mocked(getUserAccess).mockImplementation((id) =>
      id === 'alice' ? new Promise((r) => (releaseAlice = r)) : Promise.resolve(access('BobOnly')),
    )
    const w = mountView()
    await flushPromises()
    await viewButton(w, 0).trigger('click') // alice, slow
    await viewButton(w, 1).trigger('click') // bob
    await flushPromises()
    releaseAlice(access('AliceOnly'))
    await flushPromises()
    expect(w.text()).toContain('BobOnly')
    expect(w.text()).not.toContain('AliceOnly')
  })

  it('refreshes the open preview after a change of role', async () => {
    vi.mocked(getUserAccess).mockResolvedValueOnce(access('Before')).mockResolvedValueOnce(access('After'))
    vi.mocked(updateUserRole).mockResolvedValue(undefined as never)
    vi.mocked(confirmAction).mockResolvedValue(true)
    const w = mountView()
    await flushPromises()
    await viewButton(w, 0).trigger('click')
    await flushPromises()
    expect(w.text()).toContain('Before')
    await w.findAll('select')[0]!.setValue('guest')
    await flushPromises()
    expect(w.text()).toContain('After')
  })

  it('asks before making an account read-only, which revokes its tokens (BUG-116, S6-9)', async () => {
    const confirm = vi.mocked(confirmAction).mockResolvedValue(false)
    const w = mountView()
    await flushPromises()
    const select = w.findAll('select')[0]!
    await select.setValue('guest')
    await flushPromises()
    expect(confirm).toHaveBeenCalledTimes(1)
    expect(String((confirm.mock.calls[0] as unknown[])[0])).toContain('MCP tokens are revoked')
    expect(updateUserRole).not.toHaveBeenCalled()
    expect((select.element as HTMLSelectElement).value).toBe('member')

    confirm.mockResolvedValue(true)
    await select.setValue('guest')
    await flushPromises()
    expect(updateUserRole).toHaveBeenCalledWith('alice', 'guest')
  })
})
