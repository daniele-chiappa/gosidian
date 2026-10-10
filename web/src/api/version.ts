import client from './client'

export interface VersionInfo {
  version: string
  api: string
  build_time?: string
  commit?: string
  default_lang?: string
  enabled_langs?: string[]
  /** True when the server runs read-only anonymous access
   *  (GOSIDIAN_OPEN_MODE=readonly): the SPA may show a token-less guest
   *  session instead of forcing /login. See BUG-018. */
  open_mode?: boolean
  /** True when the server runs pipelines ([pipelines] enabled): the Runs
   *  window and the project flag use_pipelines are shown (IMP-166). */
  pipelines_enabled?: boolean
}

/**
 * GET /api/v1/version — public, unauthenticated. Returns server
 * version + i18n defaults so the SPA can pick the initial UI
 * language from the operator's config before the user logs in.
 */
export async function getVersion(): Promise<VersionInfo> {
  const { data } = await client.get<VersionInfo>('/version')
  return data
}
