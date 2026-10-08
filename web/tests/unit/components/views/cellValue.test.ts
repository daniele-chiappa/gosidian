import { describe, expect, it } from 'vitest'
import type { ViewColumn, ViewRow } from '@/api/preview'
import {
  displayValue,
  expectValue,
  isEditable,
  lastItemStart,
  shownValue,
  splitItems,
  toChange,
} from '@/components/views/cellValue'

const row: ViewRow = {
  path: 'p/docs/tasks/T-1.md',
  title: 'Align the icons',
  modified: '2026-10-03',
  fields: { status: 'todo', labels: ['ui', 'api'], done: 'false' },
  writable: true,
}
const col = (name: string, type?: string): ViewColumn => ({ name, type })

describe('cell values', () => {
  it('edits only declared fields of writable rows', () => {
    expect(isEditable(col('status', 'select'), row)).toBe(true)
    expect(isEditable(col('status'), row)).toBe(false)
    expect(isEditable(col('title', 'text'), row)).toBe(false)
    expect(isEditable(col('status', 'select'), { ...row, writable: false })).toBe(false)
  })

  it('shows lists joined and absent fields empty', () => {
    expect(displayValue(row, col('labels'))).toBe('ui, api')
    expect(displayValue(row, col('due'))).toBe('')
    expect(displayValue(row, col('modified'))).toBe('2026-10-03')
    expect(expectValue(row, col('due'))).toBeNull()
    expect(expectValue(row, col('labels'))).toEqual(['ui', 'api'])
  })

  it('turns an editor value into a change', () => {
    expect(toChange(col('status', 'select'), 'done')).toEqual({ set: 'done' })
    expect(toChange(col('status', 'select'), '')).toEqual({ unset: true })
    expect(toChange(col('estimate', 'number'), '1.5')).toEqual({ set: 1.5 })
    expect(toChange(col('estimate', 'number'), 'many')).toEqual({ error: 'not_a_number' })
    expect(toChange(col('done', 'checkbox'), true)).toEqual({ set: true })
    expect(toChange(col('labels', 'multi-select'), [])).toEqual({ unset: true })
    expect(toChange(col('notes', 'list'), ' a, b ,, c')).toEqual({ set: ['a', 'b', 'c'] })
    expect(toChange(col('related', 'relation'), '[[p/a]]')).toEqual({ set: '[[p/a]]' })
    expect(toChange(col('related', 'relation'), '[[p/a]]', ['[[p/b]]'])).toEqual({
      set: ['[[p/a]]'],
    })
    expect(toChange(col('due', 'date'), '2026-10-10')).toEqual({ set: '2026-10-10' })
  })

  // BUG-110: a comma inside a link, or in a value nobody edited, is not a
  // separator.
  it('splits a list on the commas outside its links', () => {
    expect(splitItems('[[people/Rossi, Mario]], [[x|a, b]] ,, plain')).toEqual([
      '[[people/Rossi, Mario]]',
      '[[x|a, b]]',
      'plain',
    ])
    expect(lastItemStart('[[a, b]], Ro')).toBe(9)
    expect(lastItemStart('[[a, b')).toBe(0)
    expect(toChange(col('related', 'relation'), '[[people/Rossi, Mario]]')).toEqual({
      set: '[[people/Rossi, Mario]]',
    })
    expect(toChange(col('notes', 'list'), '[[a, b]], c')).toEqual({ set: ['[[a, b]]', 'c'] })
  })

  it('keeps a value its editor showed and nobody changed', () => {
    expect(toChange(col('related', 'relation'), 'Rossi, Mario', 'Rossi, Mario')).toEqual({
      set: 'Rossi, Mario',
    })
    expect(toChange(col('notes', 'list'), ' Rossi, Mario ', ['Rossi, Mario'])).toEqual({
      set: ['Rossi, Mario'],
    })
    expect(toChange(col('notes', 'list'), 'Rossi, Mario, Verdi', ['Rossi, Mario'])).toEqual({
      set: ['Rossi', 'Mario', 'Verdi'],
    })
  })

  it('shows a change as the index reads it', () => {
    expect(shownValue({ set: 1.5 })).toBe('1.5')
    expect(shownValue({ set: true })).toBe('true')
    expect(shownValue({ set: ['a'] })).toEqual(['a'])
    expect(shownValue({ unset: true })).toBeUndefined()
  })
})
