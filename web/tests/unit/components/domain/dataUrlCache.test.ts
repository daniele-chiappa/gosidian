import { describe, expect, it } from 'vitest'
import { DataUrlCache } from '@/components/domain/dataUrlCache'

describe('DataUrlCache', () => {
  it('drops the least recently used image past its budget', () => {
    const c = new DataUrlCache(10)
    c.set('a', 'aaaa')
    c.set('b', 'bbbb')
    expect(c.get('a')).toBe('aaaa') // a is now the most recent
    c.set('c', 'cccc')
    expect(c.get('b')).toBeUndefined()
    expect(c.get('a')).toBe('aaaa')
    expect(c.get('c')).toBe('cccc')
    expect(c.bytes).toBe(8)
  })

  it('counts a replaced image once, and keeps none larger than the budget', () => {
    const c = new DataUrlCache(10)
    c.set('a', 'aaaa')
    c.set('a', 'aaaaaa')
    expect(c.bytes).toBe(6)
    c.set('big', 'x'.repeat(11))
    expect(c.get('big')).toBeUndefined()
    expect(c.get('a')).toBe('aaaaaa')
  })
})
