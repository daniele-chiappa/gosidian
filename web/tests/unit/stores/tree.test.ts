import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import type { TreeNode } from '@/api/tree'

vi.mock('@/api/tree', () => ({ fetchTree: vi.fn() }))

import { fetchTree } from '@/api/tree'
import { REFRESH_DELAY_MS, useTreeStore } from '@/stores/tree'

const tree = (name: string): TreeNode => ({ name, path: '', is_dir: true, kind: 'folder', children: [] }) as TreeNode

function held() {
  let release: (t: TreeNode) => void = () => {}
  const p = new Promise<TreeNode>((r) => (release = r))
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
    vi.mocked(fetchTree).mockResolvedValueOnce(tree('old'))
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

  it('keeps the last answer when an older one lands after it', async () => {
    const store = useTreeStore()
    const first = held()
    vi.mocked(fetchTree).mockReturnValueOnce(first.p).mockResolvedValueOnce(tree('second'))
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
