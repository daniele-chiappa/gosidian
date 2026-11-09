import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import itUI from '@catalogs/ui.it.json'
import { useFieldNames } from '@/components/views/fieldNames'

function names(locale: string) {
  let out!: ReturnType<typeof useFieldNames>
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en: enUI, it: itUI, de: {} } })
  mount(
    defineComponent({
      setup() {
        out = useFieldNames()
        return () => null
      },
    }),
    { global: { plugins: [i18n] } },
  )
  return out
}

describe('useFieldNames (IMP-220, F-23)', () => {
  it("names a field by the schema in the reader's language, then by the catalogue, then by its key", () => {
    const it_ = names('it')
    expect(it_.fieldName({ name: 'status', labels: { it: 'Condizione', en: 'State' } })).toBe('Condizione')
    expect(it_.fieldName({ name: 'status', labels: { en: 'State' } })).toBe('Stato')
    expect(it_.fieldName({ name: 'due', label: 'Entro' })).toBe('Entro')
    expect(it_.fieldName({ name: 'claimed_by' })).toBe('Preso da')
    expect(it_.fieldName({ name: 'open_bugs' })).toBe('Open bugs')
    expect(names('en').fieldName({ name: 'lease_until' })).toBe('Held until')
    // A language the catalogue lacks reads the English names.
    expect(names('de').fieldName({ name: 'status' })).toBe('Status')
    expect(names('pt-BR').fieldName({ name: 'x', labels: { pt: 'Xis' } })).toBe('Xis')
  })

  it('names the values of a select, and leaves any other text as written', () => {
    const it_ = names('it')
    const status = { type: 'select', option_labels: { it: { open: 'aperta' } } }
    expect(it_.valueName(status, 'open')).toBe('aperta')
    expect(it_.valueName(status, 'submitted')).toBe('inviato')
    expect(it_.valueName(status, 'in-progress')).toBe('in corso')
    expect(it_.valueName(status, 'on.hold')).toBe('on.hold')
    expect(it_.valueName({ type: 'multi-select' }, 'high')).toBe('alta')
    expect(it_.valueName({ type: 'text' }, 'open')).toBe('open')
    expect(it_.valueName({}, 'done')).toBe('done')
    expect(names('en').valueName({ type: 'select' }, 'input-required')).toBe('input required')
  })
})
