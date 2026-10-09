import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import type { TreeNode } from '@/api/tree'

const writable = new Set<string>()
const accessState = { trash: true as boolean | null }
const windows = {
  windows: [] as { id: string; type: string; dirty: boolean; props: Record<string, unknown> }[],
  close: vi.fn(),
}
const tree = { refresh: vi.fn() }

vi.mock('@/api/notes', () => ({ deleteNote: vi.fn() }))
vi.mock('@/api/folders', () => ({ deleteFolder: vi.fn(), exportFolder: vi.fn() }))
vi.mock('@/api/projects', () => ({ exportProject: vi.fn() }))
vi.mock('@/api/noteDownload', async (orig) => ({
  ...(await orig<typeof import('@/api/noteDownload')>()),
  downloadNote: vi.fn(),
}))
vi.mock('@/stores/access', () => ({
  useAccessStore: () => ({
    canWrite: (p: string) => writable.has(p.split('/')[0]!),
    get trash() {
      return accessState.trash
    },
    get deleteNoteKey() {
      return accessState.trash === true
        ? 'tree.menu.confirm_delete_note'
        : accessState.trash === false
          ? 'tree.menu.confirm_delete_note_forever'
          : 'tree.menu.confirm_delete_note_plain'
    },
  }),
}))
vi.mock('@/stores/tree', () => ({ useTreeStore: () => tree }))
vi.mock('plancia', () => ({ useWindowsStore: () => windows }))

import { deleteNote } from '@/api/notes'
import { deleteFolder, exportFolder } from '@/api/folders'
import { exportProject } from '@/api/projects'
import { downloadNote } from '@/api/noteDownload'
import { useTreeMenu } from '@/composables/useTreeMenu'
import TreeContextMenu from '@/components/domain/TreeContextMenu.vue'

const note: TreeNode = { name: 'a.md', path: 'Alpha/sub/a.md', is_dir: false, kind: 'note' }
const base: TreeNode = { name: 'b.base', path: 'Alpha/b.base', is_dir: false, kind: 'base' }
const folder: TreeNode = { name: 'sub', path: 'Alpha/sub', is_dir: true, kind: 'folder', note_count: 2 }
const project: TreeNode = { name: 'Alpha', path: 'Alpha', is_dir: true, is_project_root: true, kind: 'folder' }

const mounted: { unmount(): void }[] = []

async function openOn(node: TreeNode, trigger: HTMLElement | null = null) {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
  const wrapper = mount(TreeContextMenu, {
    attachTo: document.body,
    // The real Teleport: the stub re-creates its content at every render,
    // which would drop the focus.
    global: { plugins: [i18n] },
  })
  mounted.push(wrapper)
  useTreeMenu().open(node, 10, 20, trigger)
  await flushPromises()
  return wrapper
}

function buttons(): HTMLButtonElement[] {
  return [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')]
}
function labels(): string[] {
  return buttons().map((b) => b.textContent?.trim() ?? '')
}
async function click(i: number) {
  buttons()[i]!.click()
  await flushPromises()
}
async function key(k: string) {
  document.querySelector('[role="menu"]')!.dispatchEvent(new KeyboardEvent('keydown', { key: k, bubbles: true }))
  await flushPromises()
}

describe('TreeContextMenu', () => {
  beforeEach(() => {
    writable.clear()
    accessState.trash = true
    windows.windows = []
    vi.clearAllMocks()
  })
  afterEach(() => {
    useTreeMenu().close()
    mounted.splice(0).forEach((w) => w.unmount())
    document.body.innerHTML = ''
  })

  it('offers download and delete on a note it may write', async () => {
    writable.add('Alpha')
    await openOn(note)
    expect(labels()).toEqual(['Download', 'Delete'])
    await click(0)
    expect(downloadNote).toHaveBeenCalledWith('Alpha/sub/a.md')
  })

  it('leaves out delete where the account only reads, and on a base', async () => {
    await openOn(note)
    expect(labels()).toEqual(['Download'])
    writable.add('Alpha')
    useTreeMenu().open(base, 10, 20, null)
    await flushPromises()
    expect(labels()).toEqual(['Download'])
  })

  it('downloads a project as its zip, with no delete', async () => {
    writable.add('Alpha')
    await openOn(project)
    expect(labels()).toEqual(['Download as zip'])
    await click(0)
    expect(exportProject).toHaveBeenCalledWith('Alpha')
    expect(exportFolder).not.toHaveBeenCalled()
  })

  it('trashes a folder after a confirmation naming its notes, and closes their windows', async () => {
    writable.add('Alpha')
    windows.windows = [
      { id: 'w1', type: 'note', dirty: false, props: { path: 'Alpha/sub/a.md' } },
      { id: 'w2', type: 'note', dirty: true, props: { path: 'Alpha/sub/b.md' } },
      { id: 'w3', type: 'note', dirty: false, props: { path: 'Alpha/keep.md' } },
    ]
    vi.mocked(deleteFolder).mockResolvedValue({ trash_id: 'x', removed: ['Alpha/sub/a.md', 'Alpha/sub/b.md'] })
    const confirm = vi.fn(() => true)
    window.confirm = confirm
    await openOn(folder)
    expect(labels()).toEqual(['Download as zip', 'Delete'])
    await click(1)
    expect(String((confirm.mock.calls[0] as unknown[])[0])).toContain('its 2 notes')
    expect(deleteFolder).toHaveBeenCalledWith('Alpha/sub')
    // The dirty window keeps its draft.
    expect(windows.close.mock.calls).toEqual([['w1']])
    expect(tree.refresh).toHaveBeenCalled()
  })

  it('says a delete is for good without the trash, and offers none on a folder (BUG-116, S6-3)', async () => {
    writable.add('Alpha')
    accessState.trash = false
    const confirm = vi.fn(() => false)
    window.confirm = confirm
    await openOn(note)
    await click(1)
    expect(String((confirm.mock.calls[0] as unknown[])[0])).toContain('for good')
    useTreeMenu().open(folder, 10, 20, null)
    await flushPromises()
    expect(labels()).toEqual(['Download as zip'])
  })

  it('does nothing when the confirmation is declined', async () => {
    writable.add('Alpha')
    window.confirm = vi.fn(() => false)
    await openOn(note)
    await click(1)
    expect(deleteNote).not.toHaveBeenCalled()
  })

  it('says why a delete failed', async () => {
    writable.add('Alpha')
    window.confirm = vi.fn(() => true)
    const alert = vi.fn()
    window.alert = alert
    vi.mocked(deleteNote).mockRejectedValue(new Error('locked'))
    await openOn(note)
    await click(1)
    expect(alert).toHaveBeenCalledWith('locked')
  })

  it('moves with the arrows and gives the focus back to the row on Esc', async () => {
    writable.add('Alpha')
    const row = document.createElement('button')
    document.body.appendChild(row)
    await openOn(note, row)
    const items = buttons()
    expect(document.activeElement).toBe(items[0])
    await key('ArrowDown')
    expect(document.activeElement).toBe(items[1])
    await key('ArrowDown')
    expect(document.activeElement).toBe(items[0])
    await key('Escape')
    expect(document.querySelector('[role="menu"]')).toBeNull()
    expect(document.activeElement).toBe(row)
  })

  it('closes on a click outside', async () => {
    await openOn(note)
    document.body.dispatchEvent(new Event('pointerdown', { bubbles: true }))
    await flushPromises()
    expect(document.querySelector('[role="menu"]')).toBeNull()
  })
})
