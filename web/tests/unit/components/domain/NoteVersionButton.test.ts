import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { AxiosError, AxiosHeaders } from 'axios'
import enUI from '@catalogs/ui.en.json'
import type { NoteTag, ValidVersion, Versions } from '@/api/history'

vi.mock('@/api/history', async (orig) => ({
  ...(await orig<typeof import('@/api/history')>()),
  getVersions: vi.fn(),
  getHistory: vi.fn(),
  tagVersion: vi.fn(),
  setValidVersion: vi.fn(),
  clearValidVersion: vi.fn(),
  setMCPVersion: vi.fn(),
  clearMCPVersion: vi.fn(),
}))

import {
  clearMCPVersion,
  clearValidVersion,
  getHistory,
  getVersions,
  setMCPVersion,
  setValidVersion,
  tagVersion,
} from '@/api/history'
import NoteVersionButton from '@/components/domain/NoteVersionButton.vue'

// Which version of a note readers see (IMP-228): the state in the bar of the
// note and of its design, and in a panel the way to change it.

const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })

const tag = (label: string, sha = label.toLowerCase().padEnd(40, '0')): NoteTag => ({
  tag: `note/p/a.md/${label}`,
  label,
  sha,
  short_sha: sha.slice(0, 7),
  date: '2026-11-06T10:00:00Z',
  path: 'p/a.md',
})
const pinned = (label = 'V2', over: Partial<ValidVersion> = {}): ValidVersion => ({
  tag: `note/p/a.md/${label}`,
  label,
  same: false,
  ...over,
})
const list = (over: Partial<Versions> = {}): Versions => ({ items: [tag('V2'), tag('V1')], valid: null, can_admin: true, ...over })

async function mountButton(props: Record<string, unknown> = {}) {
  const w = mount(NoteVersionButton, {
    props: { path: 'p/a.md', ...props },
    global: { plugins: [i18n()] },
    attachTo: document.body,
  })
  return w
}
async function openPanel(props: Record<string, unknown> = {}) {
  const w = await mountButton(props)
  await w.find('[data-version-button]').trigger('click')
  await flushPromises()
  return w
}

