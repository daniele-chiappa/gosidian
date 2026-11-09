import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import itUI from '@catalogs/ui.it.json'
import { i18n as appI18n } from '@/locales'
import type { ViewColumn } from '@/api/preview'
import FieldValue from '@/components/views/FieldValue.vue'

// FieldValue has several root nodes: its text is read from a host.
function show(value: string | string[], column?: ViewColumn) {
  appI18n.global.locale.value = 'it'
  const i18n = createI18n({ legacy: false, locale: 'it', fallbackLocale: 'en', messages: { en: enUI, it: itUI } })
  const host = defineComponent({ render: () => h('p', [h(FieldValue, { value, column })]) })
  return mount(host, { global: { plugins: [i18n] } })
}

describe('FieldValue, values and dates in words (IMP-220, F-23)', () => {
  it("names a select's values, the code in the tooltip", () => {
    const w = show(['submitted', 'mine'], { name: 'status', type: 'multi-select' })
    expect(w.text()).toBe('inviato, mine')
    const named = w.find('[title="submitted"]')
    expect(named.text()).toBe('inviato')
    expect(w.find('[title="mine"]').exists()).toBe(false)
  })

  it('writes a date as the reader reads it, the ISO form in the tooltip', () => {
    const w = show('2026-11-03', { name: 'due', type: 'date' })
    const time = w.find('time')
    expect(time.text()).toBe('3 nov 2026')
    expect(time.attributes('datetime')).toBe('2026-11-03')
    expect(time.attributes('title')).toBe('2026-11-03')
    // A text that is no date stays.
    expect(show('soon', { name: 'due', type: 'date' }).text()).toBe('soon')
  })

  it('leaves a value as written without a column', () => {
    expect(show('submitted').text()).toBe('submitted')
    expect(show('2026-11-03').find('time').exists()).toBe(false)
  })
})
