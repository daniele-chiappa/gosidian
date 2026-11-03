/**
 * useSidebarResize — drag handle + persisted width composable.
 *
 * Bounds: 180–560 px (mirrors v1.x sidebar-resize.js). Double-click
 * the handle resets to 260 px default. Persisted in localStorage
 * under `gosidian.sidebar-width`. From the keyboard it moves as a
 * window's separator does: the arrows by 24 px, 64 with Shift, Home
 * back to 260 (IMP-219, F-17).
 *
 * Returns:
 *   - width: ref<number> — bind to inline style and aria-valuenow
 *   - min, max: number — the bounds, for aria-valuemin/max
 *   - dragging: ref<boolean> — for cursor / overlay UX
 *   - startDrag: (e: PointerEvent) => void — wire to handle pointerdown
 *   - onKeydown: (e: KeyboardEvent) => void — wire to handle keydown
 *   - reset: () => void — wire to handle dblclick
 */
import { ref, onUnmounted } from 'vue'

const STORAGE_KEY = 'gosidian.sidebar-width'
const MIN = 180
const MAX = 560
const DEFAULT = 260
/** A key's step, and with Shift: the same as a window's separator. */
const STEP = 24
const STEP_SHIFT = 64

function loadWidth(): number {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return DEFAULT
    const n = parseInt(raw, 10)
    if (!Number.isFinite(n) || n < MIN || n > MAX) return DEFAULT
    return n
  } catch {
    return DEFAULT
  }
}

export function useSidebarResize() {
  const width = ref<number>(loadWidth())
  const dragging = ref<boolean>(false)

  function persist(value: number) {
    try {
      localStorage.setItem(STORAGE_KEY, String(value))
    } catch {
      // ignore — see useRecentlyViewed for rationale
    }
  }

  let raf = 0
  let handle: HTMLElement | null = null
  function onMove(e: PointerEvent) {
    if (!dragging.value) return
    const next = Math.max(MIN, Math.min(MAX, e.clientX))
    if (raf) cancelAnimationFrame(raf)
    raf = requestAnimationFrame(() => {
      width.value = next
    })
  }
  function onUp() {
    if (!dragging.value) return
    dragging.value = false
    persist(width.value)
    unlisten()
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
  }

  function unlisten() {
    document.removeEventListener('pointermove', onMove)
    document.removeEventListener('pointerup', onUp)
    document.removeEventListener('pointercancel', onUp)
    handle?.removeEventListener('lostpointercapture', onUp)
    handle = null
  }

  function startDrag(e: PointerEvent) {
    e.preventDefault()
    dragging.value = true
    document.body.style.cursor = 'col-resize'
    document.body.style.userSelect = 'none'
    // The handle captures the pointer: over an iframe (an HTML note) or
    // outside the page the pointerup never reached the document, and the
    // drag stayed on (S7-15). A capture lost or a pointer cancelled ends it.
    handle = e.currentTarget instanceof HTMLElement ? e.currentTarget : null
    try {
      handle?.setPointerCapture(e.pointerId)
    } catch {
      // a pointer the browser no longer tracks: the document still listens
    }
    handle?.addEventListener('lostpointercapture', onUp)
    document.addEventListener('pointermove', onMove)
    document.addEventListener('pointerup', onUp)
    document.addEventListener('pointercancel', onUp)
  }

  function reset() {
    width.value = DEFAULT
    persist(DEFAULT)
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Home') {
      reset()
      e.preventDefault()
      return
    }
    const dir = e.key === 'ArrowRight' ? 1 : e.key === 'ArrowLeft' ? -1 : 0
    if (!dir) return
    width.value = Math.max(MIN, Math.min(MAX, width.value + dir * (e.shiftKey ? STEP_SHIFT : STEP)))
    persist(width.value)
    e.preventDefault()
  }

  onUnmounted(() => {
    unlisten()
    if (raf) cancelAnimationFrame(raf)
  })

  return { width, min: MIN, max: MAX, dragging, startDrag, onKeydown, reset }
}
