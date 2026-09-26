/**
 * URL form of a query window (IMP-099). The plancia keeps each window in the
 * URL through the codec (planciaKey.ts); a query window's props carry the
 * whole query, serialised as compact JSON — {p, w: [[field, op, value]], s,
 * o, f} — so a query survives a reload and can be shared as a link.
 */
import type { QueryCondition, QueryOp, QueryRequest } from '@/api/query'

export const QUERY_OPS: QueryOp[] = ['eq', 'ne', 'in', 'exists', 'lt', 'lte', 'gt', 'gte', 'contains']

export type QueryProps = Omit<QueryRequest, 'limit'>

type Wire = {
  p?: string
  w: [string, QueryOp, QueryCondition['value']?][]
  s?: string
  o?: 'asc' | 'desc'
  f?: string[]
}

const isOp = (v: unknown): v is QueryOp => typeof v === 'string' && (QUERY_OPS as string[]).includes(v)

/** Props → URL arg; null when there is no usable condition. */
export function queryArgFromProps(p: Record<string, unknown>): string | null {
  const where = Array.isArray(p.where) ? (p.where as QueryCondition[]) : []
  const w: Wire['w'] = []
  for (const c of where) {
    if (!c || typeof c.field !== 'string' || !c.field.trim() || !isOp(c.op)) continue
    w.push(c.value === undefined ? [c.field, c.op] : [c.field, c.op, c.value])
  }
  if (!w.length) return null
  const wire: Wire = { w }
  if (typeof p.project === 'string' && p.project) wire.p = p.project
  if (typeof p.sort === 'string' && p.sort) wire.s = p.sort
  if (p.order === 'asc' || p.order === 'desc') wire.o = p.order
  if (Array.isArray(p.fields) && p.fields.length) wire.f = (p.fields as unknown[]).filter((x): x is string => typeof x === 'string')
  return JSON.stringify(wire)
}

/** URL arg → props; {} for a missing or malformed arg. */
export function queryPropsFromArg(a: string | null): Record<string, unknown> {
  if (!a) return {}
  let wire: Partial<Wire>
  try {
    wire = JSON.parse(a) as Partial<Wire>
  } catch {
    return {}
  }
  if (!wire || !Array.isArray(wire.w)) return {}
  const where: QueryCondition[] = []
  for (const row of wire.w) {
    if (!Array.isArray(row) || typeof row[0] !== 'string' || !isOp(row[1])) continue
    where.push(row.length > 2 ? { field: row[0], op: row[1], value: row[2] } : { field: row[0], op: row[1] })
  }
  if (!where.length) return {}
  const props: Record<string, unknown> = { where }
  if (typeof wire.p === 'string' && wire.p) props.project = wire.p
  if (typeof wire.s === 'string' && wire.s) props.sort = wire.s
  if (wire.o === 'asc' || wire.o === 'desc') props.order = wire.o
  if (Array.isArray(wire.f) && wire.f.length) props.fields = wire.f.filter((x) => typeof x === 'string')
  return props
}
