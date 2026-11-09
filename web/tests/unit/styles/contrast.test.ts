import { describe, expect, it } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'

// Every preset's text readable on every background it is painted on
// (IMP-221, WCAG 2.2 1.4.3): the ratios computed from tokens.css, so a
// colour changed in a preset cannot fall under 4.5:1 unseen.

type RGB = [number, number, number]

// The presets' `--color-x: R G B;` triplets, by preset name; vitest runs from web/.
function presets(css: string): Map<string, Record<string, RGB>> {
  const out = new Map<string, Record<string, RGB>>()
  for (const block of css.matchAll(/\[data-preset='([\w-]+)'\]\s*\{([^}]*)\}/g)) {
    const tokens: Record<string, RGB> = {}
    for (const m of block[2]!.matchAll(/--color-([\w-]+):\s*(\d+) (\d+) (\d+);/g)) {
      tokens[m[1]!] = [Number(m[2]), Number(m[3]), Number(m[4])]
    }
    if (Object.keys(tokens).length) out.set(block[1]!, tokens)
  }
  return out
}

// WCAG 2.2 relative luminance and contrast ratio.
function luminance(c: RGB): number {
  const [r, g, b] = c.map((v) => {
    const s = v / 255
    return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4
  }) as RGB
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

function contrast(a: RGB, b: RGB): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [number, number]
  return (hi + 0.05) / (lo + 0.05)
}

// A colour at `alpha` over a background, as the browser paints `bg-x/10`.
function over(c: RGB, bg: RGB, alpha: number): RGB {
  return c.map((v, i) => Math.round(bg[i]! * (1 - alpha) + v * alpha)) as RGB
}

const TEXT = ['text', 'text-muted', 'accent', 'accent-hover', 'danger', 'warning', 'success', 'info']
const BACKGROUNDS = ['bg', 'bg-elevated', 'surface', 'surface-hover']
// A chip: a colour's text on that colour's tint (`bg-accent/10 text-accent`).
const CHIPS = ['text-muted', 'accent', 'danger', 'warning', 'success', 'info']
const CHIP_TINT = 10

// The pairs under the limit, as "fg on bg 4.12", so a failure names them.
function below(t: Record<string, RGB>, pairs: [string, string][], limit: number): string[] {
  return pairs
    .map(([fg, bg]) => [fg, bg, contrast(t[fg]!, t[bg]!)] as const)
    .filter(([, , r]) => r < limit)
    .map(([fg, bg, r]) => `${fg} on ${bg} ${r.toFixed(2)}`)
}

function files(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const p = join(dir, name)
    return statSync(p).isDirectory() ? files(p) : /\.(vue|ts)$/.test(p) ? [p] : []
  })
}

// Each tint class in the sources with the class string around it: the text
// between the nearest quotes, since a class string holds none.
function tints(): { at: string; color: string; alpha: number; classes: string }[] {
  const out = []
  for (const f of files('src')) {
    const s = readFileSync(f, 'utf8')
    for (const m of s.matchAll(/(?<![\w/-])(?:[\w-]+:)*bg-(text-muted|accent|danger|warning|success|info)\/(\d+)\b/g)) {
      const i = m.index!
      const start = Math.max(...['"', "'", '`'].map((q) => s.lastIndexOf(q, i)))
      const classes = s.slice(start + 1, s.indexOf(s[start]!, i))
      out.push({ at: `${f}:${s.slice(0, i).split('\n').length}`, color: m[1]!, alpha: Number(m[2]), classes })
    }
  }
  return out
}

describe('theme contrast (IMP-221)', () => {
  const all = presets(readFileSync('src/styles/tokens.css', 'utf8'))

  it('reads the four presets', () => {
    expect([...all.keys()]).toEqual(['catppuccin-mocha', 'catppuccin-latte', 'tokyo-night', 'solarized-light'])
  })

  for (const [name, t] of all) {
    it(`${name}: every text colour reaches 4.5:1 on every background`, () => {
      const pairs = TEXT.flatMap((fg) => BACKGROUNDS.map((bg): [string, string] => [fg, bg]))
      expect(below(t, pairs, 4.5)).toEqual([])
    })

    it(`${name}: a button's text reaches 4.5:1, under the pointer too`, () => {
      const pairs: [string, string][] = [
        ['accent-fg', 'accent'],
        ['accent-fg', 'accent-hover'],
        ['bg', 'danger'],
      ]
      expect(below(t, pairs, 4.5)).toEqual([])
    })

    it(`${name}: a chip's text reaches 4.5:1 on its tint, so do a diff row and a diagram's selection`, () => {
      const low: string[] = []
      for (const bg of ['bg', 'bg-elevated', 'surface']) {
        const check = (fg: string, tint: string, alpha: number) => {
          const r = contrast(t[fg]!, over(t[tint]!, t[bg]!, alpha / 100))
          if (r < 4.5) low.push(`${fg} on ${tint}/${alpha} over ${bg} ${r.toFixed(2)}`)
        }
        for (const c of CHIPS) check(c, c, CHIP_TINT)
        for (const row of ['danger', 'success']) check('text', row, 15)
        // plancia-diagrams' selection and trigger pills (plancia-bridge.css).
        check('text-muted', 'accent', CHIP_TINT)
      }
      expect(low).toEqual([])
    })

    it(`${name}: a field's border reaches 3:1 where fields sit`, () => {
      const pairs = ['bg', 'bg-elevated', 'surface'].map((bg): [string, string] => ['border-input', bg])
      expect(below(t, pairs, 3)).toEqual([])
    })
  }
})

// The start tags of the fields, `<input …>`, `<select …>`, `<textarea …>`,
// to their `>` outside quotes (a bound class can hold one).
function fields(): { at: string; tag: string }[] {
  const out = []
  for (const f of files('src').filter((p) => p.endsWith('.vue'))) {
    const s = readFileSync(f, 'utf8')
    for (const m of s.matchAll(/<(?:input|select|textarea)\b/g)) {
      let i = m.index! + m[0].length
      let quote = ''
      for (; i < s.length; i++) {
        const c = s[i]!
        if (quote) quote = c === quote ? '' : quote
        else if (c === '"' || c === "'") quote = c
        else if (c === '>') break
      }
      out.push({ at: `${f}:${s.slice(0, m.index).split('\n').length}`, tag: s.slice(m.index, i + 1) })
    }
  }
  return out
}

describe('class strings (IMP-221)', () => {
  it(`gives a chip's text its own colour's tint at ${CHIP_TINT}% at most`, () => {
    const strong = tints().filter(
      ({ color, alpha, classes }) =>
        alpha > CHIP_TINT && new RegExp(`(?<![\\w/-])(?:[\\w-]+:)*text-${color}(?![\\w/-])`).test(classes),
    )
    expect(strong.map(({ at, color, alpha }) => `${at} bg-${color}/${alpha}`)).toEqual([])
  })

  it("gives a field the input border, not the separators' one", () => {
    expect(fields().length).toBeGreaterThan(100)
    const separators = fields().filter(({ tag }) => /(?<![\w-])border-border(?![\w-])/.test(tag))
    expect(separators.map(({ at }) => at)).toEqual([])
  })
})
