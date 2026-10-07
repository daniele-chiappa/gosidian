/**
 * UI store — owns presentation state that is *not* business data:
 * theme preset and i18n locale. Persisted to localStorage so the
 * choice survives reloads.
 *
 * Locale picking flow on first boot (no `gosidian.ui` in localStorage):
 *   1. State is created with `locale: ''` (empty sentinel).
 *   2. hydrate() is called from App.vue setup. It fetches
 *      /api/v1/version (public, unauthenticated) for the operator's
 *      `i18n.default_lang` and `i18n.enabled_langs`; an empty locale
 *      takes the default.
 *   3. The user's later choice via SettingsView wins after that, as long
 *      as the operator keeps that language enabled: a stored locale that
 *      is no longer in `enabled_langs` falls back to the default.
 *
 * The `''` empty sentinel is the only way to tell "first run" apart
 * from "user picked English" — pinia-plugin-persistedstate restores
 * whatever was on disk before our action runs, and a hardcoded
 * default would always look identical to the persisted value.
 */
import { defineStore } from 'pinia'
import { i18n } from '@/locales'
import { getVersion, type VersionInfo } from '@/api/version'

export type ThemePreset =
  | 'catppuccin-mocha'
  | 'catppuccin-latte'
  | 'tokyo-night'
  | 'solarized-light'
  | 'custom'

export type LocaleCode = 'it' | 'en' | 'es' | 'fr' | 'de'

/** plancia layout: the niri-style strip or a tab bar. Persisted like the rest. */
export type PlanciaViewMode = 'strip' | 'tabs'

/** Graph window renderer: 2D canvas (default) or 3D WebGL. Global default;
 *  each graph window starts from it and the toggle updates it. */
export type GraphRenderMode = '2d' | '3d'

interface UIState {
  preset: ThemePreset
  locale: LocaleCode | ''
  planciaViewMode: PlanciaViewMode
  graphMode: GraphRenderMode
  /** The languages the operator enables (`i18n.enabled_langs`), read from
   *  /api/v1/version at every hydrate; not persisted. */
  enabledLocales: LocaleCode[]
}

const VALID_PRESETS: ThemePreset[] = [
  'catppuccin-mocha',
  'catppuccin-latte',
  'tokyo-night',
  'solarized-light',
  'custom',
]
const VALID_LOCALES: LocaleCode[] = ['it', 'en', 'es', 'fr', 'de']

function isLocale(s: string): s is LocaleCode {
  return (VALID_LOCALES as string[]).includes(s)
}

export const useUIStore = defineStore('ui', {
  state: (): UIState => ({
    preset: 'catppuccin-mocha',
    locale: '',
    planciaViewMode: 'strip',
    graphMode: '2d',
    enabledLocales: [...VALID_LOCALES],
  }),
  actions: {
    setPreset(preset: ThemePreset) {
      if (!VALID_PRESETS.includes(preset)) return
      this.preset = preset
      this.applyPreset()
    },
    setPlanciaViewMode(mode: PlanciaViewMode) {
      if (mode !== 'strip' && mode !== 'tabs') return
      this.planciaViewMode = mode
    },
    setGraphMode(mode: GraphRenderMode) {
      if (mode !== '2d' && mode !== '3d') return
      this.graphMode = mode
    },
    setLocale(locale: LocaleCode) {
      if (!VALID_LOCALES.includes(locale) || !this.enabledLocales.includes(locale)) return
      this.locale = locale
      this.applyLocale()
    },
    applyPreset() {
      document.documentElement.dataset.preset = this.preset
    },
    applyLocale() {
      // Empty (first boot, server fetch not done yet) → leave
      // vue-i18n on its `fallbackLocale` ('en').
      const lang = this.locale || 'en'
      const global = i18n.global
      ;(global.locale as unknown as { value: string }).value = lang
      document.documentElement.lang = lang
    },
    async hydrate() {
      // The stored choice applies at once (no first-paint flash); the
      // server's languages may still correct it.
      this.applyPreset()
      if (this.locale) this.applyLocale()
      await this.refreshLocales()
    },
    /** Reads the operator's default and enabled languages: a locale that is
     *  empty (first boot) or no longer enabled takes the default. Called at
     *  hydrate and after the Settings save them. Without an answer every
     *  language stays offered and the stored one is kept. */
    async refreshLocales() {
      let v: VersionInfo | null = null
      try {
        v = await getVersion()
      } catch {
        v = null
      }
      const enabled = (v?.enabled_langs ?? []).filter(isLocale)
      this.enabledLocales = enabled.length ? enabled : [...VALID_LOCALES]
      if (!this.locale || !this.enabledLocales.includes(this.locale)) {
        const d = v?.default_lang
        if (d && isLocale(d) && this.enabledLocales.includes(d)) this.locale = d
        else this.locale = this.enabledLocales.includes('en') ? 'en' : (this.enabledLocales[0] ?? 'en')
      }
      this.applyLocale()
    },
  },
  persist: { key: 'gosidian.ui', paths: ['preset', 'locale', 'planciaViewMode', 'graphMode'] },
})
