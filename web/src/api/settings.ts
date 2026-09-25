import client from './client'

export interface GitSettings {
  enabled: boolean
  remote: string
  branch: string
  author_name: string
  author_email: string
  debounce_ms: number
  push: boolean
  token_env: string
}
export interface TrashSettings {
  enabled: boolean
  retention_ms: number
}
export interface I18nSettings {
  default_lang: string
  enabled_langs: string[]
}
export interface MCPSettings {
  write_per_minute: number
  max_note_bytes: number
}
export interface Settings {
  git: GitSettings
  trash: TrashSettings
  i18n: I18nSettings
  mcp: MCPSettings
  totp_mode: string // off | optional | required (global two-factor policy)
  default_visibility: string // public | internal | private, for projects created from now on
  anchors_enabled: boolean // read-only master switch GOSIDIAN_ANCHORS_ENABLED
  globals_enabled: boolean // read-only master switch GOSIDIAN_GLOBAL_ENABLED
}

export interface UpdateSettings {
  git?: Partial<{
    enabled: boolean
    remote: string
    branch: string
    author_name: string
    author_email: string
    debounce_ms: number
    push: boolean
    token_env: string
  }>
  trash?: Partial<{
    enabled: boolean
    retention_ms: number
  }>
  i18n?: Partial<{
    default_lang: string
    enabled_langs: string[]
  }>
  mcp?: Partial<{
    write_per_minute: number
    max_note_bytes: number
  }>
  totp_mode?: string
  default_visibility?: string
}

export async function getSettings(): Promise<Settings> {
  const { data } = await client.get<Settings>('/settings')
  return data
}

export async function updateSettings(body: UpdateSettings): Promise<Settings> {
  const { data } = await client.put<Settings>('/settings', body)
  return data
}
