/// <reference types="vite/client" />
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

// The phrases of the audit's 60 actions are a catalogue of their own,
// audit.<lang>.json (IMP-220): only the owner's audit page reads them, so
// they stay out of the first load. Each is a chunk fetched once.
const catalogues = import.meta.glob<{ default: Record<string, unknown> }>('../../../internal/i18n/catalogs/audit.*.json')
const byLanguage = new Map(
  Object.entries(catalogues).map(([file, load]) => [file.replace(/^.*\/audit\.(.+)\.json$/, '$1'), load]),
)

/**
 * Merges the audit's catalogue into the web UI's messages, for the
 * reader's language and for English, the others' fallback; again when the
 * language changes. `ready` turns true once the first are in.
 */
export function useAuditCatalog() {
  const { locale, mergeLocaleMessage } = useI18n({ useScope: 'global' })
  const ready = ref(false)
  async function load(lang: string) {
    for (const l of new Set(['en', lang])) {
      const get = byLanguage.get(l)
      if (get) mergeLocaleMessage(l, (await get()).default)
    }
    ready.value = true
  }
  watch(locale, (l) => void load(String(l)), { immediate: true })
  return { ready }
}
