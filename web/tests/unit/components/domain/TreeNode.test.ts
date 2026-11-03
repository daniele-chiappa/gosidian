import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import type { TreeNode as TN } from '@/api/tree'

const open = vi.hoisted(() => vi.fn())
const menuOpen = vi.hoisted(() => vi.fn())
const writable = vi.hoisted(() => ({ value: false }))
const record = vi.hoisted(() => vi.fn())
vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => ({ open }),
  usePlanciaFocus: () => ({ focusWindow: vi.fn(async () => true) }),
}))
vi.mock('@/stores/access', () => ({
  useAccessStore: () => ({ canWrite: () => writable.value, visibility: () => 'internal' }),
}))
vi.mock('@/composables/useRecentlyViewed', () => ({ useRecentlyViewed: () => ({ record }) }))
vi.mock('@/composables/useTreeMenu', () => ({ useTreeMenu: () => ({ open: menuOpen }) }))

import TreeNode from '@/components/domain/TreeNode.vue'

const dir = (path: string, children: TN[] = []): TN => ({
  name: path.split('/').pop()!,
  path,
  is_dir: true,
  kind: 'dir',
  children,
})
const note = (path: string): TN => ({ name: path.split('/').pop()!, path, is_dir: false, kind: 'note' })

const i18n = () => createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
const mountNode = (node: TN) => mount(TreeNode, { props: { node }, global: { plugins: [i18n()] }, attachTo: document.body })

describe('TreeNode', () => {
  beforeEach(() => {
    localStorage.clear()
    writable.value = false
    open.mockClear()
    menuOpen.mockClear()
    record.mockClear()
  })

  it('turns the chevron of an open folder only, not of the closed folders inside it', async () => {
    localStorage.setItem('gosidian.tree.open:p/docs', '1')
    const w = mountNode(dir('p/docs', [dir('p/docs/bugs', [dir('p/docs/bugs/old')])]))
    const chevrons = () => w.findAll('button[aria-expanded] > svg').map((c) => c.classes('rotate-90'))
    // The closed subfolder draws nothing inside it.
    expect(chevrons()).toEqual([true, false])

    // Opening the subfolder turns its own chevron.
    await w.findAll('button[aria-expanded]')[1]!.trigger('click')
    expect(chevrons()).toEqual([true, true, false])
    expect(localStorage.getItem('gosidian.tree.open:p/docs/bugs')).toBe('1')
  })

  // F-08: a folder is a button that opens and closes it, and its + sits
  // beside it, not inside: no control in a control (axe nested-interactive).
  it('opens and closes a folder with a button, the + beside it, and draws a closed folder empty', async () => {
    writable.value = true
    const w = mountNode(dir('p/docs', [note('p/docs/a.md')]))
    const toggle = w.find('button[aria-expanded]')
    expect(toggle.attributes('aria-expanded')).toBe('false')
    expect(w.find('li li').exists()).toBe(false)
    expect(w.findAll('button').every((b) => !b.element.querySelector('button, a, input'))).toBe(true)
    const plus = w.find('button[aria-label="New note in this folder"]')
    expect(plus.element.parentElement).toBe(toggle.element.parentElement)

    await toggle.trigger('click')
    expect(toggle.attributes('aria-expanded')).toBe('true')
    expect(w.find('li li button').text()).toBe('a')
    expect(localStorage.getItem('gosidian.tree.open:p/docs')).toBe('1')

    await toggle.trigger('click')
    expect(toggle.attributes('aria-expanded')).toBe('false')
    expect(w.find('li li').exists()).toBe(false)
  })

  // F-18: the + is 24 px, shows with the pointer on the row or the focus in
  // it, and always where nothing hovers (a touch screen).
  it('makes the + a 24 px target that shows with the focus in its row', async () => {
    writable.value = true
    const w = mountNode(dir('p/docs'))
    const plus = w.find('button[aria-label="New note in this folder"]')
    expect(plus.classes()).toEqual(expect.arrayContaining(['h-6', 'w-6']))
    expect(plus.classes()).toEqual(
      expect.arrayContaining(['opacity-0', 'group-hover/row:opacity-100', 'group-focus-within/row:opacity-100', '[@media(hover:none)]:opacity-100']),
    )
    expect(plus.element.parentElement!.classList.contains('group/row')).toBe(true)

    await plus.trigger('click')
    expect(open).toHaveBeenCalledWith(expect.objectContaining({ type: 'create', props: { path: 'p/docs' } }))
    expect(w.find('button[aria-expanded]').attributes('aria-expanded')).toBe('false')
  })

  // The rows open a note, a folder or the IN window: none says it opens a
  // menu. The context menu stays on the right click, Menu and Shift+F10.
  it('has no row that says it opens a menu, and Shift+F10 still opens the context menu', async () => {
    localStorage.setItem('gosidian.tree.open:p', '1')
    const folder = dir('p', [note('p/a.md'), { name: 'IN', path: 'p/IN', is_dir: true, kind: 'in', in: { files: 0, bytes: 0, new: 0, working: 0, errors: 0 } }])
    const w = mountNode(folder)
    expect(w.findAll('[aria-haspopup]')).toHaveLength(0)

    const toggle = w.find('button[aria-expanded]')
    await toggle.trigger('keydown', { key: 'F10', shiftKey: true })
    expect(menuOpen).toHaveBeenCalledWith(folder, expect.any(Number), expect.any(Number), toggle.element)

    const row = w.find('li li > button')
    await row.trigger('keydown', { key: 'ContextMenu' })
    expect(menuOpen).toHaveBeenLastCalledWith(folder.children![0], expect.any(Number), expect.any(Number), row.element)
  })

  // F-19: a note's row shows its title, its file name on hover, and its
  // window starts with the title; a note without one keeps its file name.
  it('shows a note by its title, its file name on hover, and opens its window with the title', async () => {
    const path = 'p/tasks/20261031-004154-nota-delta.md'
    const w = mountNode({ ...note(path), title: 'Nota delta' })
    const row = w.find('li > button')
    expect(row.text()).toBe('Nota delta')
    expect(row.find('[title]').attributes('title')).toBe('20261031-004154-nota-delta.md')
    await row.trigger('click')
    expect(open).toHaveBeenCalledWith(expect.objectContaining({ type: 'note', title: 'Nota delta', props: { path } }))
    expect(record).toHaveBeenCalledWith(path, 'Nota delta')

    await mountNode(note('p/plain.md')).find('li > button').trigger('click')
    expect(open).toHaveBeenLastCalledWith(expect.objectContaining({ title: 'plain', props: { path: 'p/plain.md' } }))
  })

  it('draws the IN folder of a project as one row with what it holds, which opens the IN window', async () => {
    const w = mountNode({ name: 'IN', path: 'p/IN', is_dir: true, kind: 'in', in: { files: 12, bytes: 4096, new: 3, working: 0, errors: 0 } })
    const row = w.find('[data-tree-in]')
    expect(row.text()).toContain('IN')
    expect(row.text()).toContain('3')
    expect(row.text()).toContain('12')
    expect(w.find('button[aria-expanded]').exists()).toBe(false)
    await row.trigger('click')
    expect(open).toHaveBeenCalledWith(expect.objectContaining({ type: 'in', key: 'in:p', props: { project: 'p' } }))
  })
})
