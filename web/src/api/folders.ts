import client from './client'
import { downloadFile } from './download'

/**
 * Folder actions of the tree's context menu (IMP-150). The path is
 * vault-relative; a project folder has its own routes in projects.ts.
 */

/** Saves a zip of the folder; names in the archive start at the folder. */
export async function exportFolder(path: string): Promise<void> {
  const name = path.split('/').pop() || 'folder'
  await downloadFile(`/folders/${encodeURIComponent(path)}/export.zip`, `${name}.zip`)
}

export interface DeleteFolderResult {
  trash_id: string
  /** The notes the folder held. */
  removed: string[]
}

/** Moves a folder inside a project to the trash, as one entry. */
export async function deleteFolder(path: string): Promise<DeleteFolderResult> {
  const { data } = await client.delete<DeleteFolderResult>(`/folders/${encodeURIComponent(path)}`)
  return data
}
