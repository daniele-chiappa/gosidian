import client from './client'

export interface GraphNode {
  id: string
  label: string
  project?: string
  degree: number
  /** Last-modified unix seconds; absent on cross-project foreign endpoints. */
  mtime?: number
  /** A plugin's graph (IMP-183): the node's group, the note it opens,
   *  the text it shows, and a place the plugin fixed. */
  group?: string
  link?: string
  text?: string
  x?: number
  y?: number
  z?: number
}

export interface GraphEdge {
  source: string
  target: string
  count: number
  cross_project?: boolean
  /** A plugin's edge: its weight, a similarity say. */
  weight?: number
}

export interface GraphStats {
  node_count: number
  edge_count: number
  truncated: boolean
  filter?: string
}

export interface GraphResponse {
  nodes: GraphNode[]
  edges: GraphEdge[]
  stats: GraphStats
  /** A plugin's graph: its source, and whether it came from the cache. */
  source?: string
  cached?: boolean
}

export interface GraphQuery {
  /** links (the default) or <plugin>/<graph>. */
  source?: string
  /** The arguments of a plugin's graph. */
  with?: Record<string, unknown>
  /** Ask the plugin again, past the server's cache. */
  refresh?: boolean
  project?: string
  tag?: string
  focus?: string
  depth?: number
  min_degree?: number
  limit?: number
  include_cross?: boolean
}

export async function fetchGraph(query: GraphQuery = {}): Promise<GraphResponse> {
  const params: Record<string, string | number | boolean> = {}
  if (query.source && query.source !== 'links') {
    params.source = query.source
    if (query.project) params.project = query.project
    if (query.with && Object.keys(query.with).length) params.with = JSON.stringify(query.with)
    if (query.refresh) params.refresh = '1'
    const { data } = await client.get<GraphResponse>('/graph', { params })
    return data
  }
  if (query.project) params.project = query.project
  if (query.tag) params.tag = query.tag
  if (query.focus) params.focus = query.focus
  if (query.depth) params.depth = query.depth
  if (query.min_degree) params.min_degree = query.min_degree
  if (query.limit) params.limit = query.limit
  if (query.include_cross) params.include_cross = 'true'
  const { data } = await client.get<GraphResponse>('/graph', { params })
  return data
}

/** A source of a project's Graph window: the links, or a plugin's graph. */
export interface GraphSource {
  source: string
  plugin?: string
  graph?: string
  description?: string
  input_schema?: {
    type?: string
    properties?: Record<string, { type?: string; description?: string; enum?: unknown[] }>
    required?: string[]
  }
  status?: string
}

export async function fetchGraphSources(project: string): Promise<GraphSource[]> {
  const { data } = await client.get<{ sources: GraphSource[] }>('/graph/sources', { params: { project } })
  return data.sources
}
