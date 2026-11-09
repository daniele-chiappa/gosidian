/// <reference types="vite/client" />
import { createI18n } from 'vue-i18n'

// Catalogs live in /internal/i18n/catalogs/ as the single source of
// truth shared with the Go side. Italian and English ship in the first
// load: English is every language's fallback, Italian the one most
// readers use. German, Spanish and French are whole too (IMP-220, F-20),
// each a chunk of its own, loaded before the web UI switches to it.
import itUI from '@catalogs/ui.it.json'
import enUI from '@catalogs/ui.en.json'

export const i18n = createI18n({
  legacy: false,
  // Initial locale matches the API/CLI default (`en`); the UI store
  // calls hydrate() in App.vue setup which fetches the operator's
  // configured i18n.default_lang and overrides this on first boot.
  locale: 'en',
  fallbackLocale: 'en',
  messages: {
    it: itUI,
    en: enUI,
  },
})

// A relative path: the @catalogs alias does not resolve in a glob.
const catalogues = import.meta.glob<{ default: Record<string, unknown> }>(
  '../../../internal/i18n/catalogs/ui.{de,es,fr}.json',
)
const byLanguage = new Map(
  Object.entries(catalogues).map(([file, load]) => [file.replace(/^.*\/ui\.(.+)\.json$/, '$1'), load]),
)
const loaded = new Set(['it', 'en'])
const loading = new Map<string, Promise<void>>()

/** Whether a language's catalogue is in. */
export function isLoaded(lang: string): boolean {
  return loaded.has(lang)
}

/** Loads a language's catalogue once; at once for one that is in, or
 *  unknown. Merged, not set: the audit's phrases may be in already. */
export function loadLocale(lang: string): Promise<void> {
  const load = byLanguage.get(lang)
  if (loaded.has(lang) || !load) return Promise.resolve()
  let p = loading.get(lang)
  if (!p) {
    p = load()
      .then((m) => {
        i18n.global.mergeLocaleMessage(lang, m.default)
        loaded.add(lang)
      })
      .finally(() => loading.delete(lang))
    loading.set(lang, p)
  }
  return p
}
