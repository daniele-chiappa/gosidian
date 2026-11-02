import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { useWindowsStore } from 'plancia'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'

vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  // No address nor browser store here: the tests open the windows.
  usePlanciaSync: () => ({ hydrate: () => {} }),
}))

vi.mock('@/components/plancia/windowRegistry', async () => {
  const { defineComponent, h } = await import('vue')
  // A view that names itself in an h1, as every view does.
  const Probe = defineComponent({
    props: { name: { type: String, default: '' } },
    setup: (p) => () => h('h1', { tabindex: '-1', 'data-window-heading': '' }, p.name),
  })
  return { windowRegistry: { probe: Probe } }
})

import AppShell from '@/components/layout/AppShell.vue'
import { useAuthStore } from '@/stores/auth'

function mountShell() {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
  return mount(AppShell, {
    global: {
      plugins: [i18n],
      stubs: { TopBar: true, Sidebar: true, CommandPalette: true, PasswordChange: true, TotpEnroll: true },
    },
    attachTo: document.body,
  })
}

describe('AppShell landmarks (IMP-217, F-35)', () => {
  beforeEach(() => setActivePinia(createPinia()))
  afterEach(() => {
    document.body.innerHTML = ''
  })

  it("keeps the sidebar's separator in the sidebar's landmark", () => {
    const w = mountShell()
    const separator = w.findAll('[role="separator"]').find((s) => s.attributes('aria-label') === 'Resize the sidebar')
    expect(separator?.element.closest('aside')).toBeTruthy()
    w.unmount()
  })

  it('names the empty desk in an h1, and says where the minimized windows went', async () => {
    const w = mountShell()
    expect(w.find('main h1').text()).toBe('No windows open.')
    expect(w.find('main').text()).toContain('Open a note from the sidebar.')
    const windows = useWindowsStore()
    windows.minimize(windows.open({ type: 'probe', key: 'probe', title: 'Probe' }))
    await nextTick()
    expect(w.find('main h1').text()).toBe('No windows open.')
    expect(w.find('main').text()).toContain('1 minimized in the footer')
    expect(w.find('main').text()).not.toContain('Open a note from the sidebar.')
    w.unmount()
  })

  it.each([
    ['passwordChangeRequired', 'Choose your password'],
    ['enrollmentRequired', 'Two-factor required'],
  ] as const)('draws the %s gate as the main, with its h1', (gate, title) => {
    useAuthStore()[gate] = true
    const w = mountShell()
    const mains = w.findAll('main')
    expect(mains).toHaveLength(1)
    expect(mains[0]!.find('h1').text()).toBe(title)
    w.unmount()
  })
})

describe('AppShell and the focus of the windows (F-03)', () => {
  beforeEach(() => setActivePinia(createPinia()))
  afterEach(() => {
    document.body.innerHTML = ''
  })

  it("gives the plancia the views' titles as the focus target of a window", () => {
    const w = mountShell()
    expect(useWindowsStore().focusTarget).toBe('[data-window-heading]')
    w.unmount()
  })

  it('takes the focus to the main once the last window has gone with it', async () => {
    const w = mountShell()
    const windows = useWindowsStore()
    windows.open({ type: 'probe', key: 'probe:A', title: 'A', props: { name: 'A' } })
    await flushPromises()
    const close = w.find<HTMLButtonElement>('[data-win-id] header button[aria-label="Close"]')
    close.element.focus()
    await close.trigger('click')
    await flushPromises()
    expect(windows.windows).toHaveLength(0)
    expect(document.activeElement).toBe(w.find('main').element)
    w.unmount()
  })
})

describe('AppShell skip link (IMP-217, F-35)', () => {
  beforeEach(() => setActivePinia(createPinia()))
  afterEach(() => {
    document.body.innerHTML = ''
  })

  const probe = (name: string) => ({ type: 'probe', key: `probe:${name}`, title: name, props: { name } })
  const link = (w: ReturnType<typeof mountShell>) => w.find('[data-skip-to-window]')

  it('comes first, once a window is open', async () => {
    const w = mountShell()
    expect(link(w).exists()).toBe(false)
    useWindowsStore().open(probe('A'))
    await flushPromises()
    expect(link(w).text()).toBe('Skip to the active window')
    expect(w.element.firstElementChild).toBe(link(w).element)
    w.unmount()
  })

  it('takes the focus to the title of the active window', async () => {
    const w = mountShell()
    const windows = useWindowsStore()
    const a = windows.open(probe('A'))
    windows.open(probe('B'))
    windows.focus(a)
    await flushPromises()
    await link(w).trigger('click')
    expect(document.activeElement?.matches('[data-window-heading]')).toBe(true)
    expect(document.activeElement?.textContent).toBe('A')
    w.unmount()
  })

  it('takes it to the main while the window has no title', async () => {
    const w = mountShell()
    useWindowsStore().open({ type: 'gone', key: 'gone', title: 'Gone' })
    await flushPromises()
    await link(w).trigger('click')
    expect(document.activeElement).toBe(w.find('main').element)
    w.unmount()
  })
})
