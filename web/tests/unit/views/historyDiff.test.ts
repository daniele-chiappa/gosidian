import { describe, expect, it } from 'vitest'
import { parseDiff } from '@/views/historyDiff'

// The diff of a version to the note now, as the server gives it: hunks of a
// unified diff, without a header (IMP-222).
describe('parseDiff', () => {
  it('reads the hunks: a heading, the lines kept, removed and added', () => {
    const diff = '@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n@@ -10 +10 @@\n-x\n+y\n'
    expect(parseDiff(diff)).toEqual([
      { kind: 'hunk', text: '@@ -1,3 +1,3 @@' },
      { kind: 'same', text: 'a' },
      { kind: 'removed', text: 'b' },
      { kind: 'added', text: 'B' },
      { kind: 'same', text: 'c' },
      { kind: 'hunk', text: '@@ -10 +10 @@' },
      { kind: 'removed', text: 'x' },
      { kind: 'added', text: 'y' },
    ])
  })

  it('keeps the remark of a last line without its end, and an empty line', () => {
    expect(parseDiff('@@ -1 +1,2 @@\n-a\n\\ No newline at end of file\n+a\n+\n')).toEqual([
      { kind: 'hunk', text: '@@ -1 +1,2 @@' },
      { kind: 'removed', text: 'a' },
      { kind: 'note', text: '\\ No newline at end of file' },
      { kind: 'added', text: 'a' },
      { kind: 'added', text: '' },
    ])
  })

  it('has no lines for no diff', () => {
    expect(parseDiff('')).toEqual([])
  })
})
