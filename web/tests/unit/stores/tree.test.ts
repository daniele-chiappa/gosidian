import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import type { TreeNode } from '@/api/tree'

vi.mock('@/api/tree', () => ({ fetchTree: vi.fn() }))

import { fetchTree, type TreeAnswer } from '@/api/tree'
import { REFRESH_DELAY_MS, eventProject, useTreeStore } from '@/stores/tree'

const tree = (name: string): TreeNode => ({ name, path: '', is_dir: true, kind: 'folder', children: [] }) as TreeNode
const answer = (root: TreeNode, etag = `W/"${root.name}"`): TreeAnswer => ({ root, etag })

function held() {
  let release: (t: TreeNode) => void = () => {}
  const p = new Promise<TreeAnswer>((r) => (release = (t) => r(answer(t))))
  return { p, release }
}

describe('tree store (BUG-117, S7-11)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked(fetchTree).mockReset()
  })
  afterEach(() => vi.useRealTimers())

  it('keeps the tree it shows while a refresh is on its way', async () => {
    vi.useFakeTimers()
    const store = useTreeStore()
    vi.mocked(fetchTree).mockResolvedValueOnce(answer(tree('old')))
    await store.load()
    const next = held()
    vi.mocked(fetchTree).mockReturnValueOnce(next.p)
    store.refresh()
    store.refresh() // a burst of events: one request
    vi.advanceTimersByTime(REFRESH_DELAY_MS)
    expect(fetchTree).toHaveBeenCalledTimes(2)
    expect(store.byProject['']?.name).toBe('old')
    next.release(tree('new'))
    await vi.waitFor(() => expect(store.byProject['']?.name).toBe('new'))
  })

  it('refreshes while the events come without a pause (BUG-146)', async () => {
    vi.useFakeTimers()
    const store = useTreeStore()
    vi.mocked(fetchTree).mockResolvedValue(answer(tree('t')))
    // An event every half a delay for 3 s, as with many writers.
    for (let at = 0; at < 3000; at += REFRESH_DELAY_MS / 2) {
      store.refresh()
      await vi.advanceTimersByTimeAsync(REFRESH_DELAY_MS / 2)
    }
    expect(vi.mocked(fetchTree).mock.calls.length).toBeGreaterThanOrEqual(2)
  })

  it('keeps the last answer when an older one lands after it', async () => {
    const store = useTreeStore()
    const first = held()
    vi.mocked(fetchTree)
      .mockReturnValueOnce(first.p)
      .mockResolvedValueOnce(answer(tree('second')))
    const a = store.load()
    await store.load()
    first.release(tree('first'))
    await a
    expect(store.byProject['']?.name).toBe('second')
  })

  it('drops an answer that lands after a sign-out', async () => {
    const store = useTreeStore()
    const late = held()
    vi.mocked(fetchTree).mockReturnValueOnce(late.p)
    const a = store.load()
    store.invalidateAll()
    late.release(tree('previous account'))
    await a
    expect(store.byProject['']).toBeUndefined()
  })
})

