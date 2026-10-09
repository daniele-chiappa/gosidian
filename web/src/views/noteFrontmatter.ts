/**
 * The frontmatter of a note the web UI creates (BUG-116, S6-6). Values were
 * written as typed: `title: Plan: Q3` was not YAML and the title was lost,
 * `#1 priority` became a comment, and a note at the vault root got
 * `tags: [, type:image]`.
 */

// What YAML reads back as the same text when written bare: a letter, a
// digit or `_` first, then the characters of a title or a tag, a colon
// only inside a word (`type:image`).
const PLAIN = /^[\p{L}_][\p{L}\p{N}_ .()'/:-]*$/u

/** A YAML scalar holding s: bare when that reads back the same, quoted otherwise. */
export function yamlScalar(s: string): string {
  if (PLAIN.test(s) && !/[\s:]$/.test(s) && !/:\s/.test(s) && !/^(true|false|yes|no|on|off|null)$/i.test(s)) {
    return s
  }
  // A JSON string is a YAML double-quoted scalar; the line separators JSON
  // leaves as they are would end a line there.
  return JSON.stringify(s).replace(/[\u0085\u2028\u2029]/g, (c) => `\\u${c.charCodeAt(0).toString(16).padStart(4, '0')}`)
}

/** The frontmatter block of fields in order, a list as a flow sequence. */
export function noteFrontmatter(fields: [string, string | string[]][]): string {
  const lines = fields.map(([k, v]) =>
    Array.isArray(v) ? `${k}: [${v.filter((x) => x !== '').map(yamlScalar).join(', ')}]` : `${k}: ${yamlScalar(v)}`,
  )
  return `---\n${lines.join('\n')}\n---\n\n`
}
