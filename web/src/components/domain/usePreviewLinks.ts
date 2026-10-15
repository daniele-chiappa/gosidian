/**
 * The clicks on the links of a rendered note, for the views that show one
 * (MarkdownPreview, CutPreview): a note opens in a window, at its heading
 * when the link names one; a tag in its window; an anchor of the note
 * scrolls to it; a web page or an attachment in a new tab. Nothing
 * navigates the SPA itself away, which would drop the drafts of every
 * window (BUG-117, S7-3).
 */
import { inject, type Ref } from 'vue'
import { useWindowsStore, type OpenSpec } from 'plancia'
import { planciaKey, base } from '@/composables/planciaKey'
import { findHeading } from './headings'
import { linkTarget } from './noteLinks'

/** The decoded fragment of an href, without the #; null when there is none. */
function fragment(href: string): string | null {
  const i = href.indexOf('#')
  if (i < 0 || i === href.length - 1) return null
  try {
    return decodeURIComponent(href.slice(i + 1))
  } catch {
    return href.slice(i + 1)
  }
}

/**
 * root: the element the note is rendered in, where an anchor is looked for.
 * notePath: the note the HTML belongs to, which relative links start from.
 */
export function usePreviewLinks(root: Ref<HTMLElement | null>, notePath: () => string | undefined) {
  const store = useWindowsStore()
  const openWindow = inject<(spec: OpenSpec) => string>('openWindow', (s) => store.open(s))

  function openNote(path: string, anchor: string | null) {
    const key = planciaKey('note', path)
    const props = anchor ? { path, anchor, anchorAt: Date.now() } : { path }
    const id = openWindow({ type: 'note', key, title: base(path), props })
    if (anchor && id) store.identify(id, key, props)
  }

  function onClick(e: MouseEvent) {
    if (e.defaultPrevented || e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return
    const a = (e.target as HTMLElement | null)?.closest('a')
    if (!a) return

    const previewPath = a.getAttribute('data-preview-path')
    const href = a.getAttribute('href') ?? ''

    if (previewPath) {
      e.preventDefault()
      // A link to a heading opens the note there (IMP-140): the heading as
      // written, else the fragment of the href.
      openNote(previewPath, a.getAttribute('data-heading') ?? fragment(href))
      return
    }
    if (href.startsWith('/tags/')) {
      e.preventDefault()
      const tag = decodeURIComponent(href.slice('/tags/'.length).split('#')[0] ?? '')
      if (tag) openWindow({ type: 'tags', key: planciaKey('tags', tag), title: `#${tag}`, props: { tag } })
      return
    }
    if (href.startsWith('#')) {
      e.preventDefault()
      const anchor = a.getAttribute('data-heading') ?? fragment(href) ?? ''
      if (root.value) findHeading(root.value, anchor)?.scrollIntoView({ behavior: 'smooth' })
      return
    }
    if (href.startsWith('/notes/new')) {
      // Unresolved wikilink → nothing to open yet; swallow to stay in the SPA.
      e.preventDefault()
      return
    }
    if (a.hasAttribute('download')) return
    const target = linkTarget(href, notePath())
    if (target.kind === 'browser') return // mailto:, tel:, an app's scheme: the browser's
    e.preventDefault()
    if (target.kind === 'note') openNote(target.path, target.anchor ?? null)
    else if (target.kind === 'tab') window.open(target.url, '_blank', 'noopener,noreferrer')
  }

  return { onClick, openNote, openWindow }
}
