import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import type { Settings } from '@/api/settings'

vi.mock('@/api/settings', () => ({ getSettings: vi.fn(), updateSettings: vi.fn() }))
vi.mock('@/api/totp', () => ({ disenrollTOTP: vi.fn(), regenerateRecoveryCodes: vi.fn() }))
vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    isOwner: true,
    isAnonymous: false,
    user: { username: 'owner', totp_enrolled: false, auth_source: 'local' },
    setEnrolled: vi.fn(),
    setRecoveryCodesRemaining: vi.fn(),
  }),
}))
vi.mock('@/stores/ui', () => ({
  useUIStore: () => ({
    preset: 'catppuccin-mocha',
    locale: 'en',
    enabledLocales: ['en', 'it'],
    setPreset: vi.fn(),
    setLocale: vi.fn(),
    refreshLocales: vi.fn(),
  }),
}))

import { getSettings, updateSettings } from '@/api/settings'
import SettingsView from '@/views/SettingsView.vue'

const settings: Settings = {
  git: { enabled: true, remote: 'https://git.example/vault.git', branch: 'main', author_name: '', author_email: '', debounce_ms: 30000, push: true, token_env: '' },
  trash: { enabled: true, retention_ms: 0 },
  i18n: { default_lang: 'en', enabled_langs: ['en', 'it'] },
  mcp: { write_per_minute: 300, max_note_bytes: 1 << 20 },
  totp_mode: 'optional',
  default_visibility: 'private',
  personal_projects: true,
  anchors_enabled: false,
  globals_enabled: false,
}

function mountView() {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
  return mount(SettingsView, {
    global: {
      plugins: [i18n],
      stubs: { PasswordChange: true, TotpEnroll: true, MyTokens: true, RecoveryCodes: true },
    },
  })
}

const policy = (w: ReturnType<typeof mountView>) =>
  w.findAll('select').find((s) => s.find('option[value="required"]').exists())

describe('SettingsView owner controls (BUG-109)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(updateSettings).mockImplementation(async (body) => ({ ...settings, ...body }) as Settings)
  })

  it('shows no control that saves before the settings have loaded', async () => {
    vi.mocked(getSettings).mockRejectedValue(new Error('down'))
    const w = mountView()
    await flushPromises()
    expect(policy(w)).toBeUndefined()
    expect(w.find('input[type="checkbox"]').exists()).toBe(false)
    expect(updateSettings).not.toHaveBeenCalled()
  })

  it('saves the changed field alone, and keeps what is typed in the form', async () => {
    vi.mocked(getSettings).mockResolvedValue(structuredClone(settings))
    const w = mountView()
    await flushPromises()
    const remoteInput = w.findAll('input').find((i) => (i.element as HTMLInputElement).value === settings.git.remote)!
    await remoteInput.setValue('https://half.typed')
    await policy(w)!.setValue('required')
    await flushPromises()
    expect(updateSettings).toHaveBeenCalledTimes(1)
    expect(vi.mocked(updateSettings).mock.calls[0]![0]).toEqual({ totp_mode: 'required' })
    expect((remoteInput.element as HTMLInputElement).value).toBe('https://half.typed')
  })

  it('disables the toggles while a save is on its way', async () => {
    vi.mocked(getSettings).mockResolvedValue(structuredClone(settings))
    let reply: (s: Settings) => void = () => {}
    vi.mocked(updateSettings).mockReturnValue(new Promise((r) => (reply = r)))
    const w = mountView()
    await flushPromises()
    await policy(w)!.setValue('required')
    expect((policy(w)!.element as HTMLSelectElement).disabled).toBe(true)
    reply({ ...settings, totp_mode: 'required' })
    await flushPromises()
    expect((policy(w)!.element as HTMLSelectElement).disabled).toBe(false)
  })
})
