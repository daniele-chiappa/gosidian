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
import { i18n, isLoaded, loadLocale } from '@/locales'
import { getVersion, type VersionInfo } from '@/api/version'

export type ThemePreset =
  | 'catppuccin-mocha'
  | 'catppuccin-latte'
  | 'tokyo-night'
  | 'solarized-light'
  | 'custom'

export type LocaleCode = 'it' | 'en' | 'es' | 'fr' | 'de'

/** plancia layout: a tab bar (the default) or the niri-style strip.
 *  Persisted like the rest once the user picks one. */
export type PlanciaViewMode = 'strip' | 'tabs'

/** Graph window renderer: 2D canvas (default) or 3D WebGL. Global default;
 *  each graph window starts from it and the toggle updates it. */
export type GraphRenderMode = '2d' | '3d'

interface UIState {
  preset: ThemePreset
  locale: LocaleCode | ''
  planciaViewMode: PlanciaViewMode
  /** The user picked the view with the bar's buttons. The default was the
   *  strip before the tabs, and a stored strip without this mark is that
   *  old default, which hydrate replaces with the tabs. */
  planciaViewChosen: boolean
  /** The user closed the sidebar on a wide screen (IMP-229); the drawer of a
   *  narrow screen leaves it as it is. */
  sidebarClosed: boolean
  graphMode: GraphRenderMode
  /** The languages the operator enables (`i18n.enabled_langs`), read from
   *  /api/v1/version at every hydrate; not persisted. */
  enabledLocales: LocaleCode[]
  /** The server runs pipelines (VersionInfo.pipelines_enabled), read with
   *  the languages; not persisted. */
  pipelinesEnabled: boolean
  /** The server's caps of a graph (VersionInfo.graph_default_nodes,
   *  graph_max_nodes), which the Graph window names; not persisted. */
  graphDefaultNodes: number
  graphMaxNodes: number
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
    planciaViewMode: 'tabs',
    planciaViewChosen: false,
    sidebarClosed: false,
    graphMode: '2d',
    enabledLocales: [...VALID_LOCALES],
    pipelinesEnabled: false,
    graphDefaultNodes: 2000,
    graphMaxNodes: 5000,
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
      this.planciaViewChosen = true
    },
    setSidebarClosed(closed: boolean) {
      this.sidebarClosed = closed
    },
    setGraphMode(mode: GraphRenderMode) {
      if (mode !== '2d' && mode !== '3d') return
      this.graphMode = mode
    },
    setLocale(locale: LocaleCode) {
      if (!VALID_LOCALES.includes(locale) || !this.enabledLocales.includes(locale)) return
      this.locale = locale
      void this.applyLocale()
    },
    applyPreset() {
      document.documentElement.dataset.preset = this.preset
    },
    /** Switches vue-i18n and the page's lang once the language's catalogue
     *  is in (IMP-220, F-20): until then the page stays in the language it
     *  was, never German words under lang="en". */
    async applyLocale() {
      // Empty (first boot, server fetch not done yet) → leave
      // vue-i18n on its `fallbackLocale` ('en').
      const lang = this.locale || 'en'
      if (!isLoaded(lang)) {
        await loadLocale(lang)
        // A later choice came in while this one loaded: it applies itself.
        if ((this.locale || 'en') !== lang) return
      }
      const global = i18n.global
      ;(global.locale as unknown as { value: string }).value = lang
      document.documentElement.lang = lang
    },
    async hydrate() {
      // The stored choice applies at once (no first-paint flash: main.ts
      // loads its catalogue before mounting); the server's languages may
      // still correct it.
      this.applyPreset()
      if (!this.planciaViewChosen) this.planciaViewMode = 'tabs'
      if (this.locale) void this.applyLocale()
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
      this.pipelinesEnabled = Boolean(v?.pipelines_enabled)
      if (v?.graph_default_nodes && v.graph_default_nodes > 0) this.graphDefaultNodes = v.graph_default_nodes
      if (v?.graph_max_nodes && v.graph_max_nodes > 0) this.graphMaxNodes = v.graph_max_nodes
      const enabled = (v?.enabled_langs ?? []).filter(isLocale)
      this.enabledLocales = enabled.length ? enabled : [...VALID_LOCALES]
      if (!this.locale || !this.enabledLocales.includes(this.locale)) {
        const d = v?.default_lang
        if (d && isLocale(d) && this.enabledLocales.includes(d)) this.locale = d
        else this.locale = this.enabledLocales.includes('en') ? 'en' : (this.enabledLocales[0] ?? 'en')
      }
      await this.applyLocale()
    },
  },
  persist: { key: 'gosidian.ui', paths: ['preset', 'locale', 'planciaViewMode', 'planciaViewChosen', 'sidebarClosed', 'graphMode'] },
})
