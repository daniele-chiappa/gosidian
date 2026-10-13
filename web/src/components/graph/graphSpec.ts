/**
 * The ```graph block (IMP-183): a saved view of the graph, read by the
 * note's GraphBlock and written by the Graph window's «Save as a note».
 * The server checks the same fields (internal/graphspec): source (links,
 * or <plugin>/<graph>), with (a plugin's arguments), project, tag, focus,
 * depth, min_degree, limit (the links' filters), color, mode.
 */
import { parse, stringify } from 'yaml'
import type { GraphQuery } from '@/api/graph'

export interface GraphSpec {
  source: string
  with?: Record<string, unknown>
  project?: string
  tag?: string
  focus?: string
  depth?: number
  min_degree?: number
  limit?: number
  color?: string
  mode?: '2d' | '3d'
}

const FIELDS = new Set(['source', 'with', 'project', 'tag', 'focus', 'depth', 'min_degree', 'limit', 'color', 'mode'])

export const isPluginSource = (source: string): boolean => source !== '' && source !== 'links'

/** Reads a block; problem says why it does not read, as the server would. */
export function parseGraphBlock(src: string): { spec: GraphSpec | null; problem?: string } {
  let raw: unknown
  try {
    raw = parse(src) ?? {}
  } catch (e) {
    return { spec: null, problem: e instanceof Error ? e.message : String(e) }
  }
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) {
    return { spec: null, problem: 'a graph is a map of fields' }
  }
  const m = raw as Record<string, unknown>
  for (const k of Object.keys(m)) {
    if (!FIELDS.has(k)) return { spec: null, problem: `"${k}" is no field of a graph` }
  }
  const str = (k: string) => (typeof m[k] === 'string' ? (m[k] as string).trim() : undefined)
  const num = (k: string) => (typeof m[k] === 'number' ? (m[k] as number) : undefined)
  const spec: GraphSpec = { source: str('source') || 'links' }
  if (m.with !== undefined) {
    if (typeof m.with !== 'object' || m.with === null || Array.isArray(m.with)) {
      return { spec: null, problem: "with is a map of the source's arguments" }
    }
    spec.with = m.with as Record<string, unknown>
  }
  spec.project = str('project')
  spec.tag = str('tag')
  spec.focus = str('focus')
  spec.depth = num('depth')
  spec.min_degree = num('min_degree')
  spec.limit = num('limit')
  spec.color = str('color')
  const mode = str('mode')?.toLowerCase()
  if (mode && mode !== '2d' && mode !== '3d') return { spec: null, problem: `mode "${mode}": 2d or 3d` }
  if (mode) spec.mode = mode as '2d' | '3d'
  if (isPluginSource(spec.source) && !/^[a-z][a-z0-9-]{0,31}\/[a-z][a-z0-9_]{0,31}$/.test(spec.source)) {
    return { spec: null, problem: `source "${spec.source}": links, or <plugin>/<graph>` }
  }
  return { spec }
}

/** The block's YAML (without its fence), with only the fields set. */
export function graphBlock(spec: GraphSpec): string {
  const out: Record<string, unknown> = { source: spec.source || 'links' }
  for (const k of ['with', 'project', 'tag', 'focus', 'depth', 'min_degree', 'limit', 'color', 'mode'] as const) {
    const v = spec[k]
    if (v === undefined || v === '' || v === 0) continue
    if (k === 'with' && Object.keys(v as object).length === 0) continue
    out[k] = v
  }
  return stringify(out, { flowCollectionPadding: false })
}

/** A note type: graph holding the block. */
export function graphNote(title: string, spec: GraphSpec): string {
  const safe = title.replace(/"/g, "'")
  return `---\ntitle: "${safe}"\ntype: graph\n---\n\n# ${title}\n\n\`\`\`graph\n${graphBlock(spec)}\`\`\`\n`
}

/**
 * The request of a view: a plugin's graph is asked for the note's project
 * (unless the block names one), with color among its arguments; the links
 * take the filters.
 */
export function graphQuery(spec: GraphSpec, noteProject?: string): GraphQuery {
  if (isPluginSource(spec.source)) {
    const w = { ...(spec.with ?? {}) }
    if (spec.color && w.color === undefined) w.color = spec.color
    return { source: spec.source, project: spec.project || noteProject, with: w }
  }
  return {
    project: spec.project || undefined,
    tag: spec.tag || undefined,
    focus: spec.focus || undefined,
    depth: spec.focus ? spec.depth || 2 : undefined,
    min_degree: spec.min_degree || undefined,
    limit: spec.limit || undefined,
  }
}
