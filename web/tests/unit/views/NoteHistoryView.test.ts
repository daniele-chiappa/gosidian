import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'

vi.mock('@/api/history', () => ({
  getHistory: vi.fn(),
  getVersion: vi.fn(),
  restoreVersion: vi.fn(),
  getVersions: vi.fn(),
  tagVersion: vi.fn(),
  setValidVersion: vi.fn(),
  clearValidVersion: vi.fn(),
}))
vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => ({ open: vi.fn() }),
}))
let writable = true
vi.mock('@/stores/access', () => ({ useAccessStore: () => ({ canWrite: () => writable }) }))
vi.mock('@/composables/useConfirm', () => ({ confirmAction: vi.fn() }))
vi.mock('@/composables/useAnnounce', () => ({ announce: vi.fn() }))

import {
  clearValidVersion,
  getHistory,
  getVersion,
  getVersions,
  restoreVersion,
  setValidVersion,
  tagVersion,
  type HistoryEntry,
  type NoteTag,
  type Version,
} from '@/api/history'
import { confirmAction } from '@/composables/useConfirm'
import { announce } from '@/composables/useAnnounce'
import NoteHistoryView from '@/views/NoteHistoryView.vue'

const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })

const entry = (sha: string, subject: string): HistoryEntry => ({
  sha,
  short_sha: sha.slice(0, 7),
  author: 'daniele <gosidian@localhost>',
  date: '2026-11-01T10:00:00Z',
  subject,
  path: 'p/a.md',
})
const now = entry('a'.repeat(40), 'update p/a.md')
const yesterday = entry('b'.repeat(40), 'create p/a.md')
const version = (over: Partial<Version> = {}): Version => ({
  ...yesterday,
  content: 'old line\n',
  diff: '@@ -1 +1 @@\n-old line\n+new line\n',
  same: false,
  etag: 'E1',
  ...over,
})

function mountView() {
  return mount(NoteHistoryView, { props: { path: 'p/a.md' }, global: { plugins: [i18n()] }, attachTo: document.body })
}

// The buttons of the versions, newest first.
const versionButtons = (w: ReturnType<typeof mountView>) => w.findAll('button[data-sha]')

