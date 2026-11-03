import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { mount } from '@vue/test-utils'
import { useSidebarResize } from '@/composables/useSidebarResize'

function harness() {
  let api!: ReturnType<typeof useSidebarResize>
  const w = mount(
    defineComponent({
      setup() {
        api = useSidebarResize()
        return () =>
          h('div', {
            class: 'handle',
            onPointerdown: (e: PointerEvent) => api.startDrag(e),
            onKeydown: (e: KeyboardEvent) => api.onKeydown(e),
          })
      },
    }),
    { attachTo: document.body },
  )
  return { w, api: () => api }
}

function pointerDown(el: Element) {
  const e = new Event('pointerdown', { bubbles: true, cancelable: true }) as PointerEvent
  Object.defineProperty(e, 'pointerId', { value: 7 })
  el.dispatchEvent(e)
}

describe('useSidebarResize (S7-15)', () => {
  it('captures the pointer on the handle', () => {
    const { w, api } = harness()
    const el = w.find('.handle').element as HTMLElement
    const capture = vi.fn()
    el.setPointerCapture = capture
    pointerDown(el)
    expect(api().dragging.value).toBe(true)
    expect(capture).toHaveBeenCalledWith(7)
    w.unmount()
  })

  it('ends the drag when the capture is lost or the pointer cancelled', () => {
    const { w, api } = harness()
    const el = w.find('.handle').element as HTMLElement
    el.setPointerCapture = vi.fn()
    pointerDown(el)
    el.dispatchEvent(new Event('lostpointercapture'))
    expect(api().dragging.value).toBe(false)

    pointerDown(el)
    document.dispatchEvent(new Event('pointercancel'))
    expect(api().dragging.value).toBe(false)
    expect(document.body.style.cursor).toBe('')
    w.unmount()
  })
})

function key(el: Element, k: string, shiftKey = false): KeyboardEvent {
  const e = new KeyboardEvent('keydown', { key: k, shiftKey, bubbles: true, cancelable: true })
  el.dispatchEvent(e)
  return e
}

// F-17 (IMP-219): the separator moves from the keyboard, as a window's
// does: the arrows by 24 px, 64 with Shift, Home back to 260.
describe('useSidebarResize from the keyboard (IMP-219, F-17)', () => {
  beforeEach(() => localStorage.clear())

  it('moves by 24 px with the arrows, 64 with Shift, and keeps the width', () => {
    const { w, api } = harness()
    const el = w.find('.handle').element
    expect(api().width.value).toBe(260)
    expect(key(el, 'ArrowRight').defaultPrevented).toBe(true)
    expect(api().width.value).toBe(284)
    key(el, 'ArrowRight', true)
    expect(api().width.value).toBe(348)
    key(el, 'ArrowLeft')
    expect(api().width.value).toBe(324)
    expect(localStorage.getItem('gosidian.sidebar-width')).toBe('324')
    w.unmount()
  })

  it('stops at its bounds, goes back to 260 with Home, and leaves the other keys alone', () => {
    localStorage.setItem('gosidian.sidebar-width', '548')
    const { w, api } = harness()
    const el = w.find('.handle').element
    expect(api().min).toBe(180)
    expect(api().max).toBe(560)
    key(el, 'ArrowRight', true)
    expect(api().width.value).toBe(560)
    for (let i = 0; i < 8; i++) key(el, 'ArrowLeft', true)
    expect(api().width.value).toBe(180)

    expect(key(el, 'Home').defaultPrevented).toBe(true)
    expect(api().width.value).toBe(260)
    expect(localStorage.getItem('gosidian.sidebar-width')).toBe('260')

    for (const k of ['Tab', 'ArrowUp', 'End', 'a']) expect(key(el, k).defaultPrevented).toBe(false)
    expect(api().width.value).toBe(260)
    w.unmount()
  })
})
