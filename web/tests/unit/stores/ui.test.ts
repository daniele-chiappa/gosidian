import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import piniaPersist from 'pinia-plugin-persistedstate'
import { useUIStore } from '@/stores/ui'
import { i18n } from '@/locales'
import { getVersion } from '@/api/version'

vi.mock('@/api/version', () => ({ getVersion: vi.fn() }))
const mockedGetVersion = vi.mocked(getVersion)

describe('ui store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    mockedGetVersion.mockReset()
    mockedGetVersion.mockResolvedValue({ version: 'test', api: 'v1', default_lang: 'en' })
  })

  it('defaults to catppuccin-mocha + empty locale (server-default fetched on hydrate)', () => {
    const ui = useUIStore()
    expect(ui.preset).toBe('catppuccin-mocha')
    // Empty sentinel — hydrate() fetches /api/v1/version on first
    // boot to pick the operator's i18n.default_lang. The user's
    // explicit choice via setLocale wins forever after.
    expect(ui.locale).toBe('')
  })

  it('setPreset writes data-preset on documentElement', () => {
    const ui = useUIStore()
    ui.setPreset('tokyo-night')
    expect(document.documentElement.dataset.preset).toBe('tokyo-night')
  })

  it('setPreset rejects invalid values silently', () => {
    const ui = useUIStore()
    ui.setPreset('not-a-preset' as never)
    expect(ui.preset).toBe('catppuccin-mocha')
  })

  it('setLocale flips i18n.global.locale and <html lang>', () => {
    const ui = useUIStore()
    ui.setLocale('en')
    expect(ui.locale).toBe('en')
    expect(document.documentElement.lang).toBe('en')
    expect((i18n.global.locale as unknown as { value: string }).value).toBe('en')
  })

  it('hydrate applies both preset and locale at once (avoids first-paint flash)', () => {
    const ui = useUIStore()
    ui.preset = 'solarized-light'
    ui.locale = 'fr'
    ui.hydrate()
    expect(document.documentElement.dataset.preset).toBe('solarized-light')
    expect(document.documentElement.lang).toBe('fr')
  })

  describe('enabled languages (IMP-148)', () => {
    it('first boot takes the default and offers only the enabled languages', async () => {
      mockedGetVersion.mockResolvedValue({ version: 'test', api: 'v1', default_lang: 'it', enabled_langs: ['it', 'en'] })
      const ui = useUIStore()
      await ui.hydrate()
      expect(ui.locale).toBe('it')
      expect(ui.enabledLocales).toEqual(['it', 'en'])
    })

    it('a stored language the operator disabled falls back to the default', async () => {
      mockedGetVersion.mockResolvedValue({ version: 'test', api: 'v1', default_lang: 'it', enabled_langs: ['it', 'en'] })
      const ui = useUIStore()
      ui.locale = 'fr'
      await ui.hydrate()
      expect(ui.locale).toBe('it')
      expect(document.documentElement.lang).toBe('it')
    })

    it('a stored language still enabled is kept', async () => {
      mockedGetVersion.mockResolvedValue({ version: 'test', api: 'v1', default_lang: 'it', enabled_langs: ['it', 'en'] })
      const ui = useUIStore()
      ui.locale = 'en'
      await ui.hydrate()
      expect(ui.locale).toBe('en')
    })

    it('setLocale refuses a disabled language', async () => {
      mockedGetVersion.mockResolvedValue({ version: 'test', api: 'v1', default_lang: 'en', enabled_langs: ['it', 'en'] })
      const ui = useUIStore()
      await ui.hydrate()
      ui.setLocale('de')
      expect(ui.locale).toBe('en')
    })

    it('an older server or no answer keeps every language and the stored choice', async () => {
      const ui = useUIStore()
      ui.locale = 'de'
      await ui.hydrate() // default mock: no enabled_langs
      expect(ui.enabledLocales).toEqual(['it', 'en', 'es', 'fr', 'de'])
      expect(ui.locale).toBe('de')

      mockedGetVersion.mockRejectedValue(new Error('offline'))
      await ui.refreshLocales()
      expect(ui.enabledLocales).toHaveLength(5)
      expect(ui.locale).toBe('de')
    })

    it('the enabled list is not persisted', async () => {
      // Plugins apply once pinia is installed in an app.
      const pinia = createPinia()
      pinia.use(piniaPersist)
      createApp({}).use(pinia)
      setActivePinia(pinia)
      mockedGetVersion.mockResolvedValue({ version: 'test', api: 'v1', default_lang: 'en', enabled_langs: ['en'] })
      const ui = useUIStore()
      await ui.hydrate()
      ui.setPreset('tokyo-night')
      await nextTick()
      const stored = JSON.parse(window.localStorage.getItem('gosidian.ui') ?? '{}')
      expect(stored.preset).toBe('tokyo-night')
      expect(stored.enabledLocales).toBeUndefined()
    })
  })
})
