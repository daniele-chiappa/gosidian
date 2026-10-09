import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'

vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => ({ open: vi.fn() }),
}))

import CanvasPreview from '@/components/domain/CanvasPreview.vue'

const card = (id: string, url: string) => ({ id, type: 'link', x: 0, y: 0, width: 200, height: 80, url })

describe('CanvasPreview link cards (BUG-117, S7-14)', () => {
  it('links a web address only', () => {
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
    const w = mount(CanvasPreview, {
      props: {
        canvas: { nodes: [card('a', 'https://example.com/x'), card('b', 'javascript:alert(1)')], edges: [] },
      },
      global: { plugins: [i18n], provide: { openWindow: vi.fn() } },
    })
    const hrefs = w.findAll('a[href]').map((a) => a.attributes('href'))
    expect(hrefs).toContain('https://example.com/x')
    expect(hrefs.some((h) => /^javascript:/i.test(h ?? ''))).toBe(false)
    expect(w.find('[data-canvas-bad-url]').text()).toBe('javascript:alert(1)')
  })

  it("resolves a text card's relative link from the canvas folder", () => {
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
    const openWindow = vi.fn()
    const text = { id: 't', type: 'text', x: 0, y: 0, width: 200, height: 80, html: '<p><a id="rel" href="design.md">d</a></p>' }
    const w = mount(CanvasPreview, {
      props: { canvas: { nodes: [text], edges: [] }, path: 'proj/boards/plan.canvas' },
      global: { plugins: [i18n], provide: { openWindow } },
    })
    w.find('#rel').element.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 }))
    expect(openWindow).toHaveBeenCalledWith(expect.objectContaining({ props: { path: 'proj/boards/design.md' } }))
  })
})
