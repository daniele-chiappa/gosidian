import { describe, expect, it } from 'vitest'
import { filenameFrom } from '@/api/download'

describe('filenameFrom', () => {
  it('reads the plain and the quoted parameter', () => {
    expect(filenameFrom('attachment; filename=Alpha-20260928.zip')).toBe('Alpha-20260928.zip')
    expect(filenameFrom('attachment; filename="my project-20260928.zip"')).toBe('my project-20260928.zip')
  })

  it('prefers filename* for a non-ASCII name', () => {
    expect(filenameFrom("attachment; filename*=utf-8''Citt%C3%A0-20260928.zip")).toBe('Città-20260928.zip')
  })

  it('returns undefined without a name', () => {
    expect(filenameFrom('')).toBeUndefined()
    expect(filenameFrom('attachment')).toBeUndefined()
  })
})
