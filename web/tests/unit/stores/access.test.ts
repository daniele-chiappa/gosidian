import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import type { AccessView } from '@/api/access'

vi.mock('@/api/access', async (orig) => ({
  ...(await orig<typeof import('@/api/access')>()),
  getMyAccess: vi.fn(),
}))

import { getMyAccess } from '@/api/access'
import { useAccessStore } from '@/stores/access'

const view = (name: string, trash = true): AccessView => ({
  user_id: 'u1',
  role: 'member',
  restricted: false,
  can_create_projects: true,
  trash,
  projects: [{ name, visibility: 'internal', level: 'write', via: ['internal'] }],
})

/** A getMyAccess reply held until the test lets it go. */
function held() {
  let release: (v: AccessView) => void = () => {}
  const p = new Promise<AccessView>((r) => (release = r))
  return { p, release }
}

describe('access store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.mocked(getMyAccess).mockReset()
  })

  it('knows whether a delete goes to the trash (BUG-116, S6-3)', async () => {
    const access = useAccessStore()
    // Unknown until loaded: the question promises neither.
    expect(access.trash).toBeNull()
    expect(access.deleteNoteKey).toBe('tree.menu.confirm_delete_note_plain')
    vi.mocked(getMyAccess).mockResolvedValue(view('A', true))
    await access.load()
    expect(access.trash).toBe(true)
    expect(access.deleteNoteKey).toBe('tree.menu.confirm_delete_note')
    vi.mocked(getMyAccess).mockResolvedValue(view('A', false))
    await access.load()
    expect(access.trash).toBe(false)
    expect(access.deleteNoteKey).toBe('tree.menu.confirm_delete_note_forever')
  })

  it('loads again when asked during a load (BUG-117, S7-13)', async () => {
    const access = useAccessStore()
    const first = held()
    vi.mocked(getMyAccess).mockReturnValueOnce(first.p).mockResolvedValueOnce(view('B'))
    const running = access.load()
    void access.load() // an event while the first load is on its way
    first.release(view('A'))
    await running
    await vi.waitFor(() => expect(Object.keys(access.projects)).toEqual(['B']))
    expect(getMyAccess).toHaveBeenCalledTimes(2)
  })

  it('drops a reply that lands after a reset (BUG-117, S7-13)', async () => {
    const access = useAccessStore()
    const first = held()
    vi.mocked(getMyAccess).mockReturnValueOnce(first.p)
    const running = access.load()
    access.reset() // signed out meanwhile
    first.release(view('Secret'))
    await running
    expect(access.projects).toEqual({})
    expect(access.loaded).toBe(false)
    // and the next account loads normally
    vi.mocked(getMyAccess).mockResolvedValueOnce(view('Mine'))
    await access.load()
    expect(Object.keys(access.projects)).toEqual(['Mine'])
  })
})
