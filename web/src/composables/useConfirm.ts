/**
 * Confirmations, questions and error notices in gosidian's own dialog
 * (ConfirmHost, mounted once in AppShell), where the web UI used the
 * browser's confirm(), prompt() and alert(): those followed neither the
 * theme nor the language of the buttons. The dialog keeps the focus inside,
 * Esc cancels, and Enter confirms, from the confirm button (or the text
 * field) that has the focus when it opens.
 */
import { shallowReactive } from 'vue'

export interface ConfirmRequest {
  id: number
  /** A notice has one button; a prompt has a text field. */
  kind: 'confirm' | 'prompt' | 'notice'
  message: string
  /** The dialog's title; by kind if unset ("Are you sure?", the confirm
   *  button's text for a prompt, "Something went wrong" for a notice). */
  title?: string
  /** The confirm button's text; "Confirm" (or "OK" for a notice) if unset. */
  confirmLabel?: string
  /** A red confirm button, for what deletes or revokes. */
  danger: boolean
  /** The text field's first value, for a prompt. */
  value: string
  resolve: (ok: boolean, value: string) => void
}

/** The open requests, oldest first: ConfirmHost shows the first one. */
export const confirmQueue = shallowReactive<ConfirmRequest[]>([])
let seq = 0

function ask(req: Omit<ConfirmRequest, 'id' | 'resolve'>): Promise<{ ok: boolean; value: string }> {
  return new Promise((resolve) => {
    confirmQueue.push({ ...req, id: ++seq, resolve: (ok, value) => resolve({ ok, value }) })
  })
}

/** True when the user confirms; a destructive action unless danger is false. */
export async function confirmAction(message: string, opts: { confirmLabel?: string; danger?: boolean } = {}): Promise<boolean> {
  const { ok } = await ask({ kind: 'confirm', message, confirmLabel: opts.confirmLabel, danger: opts.danger ?? true, value: '' })
  return ok
}

/** The text the user confirms, or null when cancelled. */
export async function askText(
  message: string,
  value = '',
  opts: { confirmLabel?: string; title?: string } = {},
): Promise<string | null> {
  const res = await ask({ kind: 'prompt', message, title: opts.title, confirmLabel: opts.confirmLabel, danger: false, value })
  return res.ok ? res.value : null
}

/** An error the user acknowledges, with nothing else to do. */
export async function showNotice(message: string): Promise<void> {
  await ask({ kind: 'notice', message, danger: false, value: '' })
}

/** Answers a request (ConfirmHost's buttons, Esc, the close button). */
export function answer(id: number, ok: boolean, value = ''): void {
  const i = confirmQueue.findIndex((r) => r.id === id)
  if (i < 0) return
  const [r] = confirmQueue.splice(i, 1)
  r!.resolve(ok, value)
}

/** Cancels every open request: the account signed out under them. */
export function cancelAll(): void {
  for (const r of confirmQueue.splice(0)) r.resolve(false, '')
}
