import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import OverflowMenu from '@/components/primitives/OverflowMenu.vue'

describe('OverflowMenu (IMP-156, M5)', () => {
  afterEach(() => {
    document.body.innerHTML = ''
  })

  function mountMenu() {
    const download = vi.fn()
    const history = vi.fn()
    const w = mount(OverflowMenu, {
      attachTo: document.body,
      props: {
        label: 'More actions',
        items: [
          { key: 'download', label: 'Download', run: download },
          { key: 'snapshot', label: 'Snapshot', disabled: true, run: vi.fn() },
          { key: 'history', label: 'History', run: history },
        ],
      },
    })
    return { w, download, history }
  }

  it('opens on the button, runs the entry picked and closes', async () => {
    const { w, history } = mountMenu()
    expect(w.find('[role="menu"]').exists()).toBe(false)
    await w.find('[data-overflow-button]').trigger('click')
    expect(w.find('[role="menu"]').exists()).toBe(true)
    expect(document.activeElement?.getAttribute('data-overflow-item')).toBe('download')
    await w.find('[data-overflow-item="history"]').trigger('click')
    expect(history).toHaveBeenCalledTimes(1)
    expect(w.find('[role="menu"]').exists()).toBe(false)
    w.unmount()
  })

  it('moves with the arrows past a disabled entry and closes on Escape, the focus back on the button', async () => {
    const { w } = mountMenu()
    await w.find('[data-overflow-button]').trigger('click')
    await w.find('[role="menu"]').trigger('keydown', { key: 'ArrowDown' })
    expect(document.activeElement?.getAttribute('data-overflow-item')).toBe('history')
    await w.find('[role="menu"]').trigger('keydown', { key: 'Escape' })
    await nextTick()
    expect(w.find('[role="menu"]').exists()).toBe(false)
    expect(document.activeElement).toBe(w.find('[data-overflow-button]').element)
    w.unmount()
  })

  it('stays open when an entry loses the focus to nowhere, as a click does in Safari (review)', async () => {
    const { w, download } = mountMenu()
    await w.find('[data-overflow-button]').trigger('click')
    const entry = w.find('[data-overflow-item="download"]')
    await entry.trigger('focusout', { relatedTarget: null })
    expect(w.find('[role="menu"]').exists()).toBe(true)
    await entry.trigger('click')
    expect(download).toHaveBeenCalledTimes(1)
    w.unmount()
  })

  it('goes to the last entry on ArrowUp from the button (review)', async () => {
    const { w } = mountMenu()
    await w.find('[data-overflow-button]').trigger('click')
    ;(w.find('[data-overflow-button]').element as HTMLButtonElement).focus()
    await w.find('[role="menu"]').trigger('keydown', { key: 'ArrowUp' })
    expect(document.activeElement?.getAttribute('data-overflow-item')).toBe('history')
    w.unmount()
  })
})
