import { describe, expect, it } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'
import { ERROR_KEYS } from '@/api/errors'

// Every key the web UI names literally is in the English and the Italian
// catalogues (IMP-155): a missing one shows as its key, or falls back to
// English in an Italian page.

function files(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name)
    return statSync(p).isDirectory() ? files(p) : /\.(vue|ts)$/.test(p) ? [p] : []
  })
}

function has(catalog: unknown, key: string): boolean {
  let node = catalog as Record<string, unknown> | undefined
  for (const part of key.split('.')) {
    if (!node || typeof node !== 'object' || !(part in node)) return false
    node = node[part] as Record<string, unknown>
  }
  return typeof node === 'string'
}

// t('a.b'), tr('a.b'), keypath="a.b"; vitest runs from web/.
const KEY = /(?:\b(?:t|tr)\(\s*'([a-z_][\w-]*(?:\.[\w-]+)+)'|keypath="([a-z_][\w-]*(?:\.[\w-]+)+)")/g

// Read from disk: imported, the i18n plugin compiles them into functions.
// The audit's page merges its own catalogue into the web UI's
// (useAuditCatalog), and a board its own (useBoardCatalog): their texts
// count as the others.
const read = (file: string): unknown => JSON.parse(readFileSync(`../internal/i18n/catalogs/${file}.json`, 'utf8'))
const merged = (a: unknown, b: unknown): unknown => {
  if (!a || !b || typeof a !== 'object' || typeof b !== 'object') return b ?? a
  const x = a as Record<string, unknown>
  const y = b as Record<string, unknown>
  return Object.fromEntries([...new Set([...Object.keys(x), ...Object.keys(y)])].map((k) => [k, merged(x[k], y[k])]))
}
const catalog = (lang: string): unknown => merged(merged(read(`ui.${lang}`), read(`audit.${lang}`)), read(`board.${lang}`))
const enUI = catalog('en')
const itUI = catalog('it')

describe('i18n catalogues (IMP-155)', () => {
  const used = new Set<string>()
  for (const f of files('src')) {
    for (const m of readFileSync(f, 'utf8').matchAll(KEY)) used.add((m[1] ?? m[2])!)
  }

  it('finds the keys of the web UI', () => {
    expect(used.size).toBeGreaterThan(400)
  })

  it('has every key in English and in Italian', () => {
    const missing = [...used].filter((k) => !has(enUI, k) || !has(itUI, k))
    expect(missing).toEqual([])
  })

  // The keys the code builds from a value, which the scan above cannot see.
  const each = (prefix: string, names: string[]) => names.map((n) => `${prefix}.${n}`)
  const built = [
    ...ERROR_KEYS,
    ...each('access.role', ['owner', 'member', 'guest']),
    ...each('access.visibility', ['public', 'internal', 'private']),
    ...each('access.visibility_help', ['public', 'internal', 'private']),
    ...each('admin.tab', ['users', 'teams', 'tokens', 'spa-tokens', 'invites', 'audit']),
    ...each('note.layout', ['editor', 'split', 'stacked', 'preview']),
    ...each('members.level_name', ['read', 'write', 'admin']),
    ...each('members.level_help', ['read', 'write', 'admin']),
    ...each('members.visibility_note', ['public', 'internal', 'private']),
    ...each('admin.users.via', ['owner', 'public', 'internal']),
    ...each('admin.users.totp_policy_name', ['inherit', 'enabled', 'disabled']),
    ...each('cut.state', ['same', 'changed', 'missing']),
    ...each('uploads.state', ['opening', 'asking', 'sending', 'paused', 'completing', 'done', 'error']),
    ...each('uploads.item', ['waiting', 'sending', 'paused', 'sent', 'error', 'dropped', 'missing']),
    ...each('uploads.result', ['new', 'replaced', 'renamed', 'skipped', 'same', 'unzipped', 'dropped', 'error']),
    ...each('uploads.policy', ['replace', 'keep', 'skip']),
  ]

  it('has every key built from a value', () => {
    expect(built.filter((k) => !has(enUI, k) || !has(itUI, k))).toEqual([])
  })

  // Every text is written in five languages (IMP-220, F-20): one the web UI
  // no longer shows is work for nothing. A key counts as used when a file
  // names it in quotes, or builds it from a prefix (`fields.name.${…}`).
  it('has no key the web UI does not use', () => {
    const source = files('src')
      .map((f) => readFileSync(f, 'utf8'))
      .join('\n')
    const prefixes = [...source.matchAll(/`([a-z_][\w-]*(?:\.[\w-]+)*[._][\w-]*)\$\{/g)].map((m) => m[1]!)
    const keys = (node: unknown, path = ''): string[] =>
      Object.entries(node as Record<string, unknown>).flatMap(([k, v]) =>
        typeof v === 'string' ? [path + k] : keys(v, `${path}${k}.`),
      )
    const quoted = (k: string) => ["'", '"', '`'].some((q) => source.includes(q + k + q))
    // The words of the review the server writes in an iteration's note
    // (internal/i18n/text.go): the web UI shows none of them.
    const server = (k: string) => k.startsWith('board.review.')
    const unused = keys(enUI).filter(
      (k) => !built.includes(k) && !quoted(k) && !server(k) && !prefixes.some((p) => k.startsWith(p)),
    )
    expect(unused).toEqual([])
  })

  // vue-i18n picks a plural form only when the call gives a count, as its
  // last argument (t('k', { n }, n) or t('k', n)): a text the web UI names
  // without one has a single form in every language, or a reader sees
  // "1 nota | 1 notas" (IMP-220, F-20).
  it('has plural forms only where the web UI gives a count', () => {
    const counted = new Set<string>()
    const named = new Set<string>()
    for (const f of files('src')) {
      const text = readFileSync(f, 'utf8')
      for (const m of text.matchAll(/\b(?:t|tr)\(\s*'([a-z_][\w-]*(?:\.[\w-]+)+)'/g)) {
        named.add(m[1]!)
        if (
          callArgs(text, m.index! + m[0].indexOf('(')).length > 2 ||
          /^\s*,\s*[^{\s]/.test(text.slice(m.index! + m[0].length))
        )
          counted.add(m[1]!)
      }
    }
    const forms = (s: string) => s.replace(/\{[^}]*\}/g, '').split('|').length
    const wrong = ['en', 'it', 'de', 'es', 'fr'].flatMap((lang) => {
      const cat = catalog(lang)
      return [...named]
        .filter((k) => !counted.has(k) && has(cat, k) && forms(text(cat, k)) > 1)
        .map((k) => `${lang}: ${k}`)
    })
    expect(wrong).toEqual([])
  })
})

/** The arguments of the call whose ( is at open, split at its top-level commas. */
function callArgs(src: string, open: number): string[] {
  const args: string[] = []
  let depth = 0
  let quote = ''
  let start = open + 1
  for (let i = open; i < src.length; i++) {
    const c = src[i]!
    if (quote) {
      if (c === '\\') i++
      else if (c === quote) quote = ''
    } else if (c === "'" || c === '"' || c === '`') quote = c
    else if ('([{'.includes(c)) depth++
    else if (')]}'.includes(c)) {
      depth--
      if (depth === 0) return [...args, src.slice(start, i)]
    } else if (c === ',' && depth === 1) {
      args.push(src.slice(start, i))
      start = i + 1
    }
  }
  return args
}

function text(catalog: unknown, key: string): string {
  let node = catalog as Record<string, unknown>
  for (const part of key.split('.')) node = node[part] as Record<string, unknown>
  return node as unknown as string
}
