/**
 * The diff of a version to the note now (IMP-222), as the server gives it:
 * the hunks of a unified diff, without a header. Each line keeps its text
 * without its mark; the window shows the mark and says it to a screen
 * reader.
 */

export interface DiffLine {
  /** hunk: "@@ -1,3 +1,3 @@"; note: git's remark on a line, "\ No newline…". */
  kind: 'hunk' | 'same' | 'removed' | 'added' | 'note'
  text: string
}

export function parseDiff(diff: string): DiffLine[] {
  if (!diff) return []
  const lines = diff.endsWith('\n') ? diff.slice(0, -1).split('\n') : diff.split('\n')
  return lines.map((l): DiffLine => {
    switch (l[0]) {
      case '@':
        return { kind: 'hunk', text: l }
      case '-':
        return { kind: 'removed', text: l.slice(1) }
      case '+':
        return { kind: 'added', text: l.slice(1) }
      case '\\':
        return { kind: 'note', text: l }
      default:
        return { kind: 'same', text: l.slice(1) }
    }
  })
}
