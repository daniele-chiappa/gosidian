import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'

const store = { open: vi.fn(), identify: vi.fn() }
vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => store,
}))

import MarkdownPreview from '@/components/domain/MarkdownPreview.vue'

function click(el: Element): MouseEvent {
  const ev = new MouseEvent('click', { bubbles: true, cancelable: true, button: 0 })
  el.dispatchEvent(ev)
  return ev
}

describe('MarkdownPreview links (BUG-117, S7-3)', () => {
  it('opens a relative link in a window instead of leaving the app', () => {
    const openWindow = vi.fn(() => 'w1')
    const w = mount(MarkdownPreview, {
      props: {
        html: '<p><a id="rel" href="design.md">d</a> <a id="up" href="../other">o</a></p>',
        notePath: 'proj/docs/plan.md',
      },
      global: { provide: { openWindow } },
    })
    const ev = click(w.find('#rel').element)
    expect(ev.defaultPrevented).toBe(true)
    expect(openWindow).toHaveBeenCalledWith(expect.objectContaining({ type: 'note', props: { path: 'proj/docs/design.md' } }))
    click(w.find('#up').element)
    expect(openWindow).toHaveBeenLastCalledWith(expect.objectContaining({ props: { path: 'proj/other.md' } }))
  })

  it('sends an attachment to a new tab', () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    const w = mount(MarkdownPreview, {
      props: { html: '<p><a id="att" href="attachments/a.pdf">a</a></p>', notePath: 'proj/n.md' },
      global: { provide: { openWindow: vi.fn() } },
    })
    const ev = click(w.find('#att').element)
    expect(ev.defaultPrevented).toBe(true)
    expect(open).toHaveBeenCalledWith('/vault-files/proj/attachments/a.pdf', '_blank', 'noopener,noreferrer')
    open.mockRestore()
  })

  it('leaves mailto: and an app scheme to the browser', () => {
    const w = mount(MarkdownPreview, {
      props: { html: '<p><a id="mail" href="mailto:a@example.com">m</a></p>', notePath: 'proj/n.md' },
      global: { provide: { openWindow: vi.fn() } },
    })
    expect(click(w.find('#mail').element).defaultPrevented).toBe(false)
  })
})
