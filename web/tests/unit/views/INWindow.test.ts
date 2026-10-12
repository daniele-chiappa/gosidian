import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { createPinia, setActivePinia } from 'pinia'
import enUI from '@catalogs/ui.en.json'
import type { INFile, INListing } from '@/api/in'

const sseListeners = vi.hoisted(() => new Map<string, (p: Record<string, unknown>) => void>())

vi.mock('@/api/in', async (orig) => ({
  ...(await orig<typeof import('@/api/in')>()),
  listIN: vi.fn(),
  moveIN: vi.fn(),
  setINStatus: vi.fn(),
  deleteIN: vi.fn(),
  runIN: vi.fn(),
  scanIN: vi.fn(),
}))
vi.mock('@/api/pipelines', async (orig) => ({
  ...(await orig<typeof import('@/api/pipelines')>()),
  listPipelines: vi.fn(),
}))
vi.mock('@/api/folders', () => ({ exportFolder: vi.fn(async () => {}) }))
vi.mock('@/composables/useSSE', () => ({
  useSSE: () => ({ on: (topic: string, fn: (p: Record<string, unknown>) => void) => (sseListeners.set(topic, fn), () => {}) }),
}))
vi.mock('@/composables/useConfirm', () => ({ confirmAction: vi.fn(async () => true) }))
vi.mock('@/stores/access', () => ({
  useAccessStore: () => ({ canWrite: (p: string) => p.startsWith('p/'), trash: true }),
}))
vi.mock('@/stores/tree', () => ({ useTreeStore: () => ({ refresh: vi.fn() }) }))
vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => ({ windows: [], open: vi.fn() }),
}))

import { deleteIN, listIN, moveIN, runIN, scanIN, setINStatus, type INSource } from '@/api/in'
import { listPipelines } from '@/api/pipelines'
import { confirmAction } from '@/composables/useConfirm'
import { useUIStore } from '@/stores/ui'
import INWindow from '@/views/INWindow.vue'

const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })

const file = (path: string, over: Partial<INFile> = {}): INFile => {
  const name = path.split('/').pop()!
  return {
    project: 'p', path, name, stem: name.replace(/\.[^.]*$/, ''), ext: name.split('.').pop()!, dir: '',
    size: 1024, modified: '2026-10-11T10:00:00Z', status: 'new', source: 'local',
    first_seen: '2026-10-11T10:00:00Z', updated: '2026-10-11T10:00:00Z', ...over,
  }
}

const listing = (over: Partial<INListing> = {}): INListing => ({
  project: 'p', root: 'p/IN', dir: '', next: '', track_in_git: false, sources: [],
  counts: { files: 3, bytes: 3072, new: 2, working: 1, errors: 0 },
  folders: [{ path: 'fatture', files: 2, bytes: 2048, new: 1 }],
  files: [file('p/IN/a.pdf'), file('p/IN/b.xml', { status: 'working', run: 'abcdef1234' })],
  ...over,
})

async function mountWindow() {
  const w = mount(INWindow, { props: { project: 'p' }, global: { plugins: [i18n()] } })
  await flushPromises()
  return w
}

