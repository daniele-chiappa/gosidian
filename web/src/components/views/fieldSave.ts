import axios from 'axios'
import { patchFrontmatter, type FieldValue, type PatchFrontmatterBody } from '@/api/notes'
import type { ViewColumn } from '@/api/preview'
import { shownValue, toChange, type Change } from './cellValue'

type T = (key: string, values?: Record<string, unknown>) => string

/** How a save ended: saved, or why not, with the field's current value on a conflict. */
export type SaveResult =
  | { ok: true }
  | { ok: false; message: string; conflict: boolean; current?: string | string[] }

/**
 * saveField writes one field of the note at path with PATCH
 * /api/v1/notes/{path}/frontmatter, `seen` being the value the editor
 * showed when it opened: the server refuses the edit (409) when the field
 * changed since, instead of overwriting it.
 */
export async function saveField(
  path: string,
  col: ViewColumn,
  seen: FieldValue,
  change: Exclude<Change, { error: string }>,
  t: T,
): Promise<SaveResult> {
  const body: PatchFrontmatterBody = { expect: { [col.name]: seen } }
  if ('set' in change) body.set = { [col.name]: change.set }
  else body.unset = [col.name]
  try {
    await patchFrontmatter(path, body)
    return { ok: true }
  } catch (e) {
    return failure(e, col, t)
  }
}

/** The field commitField writes: where it is saved and how it shows. */
export interface FieldTarget {
  path: string
  col: ViewColumn
  /** The value shown before the edit. */
  before: string | string[] | undefined
  set: (v: string | string[] | undefined) => void
  saving: (on: boolean) => void
}

const same = (a: unknown, b: unknown) => JSON.stringify(a ?? null) === JSON.stringify(b ?? null)

/**
 * commitField writes what an editor gave for one field, the same way for a
 * cell of a table view and for the property panel of a row: shown at once,
 * saved against `seen`, the value the editor opened on, and put back, or
 * set to what someone else wrote meanwhile, when the save is refused.
 * Resolves to the message to show: '' after a save, null when there was
 * nothing to write.
 */
export async function commitField(
  target: FieldTarget,
  seen: FieldValue,
  input: string | boolean | string[],
  t: T,
): Promise<string | null> {
  const { col, before } = target
  const change = toChange(col, input, before)
  if ('error' in change) return t('views.not_a_number', { field: col.name })
  const shown = shownValue(change)
  if (same(shown, seen)) return null

  target.set(shown)
  target.saving(true)
  const res = await saveField(target.path, col, seen, change, t)
  target.saving(false)
  if (res.ok) return ''
  target.set(res.conflict ? res.current : before)
  return res.message
}

function failure(e: unknown, col: ViewColumn, t: T): SaveResult {
  if (axios.isAxiosError(e) && e.response) {
    const err = (
      e.response.data as { error?: { message?: string; details?: Record<string, unknown> } }
    )?.error
    switch (e.response.status) {
      case 409: {
        const fields = (err?.details?.fields ?? {}) as Record<string, string | string[] | null>
        const current = fields[col.name] ?? undefined
        const text = Array.isArray(current) ? current.join(', ') : current
        return {
          ok: false,
          conflict: true,
          current,
          message: text
            ? t('views.conflict', { field: col.name, value: text })
            : t('views.conflict_empty', { field: col.name }),
        }
      }
      case 422: {
        const problems = (err?.details?.problems ?? []) as { message?: string }[]
        return {
          ok: false,
          conflict: false,
          message: t('views.invalid', { reason: problems[0]?.message ?? err?.message ?? '' }),
        }
      }
      case 403:
        return { ok: false, conflict: false, message: t('views.forbidden') }
    }
  }
  return { ok: false, conflict: false, message: t('views.save_failed') }
}
