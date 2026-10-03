import client from './client'

export interface PreviewResponse {
  html: string
}

/**
 * POST /api/v1/preview — render markdown to sanitized HTML. `path` is the
 * note being shown, so its ```view blocks can use this.<field>.
 */
export async function renderPreview(markdown: string, path?: string): Promise<string> {
  const { data } = await client.post<PreviewResponse>('/preview', { markdown, path })
  return data.html
}