// A write reloads the tree of its project alone, asked with its ETag: an
// unchanged one costs a 304 (IMP-225). The whole tree was 287 KB.
describe('tree store, one project at a time (IMP-225)', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked(fetchTree).mockReset()
  })
  afterEach(() => vi.useRealTimers())

  const node = (name: string, notes: string[] = []): TreeNode =>
    ({
      name,
      path: name,
      is_dir: true,
      is_project_root: true,
      kind: 'folder',
      note_count: notes.length,
      children: notes.map((n) => ({ name: n, path: `${name}/${n}`, is_dir: false, kind: 'note' })),
    }) as TreeNode
  const whole = (...projects: TreeNode[]): TreeNode =>
    ({ name: '', path: '', is_dir: true, kind: 'folder', children: projects }) as TreeNode
  const names = (root?: TreeNode | null) =>
    (root?.children ?? []).map(
      (c) => `${c.name}:${(c.children ?? []).map((n) => n.name).join(',')}`,
    )

  async function loaded() {
    vi.useFakeTimers()
    const store = useTreeStore()
    vi.mocked(fetchTree).mockResolvedValueOnce(
      answer(whole(node('a', ['1.md']), node('c', ['3.md'])), 'W/"whole"'),
    )
    await store.load()
    return store
  }

  it('reloads the project an event names, and puts it back in its place', async () => {
    const store = await loaded()
    const c = store.byProject['']!.children![1]
    vi.mocked(fetchTree).mockResolvedValueOnce(answer(whole(node('a', ['1.md', '2.md'])), 'W/"a2"'))
    store.refresh('a')
    await vi.advanceTimersByTimeAsync(REFRESH_DELAY_MS)
    expect(fetchTree).toHaveBeenLastCalledWith('a', undefined)
    expect(names(store.byProject[''])).toEqual(['a:1.md,2.md', 'c:3.md'])
    // The other projects are the same objects: their rows stay as they are.
    expect(store.byProject['']!.children![1]).toBe(c)
    expect(store.byProject['']!.note_count).toBe(3)
  })

  it('asks a project again with its ETag, and keeps it on a 304', async () => {
    const store = await loaded()
    vi.mocked(fetchTree).mockResolvedValueOnce(answer(whole(node('a', ['1.md'])), 'W/"a1"'))
    store.refresh('a')
    await vi.advanceTimersByTimeAsync(REFRESH_DELAY_MS)
    const shown = store.byProject['']
    vi.mocked(fetchTree).mockResolvedValueOnce(null)
    store.refresh('a')
    await vi.advanceTimersByTimeAsync(REFRESH_DELAY_MS)
    expect(fetchTree).toHaveBeenLastCalledWith('a', 'W/"a1"')
    expect(store.byProject['']).toBe(shown)
  })

  it('adds a new project in order and takes away one left empty', async () => {
    const store = await loaded()
    vi.mocked(fetchTree)
      .mockResolvedValueOnce(answer(whole(node('b', ['x.md'])), 'W/"b"'))
      .mockResolvedValueOnce(answer(whole(), 'W/"c0"'))
    store.refresh('b')
    store.refresh('c')
    await vi.advanceTimersByTimeAsync(REFRESH_DELAY_MS)
    expect(fetchTree).toHaveBeenCalledTimes(3)
    expect(names(store.byProject[''])).toEqual(['a:1.md', 'b:x.md'])
  })

  it('reloads the whole tree, with its ETag, for a call that names no project', async () => {
    const store = await loaded()
    vi.mocked(fetchTree).mockResolvedValueOnce(null)
    store.refresh('a')
    store.refresh()
    await vi.advanceTimersByTimeAsync(REFRESH_DELAY_MS)
    expect(fetchTree).toHaveBeenCalledTimes(2)
    expect(fetchTree).toHaveBeenLastCalledWith(undefined, 'W/"whole"')
  })

  it('keeps a project reloaded after the whole tree was asked, when the whole one lands later', async () => {
    const store = await loaded()
    const late = held()
    vi.mocked(fetchTree)
      .mockReturnValueOnce(late.p)
      .mockResolvedValueOnce(answer(whole(node('a', ['1.md', 'new.md'])), 'W/"a2"'))
    const all = store.load()
    store.refresh('a')
    await vi.advanceTimersByTimeAsync(REFRESH_DELAY_MS)
    late.release(whole(node('a', ['1.md']), node('c', ['3.md'])))
    await all
    expect(names(store.byProject[''])).toEqual(['a:1.md,new.md', 'c:3.md'])
  })

  it('reads the project of an event, and none for a project-wide one', () => {
    expect(eventProject({ action: 'fs_change', path: 'claude/notes/a.md' })).toBe('claude')
    expect(eventProject({ action: 'in', project: 'p', path: 'p/IN' })).toBe('p')
    expect(eventProject({ action: 'create_project', path: 'p' })).toBeUndefined()
    expect(eventProject({ action: 'rename_project', path: 'p', to: 'q' })).toBeUndefined()
    expect(eventProject({ action: 'delete', path: 'p' })).toBeUndefined()
    expect(eventProject(undefined)).toBeUndefined()
  })
})