describe('NoteHistoryView (IMP-222)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    writable = true
    vi.mocked(getHistory).mockResolvedValue([now, yesterday])
    vi.mocked(getVersions).mockResolvedValue({ items: [], valid: null, can_admin: false })
  })

  it('opens the version the panel of the versions asked for (IMP-228)', async () => {
    vi.mocked(getVersion).mockResolvedValue(version())
    const w = mount(NoteHistoryView, {
      props: { path: 'p/a.md', sha: yesterday.sha, shaAt: 1 },
      global: { plugins: [i18n()] },
      attachTo: document.body,
    })
    await flushPromises()
    expect(getVersion).toHaveBeenCalledWith('p/a.md', yesterday.sha)
    expect(versionButtons(w)[1]!.attributes('aria-expanded')).toBe('true')
    w.unmount()
  })

  it('opens a version on a click: the diff first, then its text', async () => {
    vi.mocked(getVersion).mockResolvedValue(version())
    const w = mountView()
    await flushPromises()
    expect(w.text()).toContain('daniele')
    expect(w.text()).not.toContain('gosidian@localhost')
    const b = versionButtons(w)[1]!
    expect(b.attributes('aria-expanded')).toBe('false')
    await b.trigger('click')
    await flushPromises()
    expect(getVersion).toHaveBeenCalledWith('p/a.md', yesterday.sha)
    expect(b.attributes('aria-expanded')).toBe('true')
    expect(w.text()).toContain('From this version to now')
    const removed = w.find('[data-line="removed"]')
    expect(removed.text()).toContain('removed')
    expect(removed.text()).toContain('old line')
    expect(w.find('[data-line="added"]').text()).toContain('new line')
    expect(w.find('[data-version-text]').exists()).toBe(false)

    const show = w.findAll('button').find((x) => x.text() === 'Show the text of this version')!
    await show.trigger('click')
    expect(show.attributes('aria-expanded')).toBe('true')
    expect(w.find('[data-version-text]').text()).toBe('old line')

    // A second click closes it.
    await b.trigger('click')
    expect(b.attributes('aria-expanded')).toBe('false')
    expect(w.text()).not.toContain('From this version to now')
    w.unmount()
  })

  it('offers the restore only to who may write the note', async () => {
    vi.mocked(getVersion).mockResolvedValue(version())
    writable = false
    const w = mountView()
    await flushPromises()
    await versionButtons(w)[1]!.trigger('click')
    await flushPromises()
    expect(w.text()).not.toContain('Restore this version')
    w.unmount()
  })

  it('restores after a confirmation that starts on Cancel, then reads the history again', async () => {
    vi.mocked(getVersion).mockResolvedValue(version())
    const restored = entry('c'.repeat(40), 'restore p/a.md to bbbbbbb')
    vi.mocked(restoreVersion).mockResolvedValue({ restored: true, commit: restored })
    const w = mountView()
    await flushPromises()
    await versionButtons(w)[1]!.trigger('click')
    await flushPromises()

    vi.mocked(confirmAction).mockResolvedValueOnce(false)
    const restore = () => w.findAll('button').find((x) => x.text() === 'Restore this version')!
    await restore().trigger('click')
    await flushPromises()
    expect(restoreVersion).not.toHaveBeenCalled()
    // danger, the default: the dialog opens on Cancel.
    expect(vi.mocked(confirmAction).mock.calls[0]![1]?.danger).not.toBe(false)

    vi.mocked(getHistory).mockResolvedValue([restored, now, yesterday])
    vi.mocked(confirmAction).mockResolvedValueOnce(true)
    await restore().trigger('click')
    await flushPromises()
    expect(restoreVersion).toHaveBeenCalledWith('p/a.md', yesterday.sha, 'E1')
    expect(announce).toHaveBeenCalledWith('Version restored')
    expect(getHistory).toHaveBeenCalledTimes(2)
    expect(w.text()).toContain('restore p/a.md to bbbbbbb')
    const top = versionButtons(w)[0]!
    expect(document.activeElement).toBe(top.element)
    expect(top.attributes('aria-expanded')).toBe('false')
    w.unmount()
  })

  it('says a note changed meanwhile, and reads the version again', async () => {
    vi.mocked(getVersion).mockResolvedValue(version())
    vi.mocked(confirmAction).mockResolvedValue(true)
    vi.mocked(restoreVersion).mockRejectedValue(Object.assign(new Error('note was modified since the last read'), { status: 412 }))
    const w = mountView()
    await flushPromises()
    await versionButtons(w)[1]!.trigger('click')
    await flushPromises()
    await w.findAll('button').find((x) => x.text() === 'Restore this version')!.trigger('click')
    await flushPromises()
    expect(w.text()).toContain('Restore failed: it changed elsewhere in the meantime.')
    expect(getVersion).toHaveBeenCalledTimes(2)
    expect(getHistory).toHaveBeenCalledTimes(1)
    w.unmount()
  })

  it('says why a version has nothing to show, and that the newest is the note', async () => {
    vi.mocked(getVersion).mockResolvedValueOnce(version({ content: undefined, diff: undefined, omitted: 'deleted' }))
    const w = mountView()
    await flushPromises()
    await versionButtons(w)[1]!.trigger('click')
    await flushPromises()
    expect(w.text()).toContain('In this version the note was deleted.')
    expect(w.text()).not.toContain('Restore this version')

    vi.mocked(getVersion).mockResolvedValueOnce(version({ ...now, diff: '', same: true, content: 'new line\n' }))
    await versionButtons(w)[0]!.trigger('click')
    await flushPromises()
    expect(w.text()).toContain('This version is the note as it is now.')
    expect(w.text()).not.toContain('Restore this version')
    expect(versionButtons(w)[1]!.attributes('aria-expanded')).toBe('false')
    w.unmount()
  })

  it('names the note as it was called then', async () => {
    vi.mocked(getHistory).mockResolvedValue([now, { ...yesterday, path: 'p/old.md' }])
    vi.mocked(getVersion).mockResolvedValue(version({ path: 'p/old.md' }))
    const w = mountView()
    await flushPromises()
    await versionButtons(w)[1]!.trigger('click')
    await flushPromises()
    expect(w.text()).toContain('Named p/old.md then')
    w.unmount()
  })
})

