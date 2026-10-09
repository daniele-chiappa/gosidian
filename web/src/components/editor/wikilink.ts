/**
 * What a wikilink completion writes after the note's path. closeBrackets
 * has usually closed the `[[` already, and the completion added `]]` on
 * top: `[[note]]]]` (BUG-117, S7-6). insert is what to add, skip how many
 * closing brackets already there the cursor moves past.
 */
export function wikilinkClosing(after: string): { insert: string; skip: number } {
  if (after.startsWith(']]')) return { insert: '', skip: 2 }
  if (after.startsWith(']')) return { insert: ']', skip: 1 }
  return { insert: ']]', skip: 0 }
}
