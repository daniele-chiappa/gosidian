/**
 * The page's title (IMP-217, F-07): what the browser's tab, its list of
 * windows and a screen reader name. It was «gosidian» everywhere, so two
 * tabs of gosidian looked the same. The parts are joined by « · », with
 * «gosidian» last: in the shell the focused window and its project
 * («nota-alfa · agile-lab · gosidian»), elsewhere what the page is for
 * («Sign in · gosidian»). A text in t() follows the language of the UI.
 */
import { toValue, watchEffect, type MaybeRefOrGetter } from 'vue'
import { useWindowsStore, type WindowInstance } from 'plancia'

const APP = 'gosidian'
const SEP = ' · '

/** The parts as a title: the empty ones go, a repeated one is said once. */
export function pageTitle(parts: readonly (string | null | undefined)[]): string {
  const seen: string[] = []
  for (const part of parts) {
    for (const s of (part ?? '').split(SEP)) {
      const v = s.trim()
      if (v && v !== APP && !seen.includes(v)) seen.push(v)
    }
  }
  return [...seen, APP].join(SEP)
}

const str = (v: unknown): string => (typeof v === 'string' ? v : '')

/** The project a window is of: the first folder of its note (a graph's
 *  focus), the folder a creation starts from, or the project it names. */
function windowProject(win: WindowInstance): string | null {
  const path = str(win.props?.path) || str(win.props?.focus)
  const segs = path.split('/')
  if (segs.length > 1 || (win.type === 'create' && path)) return segs[0] ?? null
  return str(win.props?.project) || null
}

/** Keeps document.title on the parts while the calling component lives. */
export function usePageTitle(parts: MaybeRefOrGetter<readonly (string | null | undefined)[]>): void {
  watchEffect(() => {
    document.title = pageTitle(toValue(parts))
  })
}

/** The shell's title: the focused window and its project. */
export function useShellPageTitle(): void {
  const windows = useWindowsStore()
  usePageTitle(() => {
    const win = windows.focused
    return win ? [win.title, windowProject(win)] : []
  })
}
