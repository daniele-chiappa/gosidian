import axios from 'axios'
import { patchFrontmatter, type FieldValue, type PatchFrontmatterBody } from '@/api/notes'
import type { ViewColumn } from '@/api/preview'
import type { Change } from './cellValue'

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
