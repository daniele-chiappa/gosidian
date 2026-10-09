import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'

const route = { query: {} as Record<string, string> }
const router = { push: vi.fn(), replace: vi.fn() }
const auth = { user: { id: 'bob' } as { id: string } | null, login: vi.fn() }
let same = true

vi.mock('vue-router', () => ({ useRoute: () => route, useRouter: () => router }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => auth }))
vi.mock('@/api/totp', () => ({ getAuthConfig: vi.fn(async () => ({ totp: false, ldap: false })) }))
vi.mock('@/api/signup', () => ({
  signup: vi.fn(),
  isInvalidInvite: (e: unknown) => (e as { code?: string })?.code === 'invite',
}))
vi.mock('@/composables/useSessionReset', async (orig) => ({
  ...(await orig<typeof import('@/composables/useSessionReset')>()),
  noteSignedIn: () => same,
}))

import LoginView from '@/views/LoginView.vue'
import { signup } from '@/api/signup'
import { withoutWorkspace } from '@/composables/useSessionReset'

function mountView() {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
  return mount(LoginView, { global: { plugins: [i18n] } })
}

async function signIn(w: ReturnType<typeof mountView>) {
  await w.find("input[autocomplete='username']").setValue('bob')
  await w.find("input[type='password']").setValue('bob-password-1')
  await w.find('form').trigger('submit')
  await flushPromises()
}

describe('withoutWorkspace (BUG-116, S6-5)', () => {
  it('drops the windows and keeps the rest', () => {
    expect(withoutWorkspace('/?w=note%3AAlice%2Fsecret.md&f=note%3AAlice%2Fsecret.md')).toBe('/')
    expect(withoutWorkspace('/oauth/consent?req=abc&w=x')).toBe('/oauth/consent?req=abc')
    expect(withoutWorkspace('/oauth/consent?req=abc')).toBe('/oauth/consent?req=abc')
    expect(withoutWorkspace('/?w=x#top')).toBe('/#top')
    expect(withoutWorkspace('/')).toBe('/')
  })
})

describe('LoginView next= (BUG-116, S6-5)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    route.query = { next: '/?w=note%3AAlice%2Fsecret.md&f=note%3AAlice%2Fsecret.md' }
    auth.login.mockResolvedValue(undefined)
  })

  it("does not reopen another account's windows, nor an unknown one's", async () => {
    same = false
    await signIn(mountView())
    expect(router.push).toHaveBeenCalledWith('/')
  })

  it('keeps them for the same account', async () => {
    same = true
    await signIn(mountView())
    expect(router.push).toHaveBeenCalledWith('/?w=note%3AAlice%2Fsecret.md&f=note%3AAlice%2Fsecret.md')
  })
})

describe('LoginView sign-up from an invite (BUG-099)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    route.query = { invite: 'inv_abc' }
  })

  async function fill(w: ReturnType<typeof mountView>, pw: string, confirm: string) {
    await w.find("input[autocomplete='username']").setValue('carol')
    await w.find('[data-signup-password]').setValue(pw)
    await w.find('[data-signup-confirm]').setValue(confirm)
    await w.find('form').trigger('submit')
    await flushPromises()
  }

  it('creates the account, then asks to sign in with the name filled in', async () => {
    vi.mocked(signup).mockResolvedValue(undefined)
    const w = mountView()
    expect(w.find('[data-signup]').exists()).toBe(true)
    await fill(w, 'carol-password-1', 'carol-password-1')
    expect(signup).toHaveBeenCalledWith('carol', 'carol-password-1', 'inv_abc')
    expect(router.replace).toHaveBeenCalledWith({ path: '/login', query: {} })
    expect(w.find('[data-signup]').exists()).toBe(false)
    expect((w.find("input[autocomplete='username']").element as HTMLInputElement).value).toBe('carol')
    expect(w.find('[data-signup-done]').exists()).toBe(true)
  })

  it('checks the two passwords before asking the server', async () => {
    const w = mountView()
    await fill(w, 'carol-password-1', 'carol-password-2')
    expect(signup).not.toHaveBeenCalled()
    expect(w.text()).toContain('do not match')
    await fill(w, 'short', 'short')
    expect(signup).not.toHaveBeenCalled()
    expect(w.text()).toContain('at least 8 characters')
  })

  it('says an expired or used invite needs a new link', async () => {
    vi.mocked(signup).mockRejectedValue({ code: 'invite' })
    const w = mountView()
    await fill(w, 'carol-password-1', 'carol-password-1')
    expect(w.text()).toContain('expired or was already used')
    expect(w.find('form').exists()).toBe(false)
  })

  it('shows the reason the server gives otherwise', async () => {
    vi.mocked(signup).mockRejectedValue(new Error('username "carol" already exists'))
    const w = mountView()
    await fill(w, 'carol-password-1', 'carol-password-1')
    expect(w.text()).toContain('already exists')
    expect(w.find('[data-signup]').exists()).toBe(true)
  })
})
