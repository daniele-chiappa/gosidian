import { afterEach, describe, expect, it } from 'vitest'
import { i18n } from '@/locales'
import { formatDateTime, formatSize } from '@/api/format'

describe('format (IMP-155, m1)', () => {
  afterEach(() => {
    i18n.global.locale.value = 'en'
  })

  it('writes a date in the language of the web UI', () => {
    i18n.global.locale.value = 'it'
    const s = formatDateTime('2026-10-08T13:28:55Z')
    expect(s).toMatch(/2026/)
    expect(s).toMatch(/ott/)
    expect(s).not.toContain('T13')
    i18n.global.locale.value = 'en'
    expect(formatDateTime('2026-10-08T13:28:55Z')).toMatch(/Oct/)
  })

  it('leaves no date empty and a non-date as it is', () => {
    expect(formatDateTime(undefined)).toBe('')
    expect(formatDateTime('soon')).toBe('soon')
  })

  it('writes sizes in B, KB and MB', () => {
    expect(formatSize(512)).toBe('512 B')
    expect(formatSize(102986)).toBe('101 KB')
    expect(formatSize(1468006)).toBe('1.4 MB')
    i18n.global.locale.value = 'it'
    expect(formatSize(1468006)).toBe('1,4 MB')
  })
})
