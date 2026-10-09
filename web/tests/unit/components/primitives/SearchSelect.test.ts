import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import SearchSelect from '@/components/primitives/SearchSelect.vue'

const items = ['alpha', 'beta', 'gamma']

function mountSelect(modelValue = '') {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
  return mount(SearchSelect, {
    props: {
      modelValue,
      items,
      valueKey: (s: unknown) => String(s),
      label: (s: unknown) => String(s),
      'onUpdate:modelValue': () => {},
    },
    global: { plugins: [i18n] },
  })
}

describe('SearchSelect keyboard', () => {
  it('moves through the entries and picks one with Enter', async () => {
    const w = mountSelect()
    const input = w.find('input')
    await input.trigger('keydown', { key: 'ArrowDown' }) // opens
    expect(w.find('[role="listbox"]').exists()).toBe(true)
    await input.trigger('keydown', { key: 'ArrowDown' })
    await input.trigger('keydown', { key: 'ArrowDown' })
    const options = w.findAll('[role="option"]')
    expect(options[1]!.attributes('aria-selected')).toBe('true')
    expect(input.attributes('aria-activedescendant')).toBe(options[1]!.attributes('id'))
    await input.trigger('keydown', { key: 'End' })
    expect(w.findAll('[role="option"]')[2]!.attributes('aria-selected')).toBe('true')
    await input.trigger('keydown', { key: 'Home' })
    await input.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('update:modelValue')?.at(-1)).toEqual(['alpha'])
    expect(w.find('[role="listbox"]').exists()).toBe(false)
  })

  it('keeps the typed text on Enter with no entry active, and closes on Escape', async () => {
    const w = mountSelect()
    const input = w.find('input')
    await input.setValue('zzz')
    await input.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('update:modelValue')?.at(-1)).toEqual(['zzz'])
    await input.setValue('a')
    expect(w.find('[role="listbox"]').exists()).toBe(true)
    await input.trigger('keydown', { key: 'Escape' })
    expect(w.find('[role="listbox"]').exists()).toBe(false)
  })

  // GraphView passes label and secondary as inline functions: each of its
  // redraws hands new ones, and the entry the keyboard was on vanished.
  it('keeps the active entry when the parent redraws', async () => {
    const w = mountSelect()
    const input = w.find('input')
    // A query makes the list depend on label, as typing in the graph does.
    await input.setValue('a')
    await input.trigger('keydown', { key: 'ArrowDown' })
    await input.trigger('keydown', { key: 'ArrowDown' })
    await w.setProps({ label: (s: unknown) => String(s), secondary: (s: unknown) => String(s).length.toString() })
    await input.trigger('keydown', { key: 'Enter' })
    expect(w.emitted('update:modelValue')?.at(-1)).toEqual(['beta'])
  })
})
