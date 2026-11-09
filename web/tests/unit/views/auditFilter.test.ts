import { describe, expect, it } from 'vitest'
import { auditArg, auditDefaults, auditFilterOf } from '@/views/admin/auditFilter'

describe('the audit filter in the address (IMP-224, F-30)', () => {
  it('writes only what differs from the defaults', () => {
    expect(auditArg(auditDefaults())).toBe('')
    const f = {
      ...auditDefaults(),
      actor: 'claude',
      path_prefix: 'p/a b',
      order: 'asc' as const,
      page: 3,
      limit: 50,
    }
    expect(auditArg(f)).toBe('actor=claude&path=p%2Fa+b&limit=50&order=asc&page=3')
    expect(auditFilterOf(auditArg(f))).toEqual(f)
  })

  it('takes the defaults for what an address lacks or has wrong', () => {
    expect(auditFilterOf(undefined)).toEqual(auditDefaults())
    expect(auditFilterOf('order=newest&page=0&limit=7&source=%20mcp%20')).toEqual({
      ...auditDefaults(),
      source: 'mcp',
    })
    expect(auditFilterOf('page=2.5&limit=900')).toEqual(auditDefaults())
  })
})
