import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import CodeMirrorEditor from '@/components/editor/CodeMirrorEditor.vue'

const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })

describe('CodeMirrorEditor, its name (IMP-220, F-11)', () => {
  it('gives the text field the label, and follows a new one', async () => {
    const w = mount(CodeMirrorEditor, {
      props: { modelValue: '# A', label: 'Text of Alpha' },
      global: { plugins: [i18n()] },
      attachTo: document.body,
    })
    const content = () => w.find('.cm-content')
    expect(content().attributes('role')).toBe('textbox')
    expect(content().attributes('aria-label')).toBe('Text of Alpha')
    await w.setProps({ label: 'Text of Beta' })
    expect(content().attributes('aria-label')).toBe('Text of Beta')
    w.unmount()
  })

  it('has no name of its own without a label', () => {
    const w = mount(CodeMirrorEditor, {
      props: { modelValue: '' },
      global: { plugins: [i18n()] },
      attachTo: document.body,
    })
    expect(w.find('.cm-content').attributes('aria-label')).toBeUndefined()
    w.unmount()
  })
})
