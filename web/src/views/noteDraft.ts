import { DRAFT_STORAGE_PREFIX } from '@/composables/useSessionReset'

/**
 * What the editor's draft becomes when a save comes back (BUG-108). The
 * server's copy replaces it only when nothing was typed since the request
 * left: text typed while the save was on its way stays, and stays unsaved,
 * where it used to be replaced by the saved copy and marked clean, so the
 * window closed without asking.
 */
export function draftAfterSave(sent: string, draft: string, saved: string): { draft: string; dirty: boolean } {
  const next = draft === sent ? saved : draft
  return { draft: next, dirty: next !== saved }
}

/**
 * The draft kept in this browser (BUG-142): its text, the etag of the
 * version it was written on, and when (ms). It outlives a session that
 * ends, a reload and a crash; resetSessionState drops them all.
 */
export interface KeptDraft {
  text: string
  etag: string
  at: number
}

const draftKey = (user: string, path: string) => `${DRAFT_STORAGE_PREFIX}${user}.${path}`

/** Keeps the draft. When the storage refuses it (full, blocked) an older
 *  copy goes too, so the note never offers that one for this. */
export function keepDraft(user: string, path: string, d: KeptDraft): void {
  try {
    localStorage.setItem(draftKey(user, path), JSON.stringify(d))
  } catch {
    dropDraft(user, path)
  }
}

export function dropDraft(user: string, path: string): void {
  try {
    localStorage.removeItem(draftKey(user, path))
  } catch {
    /* storage disabled: nothing kept */
  }
}

/** The draft kept for the note, or null. The drafts older than maxAge, of
 *  any account, and the broken ones go on the way. */
export function keptDraft(user: string, path: string, maxAge: number, now = Date.now()): KeptDraft | null {
  const key = draftKey(user, path)
  let found: KeptDraft | null = null
  try {
    // Collected first: removing a key while walking them shifts the indexes.
    const stale: string[] = []
    for (let i = 0; i < localStorage.length; i++) {
      const k = localStorage.key(i)
      if (!k?.startsWith(DRAFT_STORAGE_PREFIX)) continue
      const d = parseDraft(localStorage.getItem(k))
      if (!d || now - d.at > maxAge) stale.push(k)
      else if (k === key) found = d
    }
    for (const k of stale) localStorage.removeItem(k)
  } catch {
    return null
  }
  return found
}

function parseDraft(raw: string | null): KeptDraft | null {
  try {
    const v = JSON.parse(raw ?? '') as Partial<KeptDraft> | null
    if (v && typeof v.text === 'string' && typeof v.etag === 'string' && typeof v.at === 'number')
      return { text: v.text, etag: v.etag, at: v.at }
  } catch {
    /* broken */
  }
  return null
}
