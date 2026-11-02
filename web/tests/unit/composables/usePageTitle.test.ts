import { beforeEach, describe, expect, it } from 'vitest'
import { defineComponent, h, nextTick } from 'vue'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { useWindowsStore } from 'plancia'
import { createI18n, useI18n } from 'vue-i18n'
import { pageTitle, usePageTitle, useShellPageTitle } from '@/composables/usePageTitle'

describe('pageTitle (IMP-217, F-07)', () => {
  it('joins the parts, gosidian last', () => {
    expect(pageTitle(['nota-alfa', 'agile-lab'])).toBe('nota-alfa · agile-lab · gosidian')
    expect(pageTitle(['Search'])).toBe('Search · gosidian')
  })

  it('is gosidian alone without parts', () => {
    expect(pageTitle([])).toBe('gosidian')
    expect(pageTitle([null, undefined, '', '  '])).toBe('gosidian')
  })

  it('says a part once, where a window title names its project already', () => {
    expect(pageTitle(['IN · agile-lab', 'agile-lab'])).toBe('IN · agile-lab · gosidian')
  })
})

describe('useShellPageTitle (IMP-217, F-07)', () => {
  const Shell = defineComponent({
    setup() {
      useShellPageTitle()
      return () => h('div')
    },
  })

  beforeEach(() => {
    setActivePinia(createPinia())
    document.title = ''
  })

  it('follows the focused window and its project', async () => {
    const s = useWindowsStore()
    const w = mount(Shell)
    expect(document.title).toBe('gosidian')
    const path = 'agile-lab/docs/nota-alfa.md'
    const a = s.open({ type: 'note', key: `note:${path}`, title: 'nota-alfa', props: { path } })
    await nextTick()
    expect(document.title).toBe('nota-alfa · agile-lab · gosidian')
    s.open({ type: 'search', key: 'search', title: 'Search' })
    await nextTick()
    expect(document.title).toBe('Search · gosidian')
    s.focus(a)
    await nextTick()
    expect(document.title).toBe('nota-alfa · agile-lab · gosidian')
    // A note window takes the title of its note once it has loaded.
    s.setTitle(a, 'Nota alfa')
    await nextTick()
    expect(document.title).toBe('Nota alfa · agile-lab · gosidian')
    s.reset()
    await nextTick()
    expect(document.title).toBe('gosidian')
    w.unmount()
  })

  it('names the project of a folder, a graph of links and an access window', async () => {
    const s = useWindowsStore()
    const w = mount(Shell)
    const cases: [Parameters<typeof s.open>[0], string][] = [
      [{ type: 'create', key: 'create:agile-lab', title: 'New note · agile-lab', props: { path: 'agile-lab' } }, 'New note · agile-lab · gosidian'],
      [{ type: 'create', key: 'create:agile-lab/docs', title: 'New note · docs', props: { path: 'agile-lab/docs' } }, 'New note · docs · agile-lab · gosidian'],
      [{ type: 'graph', key: 'graph:agile-lab/x.md', title: 'Links · x', props: { focus: 'agile-lab/x.md', depth: 1 } }, 'Links · x · agile-lab · gosidian'],
      [{ type: 'project-members', key: 'project-members:agile-lab', title: 'Access · agile-lab', props: { project: 'agile-lab' } }, 'Access · agile-lab · gosidian'],
    ]
    for (const [spec, title] of cases) {
      s.open(spec)
      await nextTick()
      expect(document.title).toBe(title)
    }
    w.unmount()
  })
})

describe('usePageTitle (IMP-217, F-07)', () => {
  it('follows the language of the UI', async () => {
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: { x: 'Sign in' }, it: { x: 'Accedi' } } })
    const View = defineComponent({
      setup() {
        const { t } = useI18n()
        usePageTitle(() => [t('x')])
        return () => h('div')
      },
    })
    const w = mount(View, { global: { plugins: [i18n] } })
    expect(document.title).toBe('Sign in · gosidian')
    i18n.global.locale.value = 'it'
    await nextTick()
    expect(document.title).toBe('Accedi · gosidian')
    w.unmount()
  })
})