describe('NoteHistoryView, the named versions (IMP-201)', () => {
  const v1: NoteTag = { tag: 'note/p/a.md/V1', label: 'V1', sha: yesterday.sha, short_sha: 'bbbbbbb', date: '2026-11-01T10:00:00Z', path: 'p/a.md' }
  const valid = { tag: v1.tag, label: 'V1', sha: v1.sha, same: false }

  beforeEach(() => {
    vi.clearAllMocks()
    vi.mocked(getHistory).mockResolvedValue([now, yesterday])
    vi.mocked(getVersion).mockResolvedValue(version())
    vi.mocked(confirmAction).mockResolvedValue(true)
  })

  async function open(entry: number) {
    const w = mountView()
    await flushPromises()
    await versionButtons(w)[entry]!.trigger('click')
    await flushPromises()
    return w
  }

  it('shows the tags on their entries, and which one is valid', async () => {
    vi.mocked(getVersions).mockResolvedValue({ items: [v1], valid, can_admin: false })
    const w = mountView()
    await flushPromises()
    const tags = w.findAll('[data-tag]')
    expect(tags).toHaveLength(1)
    expect(tags[0]!.text()).toContain('V1')
    expect(tags[0]!.text()).toContain('pinned version')
    expect(versionButtons(w)[1]!.find('[data-tag]').exists()).toBe(true)
    expect(versionButtons(w)[0]!.find('[data-tag]').exists()).toBe(false)
  })

  it('offers nothing to change to who is no admin of the project', async () => {
    vi.mocked(getVersions).mockResolvedValue({ items: [v1], valid: null, can_admin: false })
    const w = await open(1)
    expect(w.find('[data-version-admin]').exists()).toBe(false)
  })

  it('names the version open, and reads the versions again', async () => {
    vi.mocked(getVersions).mockResolvedValue({ items: [], valid: null, can_admin: true })
    vi.mocked(tagVersion).mockResolvedValue(v1)
    const w = await open(1)
    expect(w.find('[data-tag-version]').attributes('disabled')).toBeDefined()
    await w.find('[data-tag-label]').setValue(' V1 ')
    await w.find('[data-tag-label]').element.closest('form')!.dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(tagVersion).toHaveBeenCalledWith('p/a.md', 'V1', yesterday.sha)
    expect(announce).toHaveBeenCalledWith('This version is now V1')
    expect(vi.mocked(getVersions).mock.calls.length).toBeGreaterThan(1)
  })

  it('says why a label was refused, and keeps it', async () => {
    vi.mocked(getVersions).mockResolvedValue({ items: [], valid: null, can_admin: true })
    vi.mocked(tagVersion).mockRejectedValue(new Error('taken'))
    const w = await open(1)
    await w.find('[data-tag-label]').setValue('V1')
    await w.find('[data-tag-label]').element.closest('form')!.dispatchEvent(new Event('submit', { cancelable: true }))
    await flushPromises()
    expect(w.text()).toContain('Could not name the version')
    expect((w.find('[data-tag-label]').element as HTMLInputElement).value).toBe('V1')
  })

  it('makes a tag valid after a confirmation, and not without it', async () => {
    vi.mocked(getVersions).mockResolvedValue({ items: [v1], valid: null, can_admin: true })
    const w = await open(1)
    vi.mocked(confirmAction).mockResolvedValueOnce(false)
    await w.find('[data-make-valid]').trigger('click')
    await flushPromises()
    expect(setValidVersion).not.toHaveBeenCalled()
    await w.find('[data-make-valid]').trigger('click')
    await flushPromises()
    expect(setValidVersion).toHaveBeenCalledWith('p/a.md', v1.tag)
    expect(announce).toHaveBeenCalledWith('V1 is pinned')
  })

  it('removes the valid version from its entry', async () => {
    vi.mocked(getVersions).mockResolvedValue({ items: [v1], valid, can_admin: true })
    const w = await open(1)
    expect(w.find('[data-make-valid]').exists()).toBe(false)
    await w.find('[data-clear-valid]').trigger('click')
    await flushPromises()
    expect(clearValidVersion).toHaveBeenCalledWith('p/a.md')
  })

  it('keeps the history when the versions do not read', async () => {
    vi.mocked(getVersions).mockRejectedValue(new Error('no'))
    const w = mountView()
    await flushPromises()
    expect(versionButtons(w)).toHaveLength(2)
  })
})
