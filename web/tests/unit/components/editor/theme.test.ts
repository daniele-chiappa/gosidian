import { describe, expect, it } from 'vitest'
import { editorThemeSpec } from '@/components/editor/theme'
import { readFileSync } from 'node:fs'

// Read as text: vitest hands CSS imports over empty.
const tokens = readFileSync('src/styles/tokens.css', 'utf8') // vitest runs from web/

const COLOR_KEYS = /color|background/i

describe('editor theme (IMP-154, M1)', () => {
  const values = Object.values(editorThemeSpec).flatMap((rule) =>
    Object.entries(rule).filter(([k]) => COLOR_KEYS.test(k) || k === 'border'),
  )

  it('paints every colour from a token as rgb(), with no fallback', () => {
    expect(values.length).toBeGreaterThan(10)
    for (const [key, value] of values) {
      if (value === 'transparent' || value === 'none') continue
      expect(value, key).toMatch(/rgb\(var\(--color-[a-z-]+\)( \/ [0-9.]+)?\)/)
      expect(value, key).not.toMatch(/var\(--color-[a-z-]+,/)
    }
  })

  it('names only tokens the presets define', () => {
    for (const [, value] of values) {
      for (const [, name] of value.matchAll(/--color-([a-z-]+)/g)) {
        expect(tokens, name).toContain(`--color-${name}:`)
      }
    }
  })
})
