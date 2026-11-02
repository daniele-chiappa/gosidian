/**
 * What a screen reader should hear of an action (IMP-217, F-10): a save, a
 * card moved on a board, the results of a search. One region of the shell
 * says it (AnnounceHost, role=status), always in the page: a region added
 * together with its text is often not read. announce() empties it and
 * writes the text a moment later, so that the same text twice is said
 * twice; changed in one go, the accessibility tree would see no change.
 */
import { ref } from 'vue'

/** The text the region holds now. */
export const announcement = ref('')

// Long enough for the emptied region to reach the accessibility tree.
const SETTLE_MS = 100
let timer: ReturnType<typeof setTimeout> | undefined

/** Says the text politely, once the reader is done with what it says. */
export function announce(text: string): void {
  clearTimeout(timer)
  announcement.value = ''
  timer = setTimeout(() => (announcement.value = text), SETTLE_MS)
}
