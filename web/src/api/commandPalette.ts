import client from './client'

export interface CmdNote {
  path: string
  title: string
}
export interface CmdProject {
  name: string
  noteCount: number
}
export interface CmdTag {
  tag: string
  count: number
}
export interface CommandPaletteData {
  notes: CmdNote[]
  projects: CmdProject[]
  tags: CmdTag[]
}

/**
 * GET /api/v1/command-palette — full dataset for Cmd+K. The palette keeps
 * the last one and reads it again at each opening.
 */
export async function fetchCommandPalette(): Promise<CommandPaletteData> {
  const { data } = await client.get<CommandPaletteData>('/command-palette')
  return data
}
