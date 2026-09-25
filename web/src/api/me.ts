import client from './client'
import type { MCPToken, MCPTokenCreated } from './admin'

/** Self-service MCP tokens: owned by the signed-in account and narrowed on
 *  every request to what it may currently read and write. */
export type TokenMode = 'inherit' | 'custom'

export interface CreateMyTokenRequest {
  name: string
  /** inherit (default): follows the account's live access, later grants
   *  included. custom: an explicit subset of the projects visible now. */
  mode?: TokenMode
  projects?: string[]
  scopes?: ('read' | 'write')[]
  ttl_ms?: number
  tool_profile?: '' | 'full' | 'core'
}

interface ListResp<T> {
  items: T[]
  total: number
}

export async function listMyTokens(): Promise<MCPToken[]> {
  const { data } = await client.get<ListResp<MCPToken>>('/me/tokens')
  return data.items
}

export async function createMyToken(body: CreateMyTokenRequest): Promise<MCPTokenCreated> {
  const { data } = await client.post<MCPTokenCreated>('/me/tokens', body)
  return data
}

export async function revokeMyToken(id: string): Promise<void> {
  await client.delete(`/me/tokens/${encodeURIComponent(id)}`)
}
