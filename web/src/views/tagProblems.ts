/**
 * What is wrong in the tags of a note's frontmatter (IMP-223, F-13), read
 * as the server's line reader splits them when the YAML does not parse:
 * at the commas of an inline list (`[a, b]`, `a, b`) or one per `- ` line.
 * `[agile-lab,, topic:audit type:task]` is no YAML, and was saved as the
 * tags agile-lab and «topic:audit type:task» without a word.
 */
export interface TagProblems {
  /** How many tags are empty. */
  empty: number
  /** The tags with a space, which the reader takes as one tag. */
  spaced: string[]
}

const unquote = (s: string) =>
  s
    .trim()
    .replace(/^(["'])(.*)\1$/, '$2')
    .trim()
    .replace(/^#+/, '')

export function tagProblems(content: string): TagProblems {
  const out: TagProblems = { empty: 0, spaced: [] }
  const lines = content.split(/\r?\n/)
  if (lines[0] !== '---') return out
  const end = lines.findIndex((l, i) => i > 0 && (l === '---' || l === '...'))
  const fm = lines.slice(1, end < 0 ? lines.length : end)
  const at = fm.findIndex((l) => /^tags:/.test(l))
  if (at < 0) return out

  const items: string[] = []
  const rest = fm[at]!.slice('tags:'.length).trim()
  if (rest) {
    const inner = rest.replace(/^\[/, '').replace(/\]$/, '')
    const parts = inner.split(',')
    // YAML allows a comma after the last item of a list.
    if (parts.length > 1 && !parts[parts.length - 1]!.trim()) parts.pop()
    if (inner.trim()) items.push(...parts.map(unquote))
  } else {
    for (const l of fm.slice(at + 1)) {
      const m = /^\s+-(?:\s+(.*))?$/.exec(l) ?? /^-(?:\s+(.*))?$/.exec(l)
      if (!m) break
      items.push(unquote(m[1] ?? ''))
    }
  }
  for (const tag of items) {
    if (!tag) out.empty++
    else if (/\s/.test(tag)) out.spaced.push(tag)
  }
  return out
}
