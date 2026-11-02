import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, inject } from 'vue'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { WINDOW_HEADING, provideOpenFocused, useOpenFocused } from '@/composables/useOpenFocused'

/** What the plancia draws for a window: the frame, then the title of its
 *  view once that has loaded (unless `titled` is false). */
function draw(id: string, titled = true): HTMLElement {
  const section = document.createElement('section')
  section.dataset.winId = id
  section.tabIndex = -1
  const body = document.createElement('div')
  body.className = 'plancia-window__body'
  section.append(body)
  document.body.append(section)
  if (!titled) return section
  const h1 = document.createElement('h1')
  h1.tabIndex = -1
  h1.dataset.windowHeading = ''
  body.append(h1)
  return h1
}

function button(): HTMLButtonElement {
  const b = document.createElement('button')
  document.body.append(b)
  b.focus()
  return b
}

const note = (path: string): OpenSpec => ({ type: 'note', key: `note:${path}`, title: path, props: { path } })

describe('useOpenFocused (IMP-217, F-03)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    // What AppShell gives the plancia.
    useWindowsStore().configure({ focusTarget: WINDOW_HEADING })
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
    document.body.innerHTML = ''
  })

  it('puts the focus on the title of the window it opens, once its view has drawn it', () => {
    button()
    const id = useOpenFocused()(note('p/a.md'))
    vi.advanceTimersByTime(300)
    const heading = draw(id)
    vi.advanceTimersByTime(100)
    expect(document.activeElement).toBe(heading)
  })

  it('focuses the title of a window opened again', () => {
    const open = useOpenFocused()
    const heading = draw(open(note('p/a.md')))
    open(note('p/b.md'))
    button()
    open(note('p/a.md'))
    vi.advanceTimersByTime(100)
    expect(document.activeElement).toBe(heading)
  })

  it('leaves the focus where a key moved it meanwhile', () => {
    button()
    const id = useOpenFocused()(note('p/a.md'))
    const other = button()
    draw(id)
    vi.advanceTimersByTime(100)
    expect(document.activeElement).toBe(other)
  })

  it('takes the focus once the opener is gone, as the palette closes', () => {
    const opener = button()
    const id = useOpenFocused()(note('p/a.md'))
    opener.remove()
    const heading = draw(id)
    vi.advanceTimersByTime(100)
    expect(document.activeElement).toBe(heading)
  })

  it('falls back to the window when its view draws no title in 2 s', () => {
    button()
    const id = useOpenFocused()(note('p/a.md'))
    const section = draw(id, false)
    vi.advanceTimersByTime(1900)
    expect(document.activeElement).not.toBe(section)
    vi.advanceTimersByTime(200)
    expect(document.activeElement).toBe(section)
  })

  it('gives up after 2 s when the window is not drawn', () => {
    const opener = button()
    const id = useOpenFocused()(note('p/a.md'))
    vi.advanceTimersByTime(2100)
    draw(id)
    vi.advanceTimersByTime(100)
    expect(document.activeElement).toBe(opener)
  })

  it('gives the focus to the last of two opens', () => {
    button()
    const open = useOpenFocused()
    const first = open(note('p/a.md'))
    const second = open(note('p/b.md'))
    draw(first)
    const heading = draw(second)
    vi.advanceTimersByTime(100)
    expect(document.activeElement).toBe(heading)
  })

  it('leaves the focus to a view that takes it, as a new note in its editor', () => {
    const opener = button()
    const id = useOpenFocused()({ ...note('p/a.md'), props: { path: 'p/a.md', mode: 'edit', editorFocus: true } })
    draw(id)
    vi.advanceTimersByTime(100)
    expect(document.activeElement).toBe(opener)
  })

  it('leaves the focus alone for an open with focus: false', () => {
    const opener = button()
    const id = useOpenFocused()({ ...note('p/a.md'), focus: false })
    draw(id)
    vi.advanceTimersByTime(100)
    expect(document.activeElement).toBe(opener)
  })

  it("is what the views inject as 'openWindow' under the shell", () => {
    let injected: ((spec: OpenSpec) => string) | undefined
    const View = defineComponent({
      setup() {
        injected = inject<(spec: OpenSpec) => string>('openWindow')
        return () => h('div')
      },
    })
    const Shell = defineComponent({
      setup() {
        provideOpenFocused()
        return () => h(View)
      },
    })
    mount(Shell)
    button()
    const heading = draw(injected!(note('p/a.md')))
    vi.advanceTimersByTime(100)
    expect(document.activeElement).toBe(heading)
  })
})
