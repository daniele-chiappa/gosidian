import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import ScrollRegion from '@/components/primitives/ScrollRegion.vue'

describe('ScrollRegion (IMP-224, F-34)', () => {
  it('is a region with a name the Tab reaches, around what it holds', () => {
    const w = mount(ScrollRegion, {
      props: { label: 'Users' },
      attrs: { class: 'rounded' },
      slots: { default: '<table><tr><td>owner</td></tr></table>' },
    })
    const box = w.find('div')
    expect(box.attributes()).toMatchObject({ role: 'region', 'aria-label': 'Users', tabindex: '0' })
    expect(box.classes()).toEqual(expect.arrayContaining(['overflow-x-auto', 'rounded']))
    expect(box.find('td').text()).toBe('owner')
  })
})
