import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import type { ViewColumn } from '@/api/preview'

vi.mock('@/api/noteTitles', () => ({ suggestNoteTitles: vi.fn(async () => []) }))

import FieldEditor from '@/components/views/FieldEditor.vue'
import { toChange } from '@/components/views/cellValue'

const col = (type: string, extra: Partial<ViewColumn> = {}) => ({ name: 'f', type, ...extra }) as ViewColumn

function mountEditor(column: ViewColumn, value?: string | string[]) {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
  return mount(FieldEditor, { props: { column, value, label: 'f' }, global: { plugins: [i18n] }, attachTo: document.body })
}

describe('FieldEditor (BUG-117)', () => {
  it('does not turn a number it cannot read into an empty field (S7-7)', async () => {
    const w = mountEditor(col('number'), '4')
    const input = w.find('input')
    Object.defineProperty(input.element, 'validity', { value: { badInput: true } })
    ;(input.element as HTMLInputElement).value = ''
    await input.trigger('blur')
    const [committed] = w.emitted('commit')![0] as [string]
    expect(committed).toBe('NaN')
    expect(toChange(col('number'), committed, '4')).toEqual({ error: 'not_a_number' })
    w.unmount()
  })

  it('saves a typed date on blur, not at every key (S7-7)', async () => {
    const w = mountEditor(col('date'), '2026-10-09')
    const input = w.find('input')
    await input.trigger('keydown', { key: '2' })
    await input.setValue('0002-10-09') // a year typed halfway
    await input.trigger('change')
    expect(w.emitted('commit')).toBeUndefined()
    await input.setValue('2027-10-09')
    await input.trigger('blur')
    expect(w.emitted('commit')![0]).toEqual(['2027-10-09'])
    w.unmount()
  })

  it('saves a date picked from a calendar opened with the keyboard (S7-7)', async () => {
    const w = mountEditor(col('date'), '2026-10-09')
    const input = w.find('input')
    await input.trigger('keydown', { key: 'ArrowDown', altKey: true })
    await input.setValue('2026-12-24')
    await input.trigger('change')
    expect(w.emitted('commit')![0]).toEqual(['2026-12-24'])
    w.unmount()
  })

  it('saves a date picked from the calendar at once (S7-7)', async () => {
    const w = mountEditor(col('date'), '2026-10-09')
    const input = w.find('input')
    await input.trigger('pointerdown')
    await input.setValue('2026-11-01')
    await input.trigger('change')
    expect(w.emitted('commit')![0]).toEqual(['2026-11-01'])
    w.unmount()
  })

  it('keeps the focus on mousedown over an option or a suggestion (S7-10)', () => {
    const w = mountEditor(col('multi-select', { options: ['a', 'b'] }), ['a'])
    const label = w.findAll('label')[1]!.element
    const ev = new MouseEvent('mousedown', { bubbles: true, cancelable: true })
    label.dispatchEvent(ev)
    expect(ev.defaultPrevented).toBe(true)
    w.unmount()
  })
})
