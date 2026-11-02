import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { draftAfterSave, dropDraft, keepDraft, keptDraft } from '@/views/noteDraft'
import { DRAFT_STORAGE_PREFIX } from '@/composables/useSessionReset'

describe('draftAfterSave', () => {
  it('takes the saved copy when nothing was typed during the save', () => {
    expect(draftAfterSave('# a\n', '# a\n', '# a\n')).toEqual({ draft: '# a\n', dirty: false })
    // The server may normalise what it stores: the draft follows it.
    expect(draftAfterSave('# a', '# a', '# a\n')).toEqual({ draft: '# a\n', dirty: false })
  })

  it('keeps text typed while the save was on its way, unsaved (BUG-108)', () => {
    expect(draftAfterSave('# a', '# a and more', '# a')).toEqual({ draft: '# a and more', dirty: true })
  })
})

const T = Date.parse('2026-11-01T10:00:00Z')
const HOUR = 3600_000
const DAY = 24 * HOUR
const kept = (at: number, text = '# a\n\nmine') => ({ text, etag: 'e-1', at })
const keys = () => Array.from({ length: localStorage.length }, (_, i) => localStorage.key(i)!).sort()

describe('the drafts kept in the browser (BUG-142)', () => {
  beforeEach(() => localStorage.clear())
  afterEach(() => vi.restoreAllMocks())

  it('keeps a draft per account and note', () => {
    keepDraft('u1', 'p/a.md', kept(T))
    expect(keptDraft('u1', 'p/a.md', DAY, T)).toEqual(kept(T))
    expect(keptDraft('u2', 'p/a.md', DAY, T)).toBeNull()
    expect(keptDraft('u1', 'p/b.md', DAY, T)).toBeNull()

    keepDraft('u1', 'p/a.md', kept(T + 1, '# a\n\nmore'))
    expect(keptDraft('u1', 'p/a.md', DAY, T + 1)?.text).toBe('# a\n\nmore')
    dropDraft('u1', 'p/a.md')
    expect(keptDraft('u1', 'p/a.md', DAY, T + 1)).toBeNull()
  })

  it('drops the drafts older than the longest session, of any account, and broken ones', () => {
    keepDraft('u1', 'p/a.md', kept(T - 2 * DAY))
    keepDraft('u2', 'p/c.md', kept(T - 2 * DAY))
    keepDraft('u2', 'p/d.md', kept(T - HOUR))
    localStorage.setItem(`${DRAFT_STORAGE_PREFIX}u1.p/e.md`, '{broken')
    localStorage.setItem(`${DRAFT_STORAGE_PREFIX}u1.p/f.md`, '{"text":1}')
    localStorage.setItem('gosidian.editorMode', 'split')

    expect(keptDraft('u1', 'p/a.md', DAY, T)).toBeNull()
    expect(keys()).toEqual([`${DRAFT_STORAGE_PREFIX}u2.p/d.md`, 'gosidian.editorMode'])
  })

  it('works on without a storage', () => {
    for (const m of ['getItem', 'setItem', 'removeItem', 'key'] as const)
      vi.spyOn(localStorage, m).mockImplementation(() => {
        throw new Error('SecurityError')
      })
    expect(() => keepDraft('u1', 'p/a.md', kept(T))).not.toThrow()
    expect(() => dropDraft('u1', 'p/a.md')).not.toThrow()
    expect(keptDraft('u1', 'p/a.md', DAY, T)).toBeNull()
  })

  it('drops the older copy when the storage refuses the newer one', () => {
    keepDraft('u1', 'p/a.md', kept(T))
    vi.spyOn(localStorage, 'setItem').mockImplementation(() => {
      throw new Error('QuotaExceededError')
    })
    keepDraft('u1', 'p/a.md', kept(T + 1, '# a\n\na lot more'))
    expect(keptDraft('u1', 'p/a.md', DAY, T + 1)).toBeNull()
  })
})
