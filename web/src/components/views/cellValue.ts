import type { FieldValue } from '@/api/notes'
import type { ViewColumn, ViewRow } from '@/api/preview'

/** Columns that come from the note itself, not its frontmatter. */
const BUILTIN = new Set(['title', 'path', 'modified'])

/** Fields edited elsewhere: a row's id is its file name. */
const FIXED = new Set(['id'])

/** Field types a cell can edit. */
const EDITABLE = new Set([
  'text',
  'url',
  'select',
  'multi-select',
  'date',
  'number',
  'checkbox',
  'list',
  'relation',
])

/** List-valued types: the API takes them as a list of strings. */
const LISTS = new Set(['multi-select', 'list'])

/** Whether the reader may edit this cell from the view. */
export function isEditable(col: ViewColumn, row: ViewRow): boolean {
  return (
    row.writable &&
    !!col.type &&
    EDITABLE.has(col.type) &&
    !BUILTIN.has(col.name) &&
    !FIXED.has(col.name)
  )
}

/** The value of a cell as the index read it: a string, a list, or undefined. */
export function cellValue(row: ViewRow, col: ViewColumn): string | string[] | undefined {
  switch (col.name) {
    case 'title':
      return row.title
    case 'path':
      return row.path
    case 'modified':
      return row.modified
  }
  return row.fields[col.name]
}

/** The text a cell shows, as the server's table does: lists joined. */
export function displayValue(row: ViewRow, col: ViewColumn): string {
  const v = cellValue(row, col)
  return Array.isArray(v) ? v.join(', ') : (v ?? '')
}

/** The cell's value in the form `expect` compares: null when empty. */
export function expectValue(row: ViewRow, col: ViewColumn): FieldValue {
  const v = cellValue(row, col)
  if (v === undefined || v === '') return null
  return v
}

/** What a cell's editor asks the API to do with a field. */
export type Change = { set: FieldValue } | { unset: true } | { error: 'not_a_number' }

/**
 * toChange turns what an editor holds into a change: the input's text, the
 * checked state of a checkbox, or the options picked in a multi-select. An
 * empty value removes the key, so the frontmatter keeps no empty fields.
 */
export function toChange(
  col: ViewColumn,
  input: string | boolean | string[],
  current?: string | string[],
): Change {
  if (col.type === 'checkbox') return { set: Boolean(input) }
  if (Array.isArray(input)) return input.length ? { set: input } : { unset: true }
  const raw = String(input).trim()
  if (raw === '') return { unset: true }
  switch (col.type) {
    case 'number': {
      const n = Number(raw)
      return Number.isFinite(n) ? { set: n } : { error: 'not_a_number' }
    }
    case 'list':
    case 'relation': {
      const items = raw
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean)
      // A relation holding one link stays a single value, as it was written.
      if (col.type === 'relation' && items.length === 1 && !Array.isArray(current))
        return { set: items[0] ?? '' }
      return items.length ? { set: items } : { unset: true }
    }
  }
  return { set: raw }
}

/** The value a row shows after a change, in the form the index reads it. */
export function shownValue(change: Change): string | string[] | undefined {
  if (!('set' in change)) return undefined
  const v = change.set
  if (v === null) return undefined
  if (Array.isArray(v)) return v
  return String(v)
}

/** Whether the editor of col edits a list of values. */
export function isListType(col: ViewColumn): boolean {
  return !!col.type && LISTS.has(col.type)
}
