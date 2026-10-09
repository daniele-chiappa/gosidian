import { describe, expect, it, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { mount } from '@vue/test-utils'
import { useSidebarResize } from '@/composables/useSidebarResize'

function harness() {
  let api!: ReturnType<typeof useSidebarResize>
  const w = mount(
    defineComponent({
      setup() {
        api = useSidebarResize()
        return () => h('div', { class: 'handle', onPointerdown: (e: PointerEvent) => api.startDrag(e) })
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
