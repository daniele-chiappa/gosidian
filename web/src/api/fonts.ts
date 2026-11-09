import client from './client'

/** A font of the installation's catalog (IMP-226). embedding is what its
 *  licence bits allow: `installable`, `editable`, `preview`, `restricted`,
 *  absent when the file does not say. */
export interface CatalogFont {
  id: string
  family: string
  style: string
  format: string
  faces: number
  embedding?: string
  bytes: number
  added_at: string
}

/** GET /api/v1/fonts */
export async function listFonts(): Promise<CatalogFont[]> {
  const { data } = await client.get<{ items: CatalogFont[] }>('/fonts')
  return data.items
}

/** The files of the catalog, for the editor. The browser keeps each under
 *  its content's name, so only a new one is downloaded. */
export async function fontFiles(): Promise<ArrayBuffer[]> {
  const list = await listFonts()
  const files = list.map(async (f) => {
    const { data } = await client.get<ArrayBuffer>(`/fonts/${encodeURIComponent(f.id)}`, {
      responseType: 'arraybuffer',
    })
    return data
  })
  return Promise.all(files)
}

/** POST /api/v1/admin/fonts */
export async function addFont(file: File): Promise<CatalogFont> {
  const { data } = await client.post<CatalogFont>('/admin/fonts', file, {
    headers: { 'Content-Type': file.type || 'application/octet-stream' },
  })
  return data
}

/** DELETE /api/v1/admin/fonts/{id} */
export async function removeFont(id: string): Promise<void> {
  await client.delete(`/admin/fonts/${encodeURIComponent(id)}`)
}
