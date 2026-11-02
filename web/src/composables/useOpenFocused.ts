/**
 * A window opened by a gesture takes the focus on its title (IMP-217,
 * F-03): a screen reader says which window it is, and Tab goes on to its
 * first control. The focus stayed on the tree, the palette that closed or
 * the link, and the window opened unannounced.
 *
 * The plancia's focusWindow does the moving (plancia 0.4.0): the title is
 * its focus target (WINDOW_HEADING, which AppShell gives it), waited for
 * 2 s at most since the views load on demand, and the window itself takes
 * the focus when no title comes. The focus moves only if it is still where
 * the gesture left it, or on the page once the opener went, so a key
 * pressed meanwhile keeps it; only the last open takes it.
 *
 * An open with `focus: false` leaves the focus alone, and so do the windows
 * the address or the browser brings back at load, which the plancia opens
 * from its store; a view that takes the focus itself (a new note, in its
 * editor: props.editorFocus) is left to it. AppShell provides it as
 * 'openWindow', the key the views inject; the shell's own gestures call it
 * directly.
 */
import { provide } from 'vue'
import { useWindowsStore, usePlanciaFocus, type OpenSpec } from 'plancia'

/** The title every view draws: the plancia's focus target in a window. */
export const WINDOW_HEADING = '[data-window-heading]'

const WAIT_MS = 2000

/** The plancia's open, then the focus on the window's title. */
export function useOpenFocused(): (spec: OpenSpec) => string {
  const windows = useWindowsStore()
  const { focusWindow } = usePlanciaFocus()
  return (spec) => {
    const id = windows.open(spec)
    if (id && spec.focus !== false && !spec.props?.editorFocus) void focusWindow(id, { wait: WAIT_MS })
    return id
  }
}

/** Gives the views under the caller the open that moves the focus. */
export function provideOpenFocused(): (spec: OpenSpec) => string {
  const open = useOpenFocused()
  provide('openWindow', open)
  return open
}
