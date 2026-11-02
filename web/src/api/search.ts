import client from './client'

export interface SearchHit {
  path: string
  title: string
  snippet: string
  /** The words of the query a partial hit's note lacks (the notes with
   *  every word come first); absent on a full hit. */
  missing?: string[]
}

export interface SearchResponse {
  hits: SearchHit[]
}

export interface SearchParams {
  q: string
  project?: string
  limit?: number
}

/** GET /api/v1/search?q=&project=&limit= */
export async function search(params: SearchParams): Promise<SearchHit[]> {
  const { data } = await client.get<SearchResponse>('/search', { params })
  return data.hits ?? []
}
