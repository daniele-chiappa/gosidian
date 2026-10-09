import { describe, expect, it } from 'vitest'
import { isApple, shortcutLabel } from '@/composables/platform'

describe('shortcutLabel', () => {
  it('shows ⌘ on an Apple system and Ctrl elsewhere', () => {
    expect(shortcutLabel('K', true)).toBe('⌘K')
    expect(shortcutLabel('K', false)).toBe('Ctrl+K')
  })

  it('tells an Apple system from the platform or the user agent', () => {
    expect(isApple({ platform: 'MacIntel', userAgent: '' })).toBe(true)
    expect(isApple({ platform: '', userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X)' })).toBe(true)
    expect(isApple({ platform: 'Linux x86_64', userAgent: 'Mozilla/5.0 (X11; Linux x86_64)' })).toBe(false)
    expect(isApple({ platform: 'Win32', userAgent: 'Mozilla/5.0 (Windows NT 10.0; Win64; x64)' })).toBe(false)
  })
})
