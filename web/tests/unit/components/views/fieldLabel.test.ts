import { describe, expect, it } from 'vitest'
import { fieldLabel } from '@/components/views/fieldLabel'

describe('fieldLabel', () => {
  it('reads a key as words', () => {
    expect(fieldLabel('open_plans')).toBe('Open plans')
    expect(fieldLabel('due-date')).toBe('Due date')
    expect(fieldLabel('dueDate')).toBe('Due date')
    expect(fieldLabel('status')).toBe('Status')
  })

  it('keeps capitals and leaves what has no letters to change', () => {
    expect(fieldLabel('IMP')).toBe('IMP')
    expect(fieldLabel('URL_base')).toBe('URL base')
    expect(fieldLabel('_')).toBe('_')
  })
})
