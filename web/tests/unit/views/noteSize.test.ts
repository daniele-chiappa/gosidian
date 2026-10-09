import { describe, expect, it } from 'vitest'
import { JSON_BODY_LIMIT, jsonBytes, tooLargeToSend } from '@/views/noteSize'

describe('noteSize (S6-13)', () => {
  it('counts the bytes of the text as JSON sends them', () => {
    expect(jsonBytes('abc')).toBe(5)
    // A newline is two bytes once escaped, an è two in UTF-8.
    expect(jsonBytes('a\nè')).toBe(2 + 1 + 2 + 2)
  })

  it('flags a text the server would refuse, escapes included', () => {
    expect(tooLargeToSend('x'.repeat(1000))).toBe(false)
    expect(tooLargeToSend('x'.repeat(JSON_BODY_LIMIT))).toBe(true)
    // Under 1 MiB of characters, over it once the newlines are escaped.
    expect(tooLargeToSend('\n'.repeat(600 * 1024))).toBe(true)
  })
})
