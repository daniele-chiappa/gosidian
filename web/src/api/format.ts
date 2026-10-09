import { i18n } from '@/locales'

/**
 * Dates and sizes as the user reads them (IMP-155, m1): in the language of
 * the web UI and the browser's time zone, the ISO form kept for a tooltip.
 */

function locale(): string {
  const l = i18n.global.locale as unknown as { value?: string } | string
  return typeof l === 'string' ? l : (l.value ?? 'en')
}

// One formatter per locale and kind: a table of dates builds none per row.
const formatters = new Map<string, Intl.DateTimeFormat | Intl.NumberFormat>()
function cached<F extends Intl.DateTimeFormat | Intl.NumberFormat>(key: string, make: () => F): F {
  let f = formatters.get(key) as F | undefined
  if (!f) {
    f = make()
    formatters.set(key, f)
  }
  return f
}

/** "8 ott 2026, 15:28"; "" for no date, the value itself when it is not one. */
export function formatDateTime(value?: string | number | Date | null): string {
  if (value === undefined || value === null || value === '') return ''
  const d = value instanceof Date ? value : new Date(value)
  if (Number.isNaN(d.getTime())) return String(value)
  const l = locale()
  return cached(`dt:${l}`, () => new Intl.DateTimeFormat(l, { dateStyle: 'medium', timeStyle: 'short' })).format(d)
}

/** "512 B", "12 KB", "1,4 MB". */
export function formatSize(bytes: number): string {
  const units = ['B', 'KB', 'MB', 'GB']
  let n = bytes
  let i = 0
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i++
  }
  const digits = i === 0 || n >= 10 ? 0 : 1
  const l = locale()
  const nf = cached(`n${digits}:${l}`, () => new Intl.NumberFormat(l, { maximumFractionDigits: digits }))
  return `${nf.format(n)} ${units[i]}`
}
