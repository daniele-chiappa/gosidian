import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'
import { parseDiagram, diagramText } from 'plancia-diagrams'

// The format's contract (ADR-036): the reader the web UI draws with, from
// plancia-diagrams, and the server's Go reader say the same of every
// example of docs/diagrams/examples. expected.json is written by the Go
// reader (internal/diagram, UPDATE_EXPECTED=1); only a YAML that does not
// parse is said in each YAML library's own words. vitest runs from web/.
const root = '../docs/diagrams/examples'
const expected = JSON.parse(readFileSync(join(root, 'expected.json'), 'utf8')) as Record<
  string,
  { problems: { line: number; message: string }[]; text?: string }
>
const sorted = (ps: { line: number; message: string }[]) =>
  [...ps].sort(
    (a, b) => a.line - b.line || (a.message < b.message ? -1 : a.message > b.message ? 1 : 0),
  )

describe('the diagram format, in the web UI and on the server', () => {
  it('covers every example', () => {
    const files = ['valid', 'invalid'].flatMap((d) =>
      readdirSync(join(root, d))
        .filter((f) => f.endsWith('.yaml'))
        .map((f) => `${d}/${f}`),
    )
    expect(Object.keys(expected).sort()).toEqual(files.sort())
  })

  it.each(Object.entries(expected))('%s', (name, want) => {
    const { diagram, problems } = parseDiagram(readFileSync(join(root, name), 'utf8'))
    if (want.problems[0]?.message.startsWith('not valid YAML')) {
      expect(problems.map((p) => p.message.slice(0, 16))).toEqual(['not valid YAML: '])
      return
    }
    expect(sorted(problems)).toEqual(want.problems)
    expect(diagramText(diagram)).toBe(want.text ?? '')
  })
})
