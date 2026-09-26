import client from './client'

/** One condition of a frontmatter query (IMP-099): the same shape as the
 *  MCP memory_query tool. `value` is a list for `in`, true/false for `exists`. */
export type QueryOp = 'eq' | 'ne' | 'in' | 'exists' | 'lt' | 'lte' | 'gt' | 'gte' | 'contains'

export interface QueryCondition {
  field: string
  op: QueryOp
  value?: string | number | boolean | string[]
}

export interface QueryRequest {
  project?: string
  where: QueryCondition[]
  sort?: string
  order?: 'asc' | 'desc'
  fields?: string[]
  limit?: number
}

export interface QueryNote {
  path: string
  title: string
  /** RFC 3339, UTC. */
  modified: string
  /** A single value as a string, a list as an array. */
  fields?: Record<string, string | string[]>
}

export interface QueryResponse {
  notes: QueryNote[]
  count: number
  total: number
  truncated: boolean
}

/** POST /api/v1/query */
export async function runQuery(body: QueryRequest): Promise<QueryResponse> {
  const { data } = await client.post<QueryResponse>('/query', body)
  return { ...data, notes: data.notes ?? [] }
}
