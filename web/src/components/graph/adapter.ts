import type { GraphEdge, GraphNode } from '@/api/graph'

/**
 * Shared adapter between the API wire shape and the vasturiano
 * force-graph data model. Used by the 2D canvas today and by the 3D
 * renderer (3d-force-graph) planned for phase 2 — both libraries
 * consume the same `{ nodes, links }` structure.
 */

export interface FGNode {
  id: string
  label: string
  project: string
  degree: number
  /** Color-grouping key — see `groupKey()`. */
  group: string
  /** Last-modified unix seconds (0 = unknown) — powers the 3D recency z-mode. */
  mtime: number
  // Simulation state, written by d3-force (z only in 3D mode).
  x?: number
  y?: number
  z?: number
  vx?: number
  vy?: number
  vz?: number
  fx?: number
  fy?: number
  fz?: number
}

export interface FGLink {
  source: string | FGNode
  target: string | FGNode
  count: number
  cross: boolean
}

export interface FGData {
  nodes: FGNode[]
  links: FGLink[]
}

/** Semantic z-axis of the 3D renderer: free simulation, one plane per
 *  color group, or by note recency (mtime). */
export type ZMode = 'free' | 'groups' | 'recency'

/** Either endpoint of a link, before or after d3 resolves it to an object. */
export function endpointId(end: string | FGNode): string {
  return typeof end === 'string' ? end : end.id
}

/**
 * Color-grouping key for a node. Cross-project graphs group by
 * project; single-project graphs (the default view) would be
 * monochrome under that rule, so they group by the first folder
 * inside the project instead (plans/, skills/, docs/, …) — the level
 * at which clusters are actually meaningful there.
 */
function groupKey(node: GraphNode, multiProject: boolean): string {
  if (multiProject) return node.project ?? ''
  const parts = node.id.split('/')
  return parts.length > 2 ? (parts[1] ?? '') : ''
}

// Node area ∝ degree (capped so hubs don't dwarf the canvas). Both
// renderers draw radius `NODE_REL_SIZE * sqrt(val)` — the formula the
// libraries also use for pointer hit-testing.
export const NODE_REL_SIZE = 4
export const nodeVal = (n: FGNode): number => 1 + Math.min(n.degree, 24)
export const nodeRadius = (n: FGNode): number => NODE_REL_SIZE * Math.sqrt(nodeVal(n))

/**
 * Build force-graph data from an API response. `prevPositions` seeds
 * surviving nodes with their previous coordinates so filter tweaks
 * refine the picture instead of re-rolling it (mental-map stability —
 * the old fcose layout used `randomize: true` and re-scrambled on
 * every fetch).
 */
export function toForceGraph(
  nodes: GraphNode[],
  edges: GraphEdge[],
  prevPositions?: Map<string, { x: number; y: number; z?: number }>,
): FGData {
  const projects = new Set(nodes.map((n) => n.project ?? ''))
  const multiProject = projects.size > 1
  return {
    nodes: nodes.map((n) => {
      const prev = prevPositions?.get(n.id)
      return {
        id: n.id,
        label: n.label,
        project: n.project ?? '',
        degree: n.degree,
        group: groupKey(n, multiProject),
        mtime: n.mtime ?? 0,
        x: prev?.x,
        y: prev?.y,
        z: prev?.z,
      }
    }),
    links: edges.map((e) => ({
      source: e.source,
      target: e.target,
      count: e.count,
      cross: e.cross_project ?? false,
    })),
  }
}

/** Undirected 1-hop adjacency, for hover-neighbourhood highlighting. */
export function buildAdjacency(data: FGData): Map<string, Set<string>> {
  const adj = new Map<string, Set<string>>()
  const add = (a: string, b: string) => {
    let set = adj.get(a)
    if (!set) {
      set = new Set()
      adj.set(a, set)
    }
    set.add(b)
  }
  for (const l of data.links) {
    const s = endpointId(l.source)
    const t = endpointId(l.target)
    add(s, t)
    add(t, s)
  }
  return adj
}
