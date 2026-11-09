import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createApp, nextTick } from 'vue'
import { createPinia, setActivePinia } from 'pinia'
import piniaPersist from 'pinia-plugin-persistedstate'
import { useUIStore } from '@/stores/ui'
import { i18n, isLoaded, loadLocale } from '@/locales'
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
    ui.locale = 'it'
    ui.hydrate()
    expect(document.documentElement.dataset.preset).toBe('solarized-light')
    expect(document.documentElement.lang).toBe('it')
  })

  describe('languages loaded when chosen (IMP-220, F-20)', () => {
    const msg = () => i18n.global.t('common.save')

    it('Italian and English are in the first load; German, Spanish and French are not', () => {
      expect(isLoaded('it')).toBe(true)
      expect(isLoaded('en')).toBe(true)
      expect(['de', 'es', 'fr'].filter(isLoaded)).toEqual([])
    })

    it("switches to German once its catalogue is in, the page's lang with it", async () => {
      const ui = useUIStore()
      ui.setLocale('en')
      ui.setLocale('de')
      // Until the catalogue is in, the page stays as it was, lang and all.
      expect(document.documentElement.lang).toBe('en')
      // A chunk to import: in a CI busy with the whole suite it took over the
      // second waitFor gives by default, and its late arrival broke the next test.
      await vi.waitFor(() => expect(document.documentElement.lang).toBe('de'), { timeout: 10_000 })
      expect(msg()).toBe('Speichern')
      expect(isLoaded('de')).toBe(true)
    })

    it('a later choice wins over one still loading', async () => {
      const ui = useUIStore()
      ui.setLocale('fr')
      ui.setLocale('it')
      await loadLocale('fr')
      await nextTick()
      expect(ui.locale).toBe('it')
      expect(document.documentElement.lang).toBe('it')
      expect((i18n.global.locale as unknown as { value: string }).value).toBe('it')
    })

    it('a stored language is in before hydrate ends', async () => {
      const ui = useUIStore()
      ui.locale = 'es'
      await ui.hydrate()
      expect(document.documentElement.lang).toBe('es')
      expect(msg()).toBe('Guardar')
    })
  })

  describe('view of the plancia', () => {
    const persisted = () => {
      // Plugins apply once pinia is installed in an app.
      const pinia = createPinia()
      pinia.use(piniaPersist)
      createApp({}).use(pinia)
      setActivePinia(pinia)
      return useUIStore()
    }
    beforeEach(() => window.localStorage.removeItem('gosidian.ui'))

    it('starts on the tabs', () => {
      expect(useUIStore().planciaViewMode).toBe('tabs')
    })

    it('a strip stored before the choice was told apart from the default gives way to the tabs', async () => {
      window.localStorage.setItem('gosidian.ui', JSON.stringify({ preset: 'tokyo-night', planciaViewMode: 'strip' }))
      const ui = persisted()
      expect(ui.planciaViewMode).toBe('strip')
      await ui.hydrate()
      expect(ui.planciaViewMode).toBe('tabs')
      expect(ui.preset).toBe('tokyo-night')
    })

    it('a strip the user picks stays across reloads', async () => {
      const ui = persisted()
      await ui.hydrate()
      ui.setPlanciaViewMode('strip')
      await nextTick()
      const again = persisted()
      await again.hydrate()
      expect(again.planciaViewMode).toBe('strip')
    })

    it('a sidebar closed on a wide screen stays closed across reloads (IMP-229)', async () => {
      const ui = persisted()
      expect(ui.sidebarClosed).toBe(false)
      ui.setSidebarClosed(true)
      await nextTick()
      expect(persisted().sidebarClosed).toBe(true)
    })
  })

  it('reads whether the server runs pipelines (IMP-166)', async () => {
    mockedGetVersion.mockResolvedValue({ version: 'test', api: 'v1', pipelines_enabled: true })
    const ui = useUIStore()
    await ui.hydrate()
    expect(ui.pipelinesEnabled).toBe(true)
    mockedGetVersion.mockResolvedValue({ version: 'test', api: 'v1' })
    await ui.refreshLocales()
    expect(ui.pipelinesEnabled).toBe(false)
  })

  it("reads the server's caps of a graph, the defaults without them (IMP-190)", async () => {
    mockedGetVersion.mockResolvedValue({ version: 'test', api: 'v1', graph_default_nodes: 3000, graph_max_nodes: 20000 })
    const ui = useUIStore()
    expect([ui.graphDefaultNodes, ui.graphMaxNodes]).toEqual([2000, 5000])
    await ui.hydrate()
    expect([ui.graphDefaultNodes, ui.graphMaxNodes]).toEqual([3000, 20000])
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
