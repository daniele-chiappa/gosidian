/// <reference types="vite/client" />
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

// The words of a board's head and of its stale cards are a catalogue of
// their own, board.<lang>.json (IMP-216 phase 2): only a board reads them,
// so they stay out of the first load, as the audit's do. The server reads
// the same catalogue for the review it writes in an iteration's note.
const catalogues = import.meta.glob<{ default: Record<string, unknown> }>('../../../internal/i18n/catalogs/board.*.json')
const byLanguage = new Map(
  Object.entries(catalogues).map(([file, load]) => [file.replace(/^.*\/board\.(.+)\.json$/, '$1'), load]),
)

/**
 * Merges the board's catalogue into the web UI's messages, for the
 * reader's language and for English, the others' fallback; again when the
 * language changes. `ready` turns true once the first are in.
 */
export function useBoardCatalog() {
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
