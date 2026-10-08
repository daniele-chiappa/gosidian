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
