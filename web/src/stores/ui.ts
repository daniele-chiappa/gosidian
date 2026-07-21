/**
 * UI store — owns presentation state that is *not* business data:
 * theme preset and i18n locale. Persisted to localStorage so the
 * choice survives reloads.
 *
 * Locale picking flow on first boot (no `gosidian.ui` in localStorage):
 *   1. State is created with `locale: ''` (empty sentinel).
 *   2. hydrate() is called from App.vue setup. If the locale is
 *      empty, fetch /api/v1/version (public, unauthenticated) to
 *      read the operator's `i18n.default_lang`. Apply it.
 *   3. The user's later choice via SettingsView wins forever after.
 *
 * The `''` empty sentinel is the only way to tell "first run" apart
 * from "user picked English" — pinia-plugin-persistedstate restores
 * whatever was on disk before our action runs, and a hardcoded
 * default would always look identical to the persisted value.
 */
import { defineStore } from 'pinia'
import { i18n } from '@/locales'
import { getVersion } from '@/api/version'

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
      if (!VALID_LOCALES.includes(locale)) return
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
      // First boot: pick the operator's configured default lang.
      // Once the user explicitly chooses via SettingsView, the
      // persisted value wins on subsequent loads.
      if (!this.locale) {
        try {
          const v = await getVersion()
          if (v.default_lang && isLocale(v.default_lang)) {
            this.locale = v.default_lang
          } else {
            this.locale = 'en'
          }
        } catch {
          this.locale = 'en'
        }
      }
      this.applyPreset()
      this.applyLocale()
    },
  },
  persist: { key: 'gosidian.ui' },
})
