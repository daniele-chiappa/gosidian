import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { defineComponent, h } from 'vue'
import enUI from '@catalogs/ui.en.json'
import itUI from '@catalogs/ui.it.json'
import { useDiagramLabels } from '@/components/domain/diagramLabels'

describe('useDiagramLabels', () => {
  it("names a drawing's nodes, groups and links in the reader's language (BUG-157)", () => {
    let labels!: ReturnType<typeof useDiagramLabels>
    const Host = defineComponent({
      setup() {
        labels = useDiagramLabels()
        return () => h('div')
      },
    })
    const i18n = createI18n({ legacy: false, locale: 'it', messages: { it: itUI, en: enUI } })
    mount(Host, { global: { plugins: [i18n] } })
    for (const words of [labels.view.value, labels.editor.value, labels.pipeline.value]) {
      expect(words).toMatchObject({ nodeRole: 'nodo', groupRole: 'gruppo', edgeRole: 'arco' })
      expect(words.edgeName('Inizio', 'Fine')).toBe('da Inizio a Fine')
      expect(words.nodeHelp).toMatch(/^Invio o un clic seleziona il nodo/)
    }
  })
})
