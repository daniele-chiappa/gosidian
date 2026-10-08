/**
 * The tree's context menu (IMP-150): one menu for the whole sidebar, so a
 * second right-click moves it instead of stacking another. TreeNode opens
 * it on a row; TreeContextMenu, mounted once by the sidebar, renders it.
 */
import { shallowReactive } from 'vue'
import type { TreeNode } from '@/api/tree'

interface TreeMenuState {
  node: TreeNode | null
  /** Viewport coordinates of the menu's top-left corner, before clamping. */
  x: number
  y: number
  /** The row it was opened from: focus goes back there on close. */
  trigger: HTMLElement | null
}

const state = shallowReactive<TreeMenuState>({ node: null, x: 0, y: 0, trigger: null })

export function useTreeMenu() {
  return {
    state,
    open(node: TreeNode, x: number, y: number, trigger: HTMLElement | null) {
      state.node = node
      state.x = x
      state.y = y
      state.trigger = trigger
    },
    /** Closes the menu; with restoreFocus the row it came from gets the focus back. */
    close(restoreFocus = false) {
      const trigger = state.trigger
      state.node = null
      state.trigger = null
      if (restoreFocus) trigger?.focus()
    },
  }
}