describe('INWindow (IMP-172)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    useUIStore().pipelinesEnabled = true
    sseListeners.clear()
    vi.mocked(listIN).mockReset().mockResolvedValue(listing())
    vi.mocked(listPipelines).mockResolvedValue([
      { path: 'p/pipelines/lot.md', title: 'Lot', project: 'p', trigger: 'in' },
      { path: 'p/pipelines/bad.md', title: 'Bad', project: 'p', problem: 'no' },
    ])
    vi.mocked(moveIN).mockReset().mockResolvedValue({ path: 'p/IN/done/a.pdf', from: 'p/IN/a.pdf' })
    vi.mocked(deleteIN).mockReset().mockResolvedValue({ removed: ['p/IN/a.pdf'], trash: true })
    vi.mocked(runIN).mockReset().mockResolvedValue({
      runs: [{ id: 'run1', pipeline: 'p/pipelines/lot.md', project: 'p', status: 'queued', trigger: 'manual', actor: 'o', created: '' }],
      skipped: [{ path: 'p/IN/b.xml', reason: 'kinds', value: 'pdf', why: 'not of the kinds pdf' }],
    })
    vi.mocked(setINStatus).mockReset().mockResolvedValue()
    vi.mocked(scanIN).mockReset().mockResolvedValue()
  })

  it('shows a folder at a time: its subfolders, its files with their status and run', async () => {
    const w = await mountWindow()
    expect(w.find('[data-in-counts]').text()).toContain('3 files')
    expect(w.find('[data-in-folder="fatture"]').exists()).toBe(true)
    expect(w.findAll('[data-in-file]').map((r) => r.attributes('data-in-file'))).toEqual(['p/IN/a.pdf', 'p/IN/b.xml'])
    expect(w.find('[data-in-file="p/IN/b.xml"] [data-in-status]').text()).toBe('in progress')
    expect(w.find('[data-in-file="p/IN/b.xml"]').text()).toContain('abcdef12')
    expect(w.find('[data-in-file="p/IN/a.pdf"] a[download]').attributes('href')).toBe('/vault-files/p/IN/a.pdf')

    vi.mocked(listIN).mockResolvedValueOnce(listing({ dir: 'fatture', folders: [], files: [file('p/IN/fatture/c.pdf', { dir: 'fatture' })] }))
    await w.find('[data-in-folder="fatture"] [data-in-open]').trigger('click')
    await flushPromises()
    expect(vi.mocked(listIN).mock.calls.at(-1)).toEqual(['p', expect.objectContaining({ dir: 'fatture', recursive: false })])
    expect(w.findAll('[data-in-crumb]').map((c) => c.text())).toEqual(['IN', 'fatture'])
  })

  it('searches under the folder', async () => {
    const w = await mountWindow()
    await w.find('[data-in-search]').setValue('fatt')
    await flushPromises()
    expect(vi.mocked(listIN).mock.calls.at(-1)).toEqual(['p', expect.objectContaining({ recursive: true, match: '*fatt*' })])
  })

  it('moves, sets aside, deletes and starts a pipeline on the files chosen', async () => {
    const w = await mountWindow()
    expect(w.find('[data-in-actions]').exists()).toBe(false)
    await w.find('[data-in-file="p/IN/a.pdf"] [data-in-select]').setValue(true)
    expect(w.find('[data-in-actions]').exists()).toBe(true)

    await w.find('[data-in-move-target]').setValue('done')
    await w.find('[data-in-action="move"]').trigger('click')
    await flushPromises()
    expect(moveIN).toHaveBeenCalledWith('p', 'p/IN/a.pdf', 'done/')

    await w.find('[data-in-file="p/IN/a.pdf"] [data-in-select]').setValue(true)
    await w.find('[data-in-action="ignore"]').trigger('click')
    await flushPromises()
    expect(setINStatus).toHaveBeenCalledWith('p', ['p/IN/a.pdf'], 'ignored')

    // Only the pipelines the server would run are offered.
    expect(w.findAll('[data-in-pipeline] option').map((o) => o.text())).toEqual(['Choose a pipeline…', 'Lot'])
    await w.find('[data-in-pipeline]').setValue('p/pipelines/lot.md')
    await w.find('[data-in-action="run"]').trigger('click')
    await flushPromises()
    expect(runIN).toHaveBeenCalledWith('p', 'p/pipelines/lot.md', ['p/IN/a.pdf'])
    expect(w.find('[data-in-message]').text()).toContain('run1')
    expect(w.find('[data-in-message]').text()).toContain('b.xml skipped: the pipeline takes pdf only.')

    await w.find('[data-in-action="delete"]').trigger('click')
    await flushPromises()
    expect(confirmAction).toHaveBeenCalled()
    expect(deleteIN).toHaveBeenCalledWith('p', ['p/IN/a.pdf'])
  })

  it('reloads when its IN folder changes, not another one', async () => {
    vi.useFakeTimers()
    try {
      await mountWindow()
      const calls = vi.mocked(listIN).mock.calls.length
      sseListeners.get('tree')!({ action: 'in', project: 'other' })
      sseListeners.get('tree')!({ action: 'create', path: 'p/x.md' })
      await vi.advanceTimersByTimeAsync(300)
      expect(vi.mocked(listIN).mock.calls.length).toBe(calls)
      sseListeners.get('tree')!({ action: 'in', project: 'p' })
      await vi.advanceTimersByTimeAsync(300)
      expect(vi.mocked(listIN).mock.calls.length).toBe(calls + 1)
    } finally {
      vi.useRealTimers()
    }
  })

  describe('a source bound as a folder (IMP-172 phase 2)', () => {
    const source = (over: Partial<INSource> = {}): INSource => ({
      folder: 'nc', plugin: 'storage', source: 'docs', kind: 'webdav', changes: true, move: true, write: false,
      status: 'ok', checked: '2026-10-12T10:00:00Z', every: 60, ...over,
    })
    const remote = (path: string) => file(path, { dir: path.split('/').slice(2, -1).join('/'), source: 'storage/docs', etag: 'e1' })

    it('lists the sources at the root, with their state, and checks one on request', async () => {
      vi.mocked(listIN).mockResolvedValue(listing({
        folders: [{ path: 'fatture', files: 2, bytes: 2048, new: 1 }, { path: 'nc', files: 1, bytes: 10, new: 1 }],
        sources: [source(), source({ folder: 'drive', source: 'drive', status: 'error', error: 'the archive is down' })],
      }))
      const w = await mountWindow()
      expect(w.find('[data-in-source="nc"] [data-in-source-status]').text()).toBe('up to date')
      expect(w.find('[data-in-source="drive"] [data-in-source-error]').text()).toBe('the archive is down')
      // Its folder row: a cloud, no zip, no delete.
      expect(w.find('[data-in-folder="nc"] [data-in-cloud]').exists()).toBe(true)
      expect(w.find('[data-in-delete-folder="nc"]').exists()).toBe(false)
      expect(w.find('[data-in-delete-folder="fatture"]').exists()).toBe(true)
      await w.find('[data-in-refresh="nc"]').trigger('click')
      await flushPromises()
      expect(scanIN).toHaveBeenCalledWith('p', 'nc')
      expect(w.find('[data-in-message]').text()).toContain('Check started')
    })

    it('inside a read-only source: no delete, no move', async () => {
      vi.mocked(listIN).mockResolvedValue(listing({ folders: [{ path: 'nc', files: 1, bytes: 10, new: 1 }], sources: [source()], files: [] }))
      const w = await mountWindow()
      vi.mocked(listIN).mockResolvedValue(listing({ dir: 'nc', folders: [], sources: [source()], files: [remote('p/IN/nc/a.pdf')] }))
      await w.find('[data-in-folder="nc"] [data-in-open]').trigger('click')
      await flushPromises()
      expect(w.find('[data-in-sources]').exists()).toBe(false)
      expect(w.find('[data-in-source-banner]').text()).toContain('Read-only')
      await w.find('[data-in-file="p/IN/nc/a.pdf"] [data-in-select]').setValue(true)
      expect(w.find('[data-in-action="delete"]').exists()).toBe(false)
      expect(w.find('[data-in-no-delete]').text()).toContain('not deleted from here')
      await w.find('[data-in-move-target]').setValue('done')
      expect(w.find('[data-in-action="move"]').attributes('disabled')).toBeDefined()
      expect(w.find('[data-in-action="move"]').attributes('title')).toContain('read-only')
    })

    it('inside a source that moves: the files move inside it', async () => {
      const src = source({ write: true })
      vi.mocked(listIN).mockResolvedValue(listing({ folders: [{ path: 'nc', files: 1, bytes: 10, new: 1 }], sources: [src], files: [] }))
      const w = await mountWindow()
      vi.mocked(listIN).mockResolvedValue(listing({ dir: 'nc', folders: [], sources: [src], files: [remote('p/IN/nc/a.pdf')] }))
      await w.find('[data-in-source-open="nc"]').trigger('click')
      await flushPromises()
      expect(w.find('[data-in-source-banner]').text()).toContain('storage/docs')
      expect(w.find('[data-in-source-banner]').text()).toContain('move inside the source')
      await w.find('[data-in-file="p/IN/nc/a.pdf"] [data-in-select]').setValue(true)
      await w.find('[data-in-move-target]').setValue('done')
      await w.find('[data-in-action="move"]').trigger('click')
      await flushPromises()
      expect(moveIN).toHaveBeenCalledWith('p', 'p/IN/nc/a.pdf', 'done/')
    })
  })
})
