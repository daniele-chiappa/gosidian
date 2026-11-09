/**
 * What the admin's audit shows (IMP-224, F-30): its filter, its order and
 * its page, kept in the admin window's address after the section
 * (`audit?actor=claude&order=asc`), so that a reload or a link shows the
 * same entries. Only what differs from the defaults is written.
 */
export interface AuditFilter {
  actor: string
  action: string
  source: string
  path_prefix: string
  /** Entries in a page. */
  limit: number
  /** desc: the newest first. */
  order: 'desc' | 'asc'
  /** From 1. */
  page: number
}

export const auditDefaults = (): AuditFilter => ({
  actor: '',
  action: '',
  source: '',
  path_prefix: '',
  limit: 100,
  order: 'desc',
  page: 1,
})

// The texts' names in the address: path, as short as the others.
const TEXTS = [
  ['actor', 'actor'],
  ['action', 'action'],
  ['source', 'source'],
  ['path_prefix', 'path'],
] as const

/** The filter as the address keeps it; '' for the defaults. */
export function auditArg(f: AuditFilter): string {
  const q = new URLSearchParams()
  for (const [field, name] of TEXTS) if (f[field]) q.set(name, f[field])
  if (f.limit !== auditDefaults().limit) q.set('limit', String(f.limit))
  if (f.order === 'asc') q.set('order', 'asc')
  if (f.page > 1) q.set('page', String(f.page))
  return q.toString()
}

/** The filter an address gives: the defaults for what it lacks or has wrong. */
export function auditFilterOf(arg?: string): AuditFilter {
  const f = auditDefaults()
  const q = new URLSearchParams(arg ?? '')
  for (const [field, name] of TEXTS) f[field] = q.get(name)?.trim() ?? ''
  // As the field allows.
  const limit = Number(q.get('limit'))
  if (Number.isInteger(limit) && limit >= 10 && limit <= 500) f.limit = limit
  if (q.get('order') === 'asc') f.order = 'asc'
  const page = Number(q.get('page'))
  if (Number.isInteger(page) && page > 1) f.page = page
  return f
}
