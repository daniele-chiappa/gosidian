/**
 * Finding the heading a link names (IMP-140), the way memory_get_section
 * does on the server (parser.ResolveHeading): the heading itself, ignoring
 * case; else the one heading that starts with it followed by a separator,
 * an ID alone such as `ADR-010` for `## ADR-010 — …` (the highest level wins
 * when several start so and no other shares that level); else the heading
 * whose anchor id it is.
 */

/** What may follow an ID at the start of a heading (parser.headingSeps). */
const SEPS = [' — ', ' – ', ' - ', ': ', ':', ' (']

/** The anchor id the server gives a heading in a link (parser.headingID). */
export function slug(s: string): string {
  let out = ''
  let dash = false
  for (const ch of s.toLowerCase()) {
    if ((ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')) {
      out += ch
      dash = false
    } else if (!dash && out.length > 0) {
      out += '-'
      dash = true
    }
  }
  return out.replace(/-+$/, '')
}

interface Heading {
  el: HTMLElement
  level: number
  text: string
}

function headingsOf(root: ParentNode): Heading[] {
  return Array.from(root.querySelectorAll<HTMLElement>('h1, h2, h3, h4, h5, h6')).map((el) => ({
    el,
    level: Number(el.tagName.slice(1)),
    text: (el.textContent ?? '').trim(),
  }))
}

/** The one heading of the highest level among hs, or null when two share it. */
function highest(hs: Heading[]): Heading | null {
  if (hs.length === 0) return null
  const top = Math.min(...hs.map((h) => h.level))
  const at = hs.filter((h) => h.level === top)
  return at.length === 1 ? (at[0] ?? null) : null
}

/**
 * The heading element of root that `anchor` names: the heading text of a
 * link (`data-heading`), or the fragment of its href. Null when none, or
 * when the anchor names several headings alike.
 */
export function findHeading(root: ParentNode, anchor: string): HTMLElement | null {
  const want = anchor.trim().toLowerCase()
  if (!want) return null
  const hs = headingsOf(root)
  const exact = hs.find((h) => h.text.toLowerCase() === want)
  if (exact) return exact.el
  const prefixed = hs.filter((h) => {
    const text = h.text.toLowerCase()
    return text.startsWith(want) && SEPS.some((sep) => text.slice(want.length).startsWith(sep))
  })
  if (prefixed.length > 0) return highest(prefixed)?.el ?? null
  // An anchor id: the element's own, or the slug the server writes in links.
  const s = slug(anchor)
  const byId = hs.find((h) => h.el.id === anchor || (s !== '' && slug(h.text) === s))
  if (byId) return byId.el
  return s ? (highest(hs.filter((h) => slug(h.text).startsWith(s + '-')))?.el ?? null) : null
}
