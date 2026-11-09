import { useI18n } from 'vue-i18n'
import type { ViewColumn } from '@/api/preview'
import { fieldLabel } from './fieldLabel'

// A key or value the catalogue may hold: no dots, brackets or spaces, which
// would read as a path.
const plain = /^[A-Za-z0-9_-]+$/

/**
 * A field and its values as the web UI names them (IMP-220, F-23): what the
 * database's schema declares in the reader's language (label, labels,
 * option_labels), else the catalogue's name for the fields of gosidian's
 * own databases (tasks, boards, plans, backlogs), else the key as words. A
 * value is named only in a select: elsewhere it is the text written.
 */
export function useFieldNames() {
  const { t, te, locale } = useI18n()
  const inLanguage = <T>(m?: Record<string, T>): T | undefined => {
    if (!m) return undefined
    const l = String(locale.value)
    return m[l] ?? m[l.split('-')[0]!]
  }
  // English is the catalogues' fallback: a German reader gets its names.
  const known = (key: string) => te(key) || te(key, 'en')

  function fieldName(col: Pick<ViewColumn, 'name' | 'label' | 'labels'>): string {
    const own = inLanguage(col.labels) ?? col.label
    if (own) return own
    const key = `fields.name.${col.name}`
    return plain.test(col.name) && known(key) ? t(key) : fieldLabel(col.name)
  }

  function valueName(col: Pick<ViewColumn, 'type' | 'option_labels'>, value: string): string {
    if (col.type !== 'select' && col.type !== 'multi-select') return value
    const own = inLanguage(col.option_labels)?.[value]
    if (own) return own
    const key = `fields.value.${value}`
    return plain.test(value) && known(key) ? t(key) : value
  }

  return { fieldName, valueName }
}
