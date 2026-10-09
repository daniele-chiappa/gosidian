/**
 * Where a link of a note leads, for the previews that take the clicks
 * (MarkdownPreview, HTMLPreview): a note opens in a window, an attachment,
 * a web page or any other address in a new tab. Nothing navigates the SPA
 * itself away: a relative markdown link (`[x](design.md)`) did, and the
 * drafts of every window went with it (BUG-117, S7-3).
 */

export const hasScheme = (u: string) => /^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(u)

/** A web address, the only kind a canvas link card may point to (BUG-117, S7-14). */
export const isWebURL = (u?: string) => /^https?:\/\//i.test(u ?? '')

// browser: an address of another scheme (mailto:, tel:, obsidian://), which
// the browser hands to its application without leaving the page.
export type LinkTarget =
  | { kind: 'note'; path: string; anchor?: string }
  | { kind: 'tab'; url: string }
  | { kind: 'browser'; url: string }
  | { kind: 'none' }

/** The folder of a vault path, "" at the root or without a path. */
function dirOf(path?: string): string {
  const p = path ?? ''
  const i = p.lastIndexOf('/')
  return i >= 0 ? p.slice(0, i) : ''
}

/** Resolves a relative path against the note's folder; null when it climbs out of the vault. */
export function resolveRelative(notePath: string | undefined, rel: string): string | null {
  const dir = dirOf(notePath)
  const parts = dir ? dir.split('/') : []
  for (const seg of rel.split('/')) {
    if (seg === '' || seg === '.') continue
    if (seg === '..') {
      if (parts.length === 0) return null
      parts.pop()
    } else {
      parts.push(seg)
    }
  }
  return parts.length ? parts.join('/') : null
}

function decode(s: string): string {
  try {
    return decodeURIComponent(s)
  } catch {
    return s
  }
}

/** The target of href, a link in the note at notePath. */
export function linkTarget(href: string, notePath?: string): LinkTarget {
  if (/^https?:/i.test(href) || href.startsWith('//')) return { kind: 'tab', url: href }
  if (/^(javascript|vbscript|data|file|blob):/i.test(href)) return { kind: 'none' }
  if (hasScheme(href)) return { kind: 'browser', url: href }
  const cut = href.search(/[?#]/)
  const raw = cut >= 0 ? href.slice(0, cut) : href
  const hash = href.indexOf('#')
  const anchor = hash >= 0 && hash < href.length - 1 ? decode(href.slice(hash + 1)) : undefined
  const target = decode(raw)
  let path: string | null
  if (target.startsWith('/notes/')) path = target.slice('/notes/'.length)
  else if (target.startsWith('/')) return { kind: 'tab', url: href }
  else if (target === '') return { kind: 'none' }
  else path = resolveRelative(notePath, target)
  if (!path) return { kind: 'none' }
  if (!/\.(md|html|canvas|base)$/i.test(path)) {
    if (`/${path}`.includes('/attachments/')) return { kind: 'tab', url: `/vault-files/${path}` }
    path += '.md'
  }
  return anchor ? { kind: 'note', path, anchor } : { kind: 'note', path }
}
