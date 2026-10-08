import { describe, expect, it } from 'vitest'
import { draftAfterSave } from '@/views/noteDraft'

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
