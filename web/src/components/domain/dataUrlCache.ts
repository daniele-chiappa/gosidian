/**
 * The data: URIs of the vault images HTMLPreview inlines, kept so a render
 * does not ask the server again. Least recently used first out, past a
 * budget in characters (a data: URI is ASCII, one byte each): each window
 * of an HTML note kept its images without a limit while it was open.
 */
export class DataUrlCache {
  private entries = new Map<string, string>()
  private size = 0

  constructor(private readonly budget: number) {}

  get(url: string): string | undefined {
    const v = this.entries.get(url)
    if (v === undefined) return undefined
    // Most recent last: Map keeps insertion order.
    this.entries.delete(url)
    this.entries.set(url, v)
    return v
  }

  set(url: string, dataUrl: string) {
    const old = this.entries.get(url)
    if (old !== undefined) {
      this.size -= old.length
      this.entries.delete(url)
    }
    // An image larger than the whole budget is not kept.
    if (dataUrl.length > this.budget) return
    this.entries.set(url, dataUrl)
    this.size += dataUrl.length
    for (const [k, v] of this.entries) {
      if (this.size <= this.budget) break
      this.entries.delete(k)
      this.size -= v.length
    }
  }

  get bytes(): number {
    return this.size
  }
}

/** The one cache of the tab, shared by every HTML note's window: 32 MiB. */
export const inlinedImages = new DataUrlCache(32 << 20)
