/**
 * Opens the command palette from another view than Ctrl+K, with a text
 * already typed: Search, when a text finds nothing in the notes, looks
 * for it in the names (IMP-218, F-26). CommandPalette, mounted once in the
 * shell, answers each request, the same text asked again included.
 */
import { shallowRef } from 'vue'

export const paletteRequest = shallowRef<{ q: string } | null>(null)

export function openPalette(q = ''): void {
  paletteRequest.value = { q }
}
