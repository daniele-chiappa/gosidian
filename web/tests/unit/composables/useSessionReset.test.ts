import { describe, expect, it, vi } from 'vitest'

// The stores are not what this test is about: each resets as its own tests say.
vi.mock('@/stores/tree', () => ({ useTreeStore: () => ({ invalidateAll: vi.fn() }) }))
vi.mock('@/stores/access', () => ({ useAccessStore: () => ({ reset: vi.fn() }) }))
vi.mock('@/stores/uploads', () => ({ useUploadsStore: () => ({ reset: vi.fn() }) }))
vi.mock('@/composables/useRecentlyViewed', () => ({ useRecentlyViewed: () => ({ clear: vi.fn() }) }))
vi.mock('@/composables/useConfirm', () => ({ cancelAll: vi.fn() }))
vi.mock('plancia', async (orig) => ({
  ...(await orig<typeof import('plancia')>()),
  useWindowsStore: () => ({ reset: vi.fn() }),
}))

import { CUT_STORAGE_PREFIX, resetSessionState } from '@/composables/useSessionReset'

const keys = () => Array.from({ length: localStorage.length }, (_, i) => localStorage.key(i)!)

describe('resetSessionState (BUG-116)', () => {
  // The cut view's rules name the projects and the collections the account
  // saw: the next account in this browser must not find them (IMP-170).
  it("drops the window layout and every project's cut rules, and keeps the rest", () => {
    localStorage.setItem('gosidian.plancia', '{"w":[]}')
    localStorage.setItem(`${CUT_STORAGE_PREFIX}alpha`, '{"collection":"vec/docs"}')
    localStorage.setItem(`${CUT_STORAGE_PREFIX}beta`, '{}')
    localStorage.setItem(CUT_STORAGE_PREFIX, '{}')
    localStorage.setItem('gosidian.editorMode', 'split')
    localStorage.setItem('gosidian.lastUser', 'bob')

    resetSessionState()

    expect(localStorage.getItem('gosidian.plancia')).toBeNull()
    expect(keys().filter((k) => k.startsWith(CUT_STORAGE_PREFIX))).toEqual([])
    expect(localStorage.getItem('gosidian.editorMode')).toBe('split')
    expect(localStorage.getItem('gosidian.lastUser')).toBe('bob')
  })

  it('does not throw when the storage refuses', () => {
    const remove = vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => {
      throw new Error('SecurityError')
    })
    try {
      expect(() => resetSessionState()).not.toThrow()
    } finally {
      remove.mockRestore()
    }
  })
})
