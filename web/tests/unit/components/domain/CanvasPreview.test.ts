import { describe, expect, it, vi } from 'vitest'
import { mount, type VueWrapper } from '@vue/test-utils'
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

describe('CanvasPreview in words and from the keyboard (IMP-224, F-32)', () => {
  const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
  const box = { x: 0, y: 0, width: 200, height: 80 }
  const canvas = {
    nodes: [
      { id: 'g', type: 'group', ...box, width: 600, height: 400, label: 'Plan' },
      { id: 't', type: 'text', ...box, text: '# Idea\nmore', html: '<h1>Idea</h1><p>more</p>', parent: 'g' },
      { id: 'f', type: 'file', ...box, x: 700, file: 'p/a.md', path: 'p/a.md', title: 'Alpha' },
      { id: 'l', type: 'link', ...box, x: 700, y: 200, url: 'https://example.com/' },
    ],
    edges: [
      { id: 'e1', fromNode: 't', toNode: 'f', label: 'uses' },
      { id: 'e2', fromNode: 'f', toNode: 'l', fromEnd: 'arrow' as const },
      { id: 'e3', fromNode: 'g', toNode: 't', toEnd: 'none' as const },
    ],
  }
  const mountCanvas = () =>
    mount(CanvasPreview, { props: { canvas, path: 'p/board.canvas' }, global: { plugins: [i18n()], provide: { openWindow: vi.fn() } } })

  it('says its cards by group and its connections, with their way and label', async () => {
    const w = mountCanvas()
    const toggle = w.find('[data-canvas-list-toggle]')
    expect(toggle.attributes('aria-pressed')).toBe('false')
    await toggle.trigger('click')
    expect(toggle.attributes('aria-pressed')).toBe('true')
    const list = w.find('[data-canvas-list]')
    expect(list.find('p').text()).toBe('3 cards, 1 groups, 3 connections')
    expect(
      list.findAll('[data-canvas-list-group]').map((g) => [g.element.firstChild?.textContent?.trim(), ...g.findAll('li').map((li) => li.text())]),
    ).toEqual([
      ['Group Plan', 'Idea'],
      ['Outside the groups', 'Alpha', 'https://example.com/'],
    ])
    expect(list.findAll('[data-canvas-list-edge]').map((li) => li.text())).toEqual([
      'Idea → Alpha: uses',
      'Alpha ↔ https://example.com/',
      'Group Plan — Idea',
    ])
    expect(w.find('[data-canvas-card]').exists()).toBe(false)
  })

  it('moves and zooms from the keyboard, the plane holding the focus', async () => {
    const w = mountCanvas()
    const plane = w.find('[data-canvas]')
    expect(plane.attributes()).toMatchObject({ tabindex: '0', role: 'region', 'aria-label': 'Canvas' })
    expect(w.find(`#${plane.attributes('aria-describedby')}`).text()).toContain('arrows')
    const at = () => {
      const m = /translate\((-?[\d.]+)px, (-?[\d.]+)px\) scale\(([\d.]+)\)/.exec(w.find('[data-canvas] > div[style]').attributes('style') ?? '')!
      return { tx: Number(m[1]), ty: Number(m[2]), k: Number(m[3]) }
    }
    const key = async (target: VueWrapper | ReturnType<VueWrapper['find']>, k: string, shiftKey = false) =>
      target.trigger('keydown', { key: k, shiftKey })
    const start = at()
    await key(plane, 'ArrowRight')
    expect(at().tx).toBe(start.tx - 50)
    await key(plane, 'ArrowDown', true)
    expect(at().ty).toBe(start.ty - 200)
    await key(plane, '+')
    expect(at().k).toBeGreaterThan(start.k)
    await key(plane, '-')
    await key(plane, '-')
    expect(at().k).toBeLessThan(start.k)
    // The keys of a card are the card's.
    const before = at()
    await key(w.find('[data-canvas-card]'), 'ArrowLeft')
    expect(at()).toEqual(before)
  })
})

