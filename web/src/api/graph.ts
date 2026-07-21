import client from './client'

export interface GraphNode {
  id: string
  label: string
  project?: string
  degree: number
  /** Last-modified unix seconds; absent on cross-project foreign endpoints. */
  mtime?: number
}

export interface GraphEdge {
  source: string
  target: string
  count: number
  cross_project?: boolean
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
}

export interface GraphQuery {
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
