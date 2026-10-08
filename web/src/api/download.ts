import axios from 'axios'
import client from './client'

/**
 * Fetches a file from the API with the Bearer, like every other call, and
 * saves it through a Blob + <a download>. A plain link would need the token
 * in the URL, where it leaks into logs and history (IMP-090). The file name
 * comes from the server's Content-Disposition when it sends one.
 */
export async function downloadFile(path: string, fallbackName: string): Promise<void> {
  const res = await client.get<Blob>(path, { responseType: 'blob' })
  saveBlob(res.data, filenameFrom(String(res.headers['content-disposition'] ?? '')) ?? fallbackName)
}

/** Saves a Blob under name through a synthesised <a download>. */
export function saveBlob(blob: Blob, name: string): void {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = name
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

/** The file name of a Content-Disposition header: filename* (RFC 5987,
 *  what the server sends for a non-ASCII name) wins over filename. */
export function filenameFrom(disposition: string): string | undefined {
  const star = /filename\*=utf-8''([^;]+)/i.exec(disposition)
  if (star) {
    try {
      return decodeURIComponent(star[1]!)
    } catch {
      /* malformed: fall back to the plain parameter */
    }
  }
  return /filename="?([^";]+)"?/i.exec(disposition)?.[1]
}

/** A readable message for a failed download. With responseType 'blob' the
 *  JSON error body stays a Blob, so the status carries the meaning. */
export function downloadErrorMessage(e: unknown, what: string): string {
  if (axios.isAxiosError(e)) {
    if (e.response?.status === 429) return `${what}: too many exports, retry in a few minutes`
    if (e.response?.status === 404) return `${what}: not found`
    if (e.response?.status === 403) return `${what}: not allowed`
  }
  return e instanceof Error ? `${what}: ${e.message}` : `${what} failed`
}
