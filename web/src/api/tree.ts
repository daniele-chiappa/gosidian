/**
 * Typed wrapper for /api/v1/tree. The SPA's tree store consumes
 * fetchTree(); the shape mirrors apiTreeNode in
 * internal/api/v1/tree.go (camelCase JSON keys snake_cased here for
 * Vue idiomaticness — `is_dir` becomes `isDir`).
 */
import client from './client'
import type { INCounts } from './in'

export interface TreeNode {
  name: string
  path: string
  is_dir: boolean
  is_project_root?: boolean
  kind: string
  /** A note's title, where it is not its file name (IMP-219). */
  title?: string
  note_count?: number
  in_progress?: boolean
  hidden_from_mcp?: boolean
  skip_git_sync?: boolean
  /** What the project's IN folder holds, on its node (kind "in", IMP-172). */
  in?: INCounts
  children?: TreeNode[]
}

export interface TreeResponse {
  root: TreeNode
}

/** A tree and the ETag of what it holds. */
export interface TreeAnswer {
  root: TreeNode
  etag: string
}

/**
 * The tree of the vault, or of one project, as the account sees it. With
 * the ETag of the tree held, an unchanged one answers 304 without a body,
 * and this null (IMP-225).
 */
export async function fetchTree(project?: string, etag?: string): Promise<TreeAnswer | null> {
  const res = await client.get<TreeResponse>('/tree', {
    params: project ? { project } : undefined,
    headers: etag ? { 'If-None-Match': etag } : undefined,
    validateStatus: (s) => (s >= 200 && s < 300) || s === 304,
  })
  if (res.status === 304) return null
  return { root: res.data.root, etag: String(res.headers['etag'] ?? '') }
}
