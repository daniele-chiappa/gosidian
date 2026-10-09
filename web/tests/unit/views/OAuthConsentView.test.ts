import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import type { OAuthConsent } from '@/api/oauth'

vi.mock('vue-router', () => ({ useRoute: () => ({ query: { req: 'r1' } }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { username: 'owner' } }) }))
vi.mock('@/api/oauth', () => ({
  getOAuthRequest: vi.fn(),
  approveOAuthRequest: vi.fn(),
  denyOAuthRequest: vi.fn(),
}))

import { approveOAuthRequest, getOAuthRequest } from '@/api/oauth'
import OAuthConsentView from '@/views/OAuthConsentView.vue'

const consent = (over: Partial<OAuthConsent>): OAuthConsent => ({
  request_id: 'r1',
  client_id: 'c1',
  client_name: 'Claude',
  client_cimd: false,
  redirect_uri: 'https://claude.ai/cb',
  redirect_host: 'claude.ai',
  loopback_only: false,
  scopes: ['read'],
  expires_at: new Date(Date.now() + 600_000).toISOString(),
  projects: [{ name: 'Alpha', public: false, note_count: 3 }],
  can_write: true,
  is_owner: true,
  ...over,
})

async function mountWith(c: OAuthConsent) {
  vi.mocked(getOAuthRequest).mockResolvedValue(c)
  vi.mocked(approveOAuthRequest).mockResolvedValue('https://claude.ai/cb?code=x')
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
  const w = mount(OAuthConsentView, { global: { plugins: [i18n] } })
  await flushPromises()
  return w
}

const approveButton = (w: Awaited<ReturnType<typeof mountWith>>) =>
  w.findAll('button').find((b) => b.text() === 'Allow')!

describe('OAuthConsentView (BUG-116, S6-4)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    window.location.assign = vi.fn()
  })

  it('lets an owner with one project grant just that project', async () => {
    const w = await mountWith(consent({}))
    const all = w.find('[data-all-projects] input')
    expect(all.exists()).toBe(true)
    await all.setValue(false)
    await approveButton(w).trigger('click')
    await flushPromises()
    expect(approveOAuthRequest).toHaveBeenCalledWith('r1', { projects: ['Alpha'], scopes: ['read'] })
  })

  it('gives the owner an unscoped grant only from the "all projects" line', async () => {
    const w = await mountWith(consent({}))
    await approveButton(w).trigger('click')
    await flushPromises()
    expect(approveOAuthRequest).toHaveBeenCalledWith('r1', { projects: [], scopes: ['read'] })
  })

  it('never gives a member an unscoped grant', async () => {
    const w = await mountWith(
      consent({
        is_owner: false,
        projects: [
          { name: 'Alpha', public: false, note_count: 1 },
          { name: 'Beta', public: false, note_count: 1 },
        ],
      }),
    )
    expect(w.find('[data-all-projects]').exists()).toBe(false)
    await approveButton(w).trigger('click')
    await flushPromises()
    expect(approveOAuthRequest).toHaveBeenCalledWith('r1', { projects: ['Alpha', 'Beta'], scopes: ['read'] })
  })
})
