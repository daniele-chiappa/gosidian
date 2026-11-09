import { describe, expect, it } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'
import { parse } from 'vue/compiler-sfc'

// Every form control of the SPA's templates has a name a screen reader
// says (IMP-220, WCAG 2.2 4.1.2 and 3.3.2): a title alone is a tooltip,
// and a placeholder goes away as soon as one types.

// The template AST's node and prop kinds (@vue/compiler-core NodeTypes).
const ELEMENT = 1
const ATTRIBUTE = 6
const DIRECTIVE = 7

interface Prop {
  type: number
  name: string
  value?: { content: string }
  arg?: { content: string }
}
interface Node {
  type: number
  tag?: string
  props?: Prop[]
  children?: Node[]
  loc: { start: { line: number } }
}

const CONTROLS = new Set(['input', 'select', 'textarea'])
// Types whose name is their value or that are never shown.
const NAMELESS_TYPES = new Set(['hidden', 'submit', 'button', 'reset', 'image'])

// A static attribute or a v-bind of it (`:aria-label`).
function has(n: Node, name: string): Prop | undefined {
  return n.props?.find(
    (p) =>
      (p.type === ATTRIBUTE && p.name === name) ||
      (p.type === DIRECTIVE && p.name === 'bind' && p.arg?.content === name),
  )
}

function staticValue(n: Node, name: string): string {
  const p = n.props?.find((p) => p.type === ATTRIBUTE && p.name === name)
  return p?.value?.content ?? ''
}

function vueFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((f) => {
    const p = join(dir, f)
    if (statSync(p).isDirectory()) return vueFiles(p)
    return p.endsWith('.vue') ? [p] : []
  })
}

interface Control {
  at: string // file:line <tag>
  problem: string | null
}

// The controls of one template, each with what is wrong with its name.
function controls(file: string): Control[] {
  const { descriptor } = parse(readFileSync(file, 'utf8'))
  const root = descriptor.template?.ast as Node | undefined
  if (!root) return []
  let labelFor = false
  ;(function scan(n: Node) {
    if (n.tag === 'label' && has(n, 'for')) labelFor = true
    n.children?.forEach(scan)
  })(root)
  const out: Control[] = []
  ;(function visit(n: Node, inLabel: boolean) {
    if (n.type !== ELEMENT && n !== root) return
    if (n.tag && CONTROLS.has(n.tag)) {
      const hidden = /(^|\s)hidden(\s|$)/.test(staticValue(n, 'class'))
      if (!NAMELESS_TYPES.has(staticValue(n, 'type')) && !hidden) {
        const named =
          has(n, 'aria-label') ||
          has(n, 'aria-labelledby') ||
          inLabel ||
          (has(n, 'id') && labelFor) ||
          // A primitive that passes its attributes on gets its name from its caller.
          n.props?.some((p) => p.type === DIRECTIVE && p.name === 'bind' && !p.arg)
        const problem = named
          ? null
          : has(n, 'title')
            ? 'only a title'
            : has(n, 'placeholder')
              ? 'only a placeholder'
              : 'no name'
        out.push({ at: `${relative('src', file)}:${n.loc.start.line} <${n.tag}>`, problem })
      }
    }
    n.children?.forEach((c) => visit(c, inLabel || n.tag === 'label'))
  })(root, false)
  return out
}

describe('form controls have a name (IMP-220)', () => {
  // vitest runs from web/.
  const all = vueFiles('src').flatMap(controls)

  it('finds the controls of the templates', () => {
    // A scanner that finds nothing must not pass for one that finds no problem.
    expect(all.length).toBeGreaterThan(100)
  })

  it('names every input, select and textarea', () => {
    const missing = all.filter((c) => c.problem).map((c) => `${c.at} ${c.problem}`)
    expect(missing).toEqual([])
  })
})
