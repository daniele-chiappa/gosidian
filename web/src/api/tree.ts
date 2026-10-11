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

export async function fetchTree(project?: string): Promise<TreeNode> {
  const { data } = await client.get<TreeResponse>('/tree', {
    params: project ? { project } : undefined,
  })
  return data.root
}
