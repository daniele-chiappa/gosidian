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
const catalog = (lang: string): unknown => JSON.parse(readFileSync(`../internal/i18n/catalogs/ui.${lang}.json`, 'utf8'))
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
  ]

  it('has every key built from a value', () => {
    expect(built.filter((k) => !has(enUI, k) || !has(itUI, k))).toEqual([])
  })
})
