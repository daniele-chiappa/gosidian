/**
 * The narrow screen of the shell (IMP-221, F-15): a phone, or a desktop
 * zoomed to 200% and more. Below NARROW_PX the sidebar is the plancia's
 * drawer, one window at a time takes the width, and the bar's commands go
 * in a menu. The query is the drawer's own (`responsive`), so the two
 * change together.
 */
import type { Ref } from 'vue'
import { useMediaQuery } from '@vueuse/core'

export const NARROW_PX = 640

export function useNarrowScreen(): Ref<boolean> {
  return useMediaQuery(`(max-width: ${NARROW_PX}px)`)
}