describe('NoteVersionButton (IMP-228)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(getVersions).mockResolvedValue(list())
    vi.mocked(getHistory).mockResolvedValue([])
    vi.mocked(tagVersion).mockResolvedValue(tag('V3'))
    vi.mocked(setValidVersion).mockResolvedValue()
    vi.mocked(clearValidVersion).mockResolvedValue()
  })

  it('says at a glance what readers see', async () => {
    const latest = await mountButton()
    expect(latest.find('[data-version-button]').text()).toBe('Latest')
    latest.unmount()
    const fixed = await mountButton({ valid: pinned('V3') })
    expect(fixed.find('[data-version-button]').text()).toContain('Pinned V3')
    expect(fixed.find('[data-version-draft]').exists()).toBe(true)
    fixed.unmount()
    const same = await mountButton({ valid: pinned('V3', { same: true }) })
    expect(same.find('[data-version-draft]').exists()).toBe(false)
    same.unmount()
  })

  it('reads the versions only when the panel opens', async () => {
    const w = await mountButton()
    expect(getVersions).not.toHaveBeenCalled()
    await w.find('[data-version-button]').trigger('click')
    await flushPromises()
    expect(getVersions).toHaveBeenCalledWith('p/a.md')
    expect(w.findAll('[data-version-choice]').map((c) => c.text())).toEqual(
      expect.arrayContaining([expect.stringContaining('V2'), expect.stringContaining('V1')]),
    )
    w.unmount()
  })

  it('pins a tag with a click, and goes back to the latest with another', async () => {
    vi.mocked(getVersions).mockResolvedValue(list({ valid: pinned('V2') }))
    const w = await openPanel({ valid: pinned('V2') })
    await w.find('[data-version-latest]').setValue(true)
    await flushPromises()
    expect(clearValidVersion).toHaveBeenCalledWith('p/a.md')
    await w.find('input[value="note/p/a.md/V1"]').setValue(true)
    await flushPromises()
    expect(setValidVersion).toHaveBeenCalledWith('p/a.md', 'note/p/a.md/V1')
    expect(w.emitted('changed')).toHaveLength(2)
    w.unmount()
  })

  it('chooses what the note gives who calls over MCP', async () => {
    const w = await openPanel()
    const select = w.find('[data-version-mcp]')
    expect((select.element as HTMLSelectElement).value).toBe('')
    expect(select.findAll('option').map((o) => o.text())).toEqual(['The latest change', 'The same as readers', 'V2', 'V1'])
    await select.setValue('valid')
    await flushPromises()
    expect(setMCPVersion).toHaveBeenLastCalledWith('p/a.md', 'valid')
    await select.setValue('note/p/a.md/V1')
    await flushPromises()
    expect(setMCPVersion).toHaveBeenLastCalledWith('p/a.md', 'note/p/a.md/V1')
    await select.setValue('')
    await flushPromises()
    expect(clearMCPVersion).toHaveBeenCalledWith('p/a.md')
    expect(w.emitted('changed')).toHaveLength(3)
    w.unmount()
  })

  it('shows the MCP choice, even one the note no longer has', async () => {
    vi.mocked(getVersions).mockResolvedValue(list({ mcp: { tag: 'note/p/a.md/V0', label: 'V0', missing: true } }))
    const w = await openPanel()
    expect((w.find('[data-version-mcp]').element as HTMLSelectElement).value).toBe('note/p/a.md/V0')
    expect(w.find('[data-version-mcp-missing]').text()).toContain('V0')
    w.unmount()
  })

  it('proposes the next name', async () => {
    const w = await openPanel()
    expect((w.find('[data-version-name]').element as HTMLInputElement).value).toBe('V3')
    w.unmount()
    vi.mocked(getVersions).mockResolvedValue(list({ items: [tag('bozza-gennaio')] }))
    const none = await openPanel()
    expect((none.find('[data-version-name]').element as HTMLInputElement).value).toBe('V1')
    none.unmount()
  })

  it('pins the version as it is in one step', async () => {
    const w = await openPanel()
    expect(w.find('[data-version-pin]').text()).toBe('Pin this version')
    await w.find('[data-version-form]').trigger('submit')
    await flushPromises()
    expect(tagVersion).toHaveBeenCalledWith('p/a.md', 'V3', '')
    expect(setValidVersion).toHaveBeenCalledWith('p/a.md', 'note/p/a.md/V3')
    expect(w.emitted('changed')).toHaveLength(1)
    w.unmount()
  })

  it('names a version without pinning it', async () => {
    const w = await openPanel()
    await w.find('[data-version-tag]').trigger('click')
    await flushPromises()
    expect(tagVersion).toHaveBeenCalledWith('p/a.md', 'V3', '')
    expect(setValidVersion).not.toHaveBeenCalled()
    w.unmount()
  })

  it('saves first what is not saved, so that what is pinned is what shows', async () => {
    const save = vi.fn(async () => true)
    const w = await openPanel({ dirty: true, save })
    expect(w.find('[data-version-pin]').text()).toBe('Save and pin')
    await w.find('[data-version-form]').trigger('submit')
    await flushPromises()
    expect(save).toHaveBeenCalledTimes(1)
    expect(vi.mocked(save).mock.invocationCallOrder[0]!).toBeLessThan(vi.mocked(tagVersion).mock.invocationCallOrder[0]!)
    w.unmount()
  })

  it('pins nothing when the save fails', async () => {
    const w = await openPanel({ dirty: true, save: async () => false })
    await w.find('[data-version-form]').trigger('submit')
    await flushPromises()
    expect(tagVersion).not.toHaveBeenCalled()
    expect(w.find('[data-version-error]').text()).toContain('Not saved')
    w.unmount()
  })

  it('gives another commit a name, and pins that', async () => {
    const sha = 'c'.repeat(40)
    vi.mocked(getHistory).mockResolvedValue([
      { sha, short_sha: 'ccccccc', author: 'a', date: '2026-11-01T10:00:00Z', subject: 'a change', path: 'p/a.md' },
    ])
    const w = await openPanel()
    await w.find('[data-version-commits]').trigger('click')
    await flushPromises()
    expect(getHistory).toHaveBeenCalledWith('p/a.md', 20)
    await w.find(`[data-version-commit="${sha}"]`).trigger('click')
    expect(w.find('[data-version-pin]').text()).toBe('Pin ccccccc')
    await w.find('[data-version-form]').trigger('submit')
    await flushPromises()
    expect(tagVersion).toHaveBeenCalledWith('p/a.md', 'V3', sha)
    w.unmount()
  })

  it('shows the state to who is no admin, and no way to change it', async () => {
    vi.mocked(getVersions).mockResolvedValue(list({ can_admin: false }))
    const w = await openPanel()
    expect(w.find('[data-version-form]').exists()).toBe(false)
    expect(w.find('[data-version-latest]').attributes('disabled')).toBeDefined()
    expect(w.find('[data-version-mcp]').attributes('disabled')).toBeDefined()
    expect(w.find('[data-version-admin-only]').exists()).toBe(true)
    expect(w.find('[data-version-open]').exists()).toBe(true)
    w.unmount()
  })

  it('tells why a change failed, with the panel open', async () => {
    vi.mocked(tagVersion).mockRejectedValue(
      new AxiosError('x', '409', undefined, undefined, {
        status: 409,
        data: { error: { message: 'label taken' } },
        statusText: '',
        headers: {},
        config: { headers: new AxiosHeaders() },
      }),
    )
    const w = await openPanel()
    await w.find('[data-version-form]').trigger('submit')
    await flushPromises()
    expect(w.find('[data-version-error]').text()).toContain('label taken')
    expect(w.find('[data-version-panel]').exists()).toBe(true)
    w.unmount()
  })

  it('opens a version and the history for who asks', async () => {
    const w = await openPanel()
    await w.find('[data-version-open]').trigger('click')
    expect(w.emitted('open')?.[0]?.[0]).toMatchObject({ label: 'V2' })
    await w.find('[data-version-history]').trigger('click')
    expect(w.emitted('history')).toHaveLength(1)
    w.unmount()
  })

  it('stays inside the window that clips it', async () => {
    const box = (left: number, right: number, top: number, bottom: number) =>
      ({ left, right, top, bottom, width: right - left, height: bottom - top }) as DOMRect
    const window_ = document.createElement('div')
    window_.style.overflowX = 'hidden'
    document.body.append(window_)
    // As a browser does, the panel measures where its transform moved it.
    const rects = vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(function (this: HTMLElement) {
      const moved = Number(/translateX\((\d+)px/.exec(this.style.transform)?.[1] ?? 0)
      return box(40 + moved, 360 + moved, 120, 300)
    })
    window_.getBoundingClientRect = () => box(100, 500, 50, 600)
    const w = mount(NoteVersionButton, { props: { path: 'p/a.md' }, global: { plugins: [i18n()] }, attachTo: window_ })
    await w.find('[data-version-button]').trigger('click')
    await flushPromises()
    const style = (w.find('[data-version-panel]').element as HTMLElement).style
    expect(style.transform).toBe('translateX(68px)')
    expect(style.maxWidth).toBe('384px')
    expect(style.maxHeight).toBe('472px')
    await w.find('[data-version-button]').trigger('click')
    await w.find('[data-version-button]').trigger('click')
    await flushPromises()
    const again = (w.find('[data-version-panel]').element as HTMLElement).style
    expect(again.transform).toBe('translateX(68px)')
    w.unmount()
    window_.remove()
    rects.mockRestore()
  })

  it('closes on Escape, with the focus back on the button', async () => {
    const w = await openPanel()
    await w.find('[data-version-panel]').trigger('keydown', { key: 'Escape' })
    expect(w.find('[data-version-panel]').exists()).toBe(false)
    expect(document.activeElement).toBe(w.find('[data-version-button]').element)
    w.unmount()
  })
})
