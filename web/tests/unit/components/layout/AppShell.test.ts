import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick, type Plugin } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { Plancia, useWindowsStore, type PlanciaSyncOptions } from 'plancia'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import itUI from '@catalogs/ui.it.json'

const { sync } = vi.hoisted(() => ({ sync: vi.fn((..._args: unknown[]) => ({ hydrate: () => {} })) }))
vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  // No address nor browser store here: the tests open the windows.
  usePlanciaSync: sync,
}))

vi.mock('@/components/plancia/windowRegistry', async () => {
  const { defineComponent, h } = await import('vue')
  // A view that names itself in an h1, as every view does.
  const Probe = defineComponent({
    props: { name: { type: String, default: '' } },
    setup: (p) => () => h('h1', { tabindex: '-1', 'data-window-heading': '' }, p.name),
  })
  return { windowRegistry: { probe: Probe, settings: Probe } }
})

import AppShell from '@/components/layout/AppShell.vue'
import { useAuthStore } from '@/stores/auth'
import { useUIStore } from '@/stores/ui'
import { appStale } from '@/composables/staleApp'

function mountShell(i18n: Plugin = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })) {
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

  // F-17 (IMP-219): the separator is reached with Tab, and says its width
  // and its bounds; the arrows move it.
  it("makes the sidebar's separator a control of the keyboard that says its width", async () => {
    localStorage.removeItem('gosidian.sidebar-width')
    const w = mountShell()
    const separator = w.findAll('[role="separator"]').find((s) => s.attributes('aria-label') === 'Resize the sidebar')!
    expect(separator.attributes()).toMatchObject({
      tabindex: '0',
      'aria-valuemin': '180',
      'aria-valuemax': '560',
      'aria-valuenow': '260',
    })
    expect(separator.classes()).toEqual(expect.arrayContaining(['focus-visible:outline', 'focus-visible:outline-accent']))
    await separator.trigger('keydown', { key: 'ArrowRight' })
    expect(separator.attributes('aria-valuenow')).toBe('284')
    localStorage.removeItem('gosidian.sidebar-width')
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

describe('AppShell and a change of language (BUG-155)', () => {
  beforeEach(() => setActivePinia(createPinia()))
  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('gives the windows already open their titles in the new language', async () => {
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI, it: itUI } })
    const w = mountShell(i18n)
    const windows = useWindowsStore()
    windows.open({ type: 'settings', key: 'settings', title: 'Settings' })
    windows.open({ type: 'note', key: 'note:p/a.md', title: 'Note A', props: { path: 'p/a.md' } })
    i18n.global.locale.value = 'it'
    await flushPromises()
    expect(windows.windows.map((x) => x.title)).toEqual(['Impostazioni', 'Note A'])
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

  // A plugin's app is an iframe, which a browser reloads once it leaves the
  // page: behind another tab it stays in the page, hidden, with its work.
  it("keeps a plugin's app in the page behind another tab, and only that", () => {
    const w = mountShell()
    const keep = w.findComponent(Plancia).props('keepMounted') as (win: { type: string }) => boolean
    expect(keep({ type: 'app' })).toBe(true)
    expect(['note', 'search', 'graph'].filter((type) => keep({ type }))).toEqual([])
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

// A viewport `px` wide: matchMedia answers the max-width queries of
// useNarrowScreen and of the plancia's drawer, and resize() tells them.
function viewport(px: number) {
  let width = px
  const lists: { max: number; mql: MediaQueryList; on: Set<(e: MediaQueryListEvent) => void> }[] = []
  vi.spyOn(window, 'matchMedia').mockImplementation((query: string) => {
    const max = Number(/max-width:\s*(\d+)px/.exec(query)?.[1] ?? Infinity)
    const on = new Set<(e: MediaQueryListEvent) => void>()
    const mql = {
      media: query,
      get matches() {
        return width <= max
      },
      onchange: null,
      addEventListener: (_: string, f: (e: MediaQueryListEvent) => void) => on.add(f),
      removeEventListener: (_: string, f: (e: MediaQueryListEvent) => void) => on.delete(f),
      addListener: (f: (e: MediaQueryListEvent) => void) => on.add(f),
      removeListener: (f: (e: MediaQueryListEvent) => void) => on.delete(f),
      dispatchEvent: () => true,
    } as unknown as MediaQueryList
    lists.push({ max, mql, on })
    return mql
  })
  return {
    resize(px: number) {
      width = px
      for (const l of lists) for (const f of l.on) f({ matches: width <= l.max, media: l.mql.media } as MediaQueryListEvent)
    },
  }
}

describe('AppShell on a narrow screen (IMP-221, F-15)', () => {
  beforeEach(() => setActivePinia(createPinia()))
  afterEach(() => {
    document.body.innerHTML = ''
    vi.restoreAllMocks()
  })

  const aside = (w: ReturnType<typeof mountShell>) => w.find('aside.plancia-sidebar')
  const separator = (w: ReturnType<typeof mountShell>) =>
    w.findAll('[role="separator"]').find((s) => s.attributes('aria-label') === 'Resize the sidebar')
  const bar = (w: ReturnType<typeof mountShell>) => w.findComponent({ name: 'TopBar' })

  it('makes the sidebar a closed drawer without its separator, and shows one window at a time, with no choice of view', async () => {
    viewport(320)
    const w = mountShell()
    await nextTick()
    expect(aside(w).attributes('data-drawer')).toBeDefined()
    expect(aside(w).attributes('data-state')).toBe('closed')
    expect(separator(w)).toBeUndefined()
    expect(bar(w).props()).toMatchObject({ narrow: true, sidebarOpen: false })
    expect(w.findComponent(Plancia).props()).toMatchObject({ viewMode: 'tabs', showViewToggle: false })
    expect(useUIStore().planciaViewMode).toBe('tabs')
    w.unmount()
  })

  it("opens from the bar's button, and closes from its own with the focus back on the bar's", async () => {
    viewport(320)
    const w = mountShell()
    const barButton = document.createElement('button')
    barButton.setAttribute('data-sidebar-toggle', '')
    document.body.append(barButton)
    await nextTick()
    bar(w).vm.$emit('toggle-sidebar')
    await flushPromises()
    expect(aside(w).attributes('data-state')).toBe('expanded')
    expect(bar(w).props('sidebarOpen')).toBe(true)
    const close = aside(w).find<HTMLButtonElement>('button[aria-label="Close the sidebar"]')
    close.element.focus()
    await close.trigger('click')
    await flushPromises()
    expect(aside(w).attributes('data-state')).toBe('closed')
    expect(document.activeElement).toBe(barButton)
    w.unmount()
  })

  it('closes once the focus goes into a window', async () => {
    viewport(320)
    const w = mountShell()
    await nextTick()
    bar(w).vm.$emit('toggle-sidebar')
    await nextTick()
    await w.find('main').trigger('focusin')
    expect(aside(w).attributes('data-state')).toBe('closed')
    w.unmount()
  })

  it('keeps the saved view: a choice of the plancia on a narrow screen does not change it', async () => {
    viewport(320)
    const w = mountShell()
    await nextTick()
    useUIStore().setPlanciaViewMode('strip')
    w.findComponent(Plancia).vm.$emit('update:viewMode', 'tabs')
    expect(useUIStore().planciaViewMode).toBe('strip')
    w.unmount()
  })

  it('goes back inline, open, with its separator and the saved view, once the screen widens', async () => {
    const screen = viewport(320)
    const w = mountShell()
    await nextTick()
    useUIStore().setPlanciaViewMode('strip')
    screen.resize(1280)
    await flushPromises()
    expect(aside(w).attributes('data-drawer')).toBeUndefined()
    expect(aside(w).attributes('data-state')).toBe('expanded')
    expect(separator(w)).toBeDefined()
    expect(bar(w).props('narrow')).toBe(false)
    expect(w.findComponent(Plancia).props()).toMatchObject({ viewMode: 'strip', showViewToggle: false })
    w.unmount()
  })
})

describe('AppShell and the browser history (IMP-224, F-29)', () => {
  beforeEach(() => setActivePinia(createPinia()))
  afterEach(() => {
    document.body.innerHTML = ''
    vi.unstubAllGlobals()
  })

  it("adds a step for each window opened, and asks in the reader's language before Back closes unsaved work", () => {
    const i18n = createI18n({ legacy: false, locale: 'it', messages: { it: itUI, en: enUI } })
    const w = mountShell(i18n)
    const options = sync.mock.lastCall![1] as PlanciaSyncOptions
    expect(options).toMatchObject({ history: 'push', useLocalStorage: true, storageKey: 'gosidian.plancia' })
    const ask = vi.fn(() => false)
    vi.stubGlobal('confirm', ask)
    expect(options.confirmClose!([])).toBe(false)
    expect(ask).toHaveBeenCalledWith('Ci sono modifiche non salvate. Chiudere comunque?')
    w.unmount()
  })
})

describe('AppShell and a page older than the server', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    appStale.value = false
  })
  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('says so when a part of the app does not load, and offers the reload', async () => {
    const w = mountShell()
    expect(w.find('[data-app-stale]').exists()).toBe(false)
    window.dispatchEvent(new Event('vite:preloadError', { cancelable: true }))
    await nextTick()
    expect(w.find('[data-app-stale]').attributes('role')).toBe('alert')
    expect(w.find('[data-app-stale]').text()).toContain('Reload the page')
    expect(w.find('[data-app-stale] button').exists()).toBe(true)
    w.unmount()
  })
})

describe('AppShell, full screen and the sidebar of a wide screen (IMP-229)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.removeItem('gosidian.ui')
  })
  afterEach(() => {
    document.body.innerHTML = ''
    vi.restoreAllMocks()
  })

  const aside = (w: ReturnType<typeof mountShell>) => w.find('aside.plancia-sidebar')
  const separator = (w: ReturnType<typeof mountShell>) =>
    w.findAll('[role="separator"]').find((s) => s.attributes('aria-label') === 'Resize the sidebar')
  const bar = (w: ReturnType<typeof mountShell>) => w.findComponent({ name: 'TopBar' })
  const ctrlBackslash = (over: KeyboardEventInit = {}) =>
    window.dispatchEvent(new KeyboardEvent('keydown', { key: '\\', ctrlKey: true, ...over }))

  it("offers full screen on the windows, and the bar's choice of view alone", () => {
    viewport(1280)
    const w = mountShell()
    expect(w.findComponent(Plancia).props()).toMatchObject({ allowFullscreen: true, showViewToggle: false })
    w.unmount()
  })

  it('sets the bar, the sidebar and the skip link aside while a window covers the page', async () => {
    viewport(1280)
    const w = mountShell()
    const windows = useWindowsStore()
    windows.open({ type: 'probe', key: 'probe:A', title: 'A', props: { name: 'A' } })
    await flushPromises()
    await w.get('[data-plancia-fullscreen]').trigger('click')
    await flushPromises()
    expect(bar(w).attributes('inert')).toBeDefined()
    expect(aside(w).attributes('inert')).toBeDefined()
    expect(w.get('[data-skip-to-window]').attributes('inert')).toBeDefined()
    // Ctrl+\\ leaves the sidebar alone under the window.
    ctrlBackslash()
    await nextTick()
    expect(aside(w).attributes('data-state')).toBe('expanded')
    await w.get('[data-plancia-fullscreen]').trigger('click')
    await flushPromises()
    expect(bar(w).attributes('inert')).toBeUndefined()
    expect(aside(w).attributes('inert')).toBeUndefined()
    w.unmount()
  })

  it("closes and opens from the bar's button, without its separator while closed, and keeps the choice", async () => {
    viewport(1280)
    const w = mountShell()
    await nextTick()
    expect(aside(w).attributes('data-state')).toBe('expanded')
    bar(w).vm.$emit('toggle-sidebar')
    await flushPromises()
    expect(aside(w).attributes('data-state')).toBe('closed')
    expect(aside(w).attributes('data-drawer')).toBeUndefined()
    expect(separator(w)).toBeUndefined()
    expect(bar(w).props('sidebarOpen')).toBe(false)
    expect(useUIStore().sidebarClosed).toBe(true)
    w.unmount()
    // Another page with the same choice: it starts closed.
    const again = mountShell()
    await nextTick()
    expect(aside(again).attributes('data-state')).toBe('closed')
    bar(again).vm.$emit('toggle-sidebar')
    await flushPromises()
    expect(aside(again).attributes('data-state')).toBe('expanded')
    expect(separator(again)).toBeDefined()
    expect(useUIStore().sidebarClosed).toBe(false)
    again.unmount()
  })

  it('Ctrl+\\ and Cmd+\\ do what the button does; other keys do not', async () => {
    viewport(1280)
    const w = mountShell()
    await nextTick()
    ctrlBackslash()
    await flushPromises()
    expect(aside(w).attributes('data-state')).toBe('closed')
    ctrlBackslash({ ctrlKey: false, metaKey: true })
    await flushPromises()
    expect(aside(w).attributes('data-state')).toBe('expanded')
    ctrlBackslash({ shiftKey: true })
    window.dispatchEvent(new KeyboardEvent('keydown', { key: '\\' }))
    await flushPromises()
    expect(aside(w).attributes('data-state')).toBe('expanded')
    w.unmount()
  })

  it("gives the focus to the bar's button when the sidebar closes around it", async () => {
    viewport(1280)
    const w = mountShell()
    const barButton = document.createElement('button')
    barButton.setAttribute('data-sidebar-toggle', '')
    document.body.append(barButton)
    await nextTick()
    const handle = separator(w)!
    ;(handle.element as HTMLElement).focus()
    ctrlBackslash()
    await flushPromises()
    await new Promise((r) => setTimeout(r))
    expect(document.activeElement).toBe(barButton)
    w.unmount()
  })

  it('a narrow screen keeps its drawer and leaves the choice of the wide one as it is', async () => {
    const screen = viewport(1280)
    const w = mountShell()
    await nextTick()
    bar(w).vm.$emit('toggle-sidebar')
    await flushPromises()
    expect(useUIStore().sidebarClosed).toBe(true)
    screen.resize(320)
    await flushPromises()
    bar(w).vm.$emit('toggle-sidebar')
    await flushPromises()
    expect(aside(w).attributes('data-drawer')).toBeDefined()
    expect(aside(w).attributes('data-state')).toBe('expanded')
    expect(useUIStore().sidebarClosed).toBe(true)
    screen.resize(1280)
    await flushPromises()
    expect(aside(w).attributes('data-state')).toBe('closed')
    w.unmount()
  })
})
