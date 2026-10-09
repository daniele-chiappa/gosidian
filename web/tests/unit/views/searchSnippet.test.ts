import { describe, expect, it } from 'vitest'
import { plainSnippet } from '@/views/searchSnippet'

describe('plainSnippet', () => {
  it('drops the marks of emphasis, headings and lists', () => {
    expect(plainSnippet('…stesso giorno:\n- **cestino spento**: Elimina su una cartella…')).toBe(
      '…stesso giorno: cestino spento: Elimina su una cartella…',
    )
    expect(plainSnippet('# BUG-082 — Un progetto\n- **Origine**: audit')).toBe('BUG-082 — Un progetto Origine: audit')
    expect(plainSnippet('> quoted *word* and _this_ and `code`')).toBe('quoted word and this and code')
    expect(plainSnippet('1. first\n- [x] done ~~old~~')).toBe('first done old')
  })

  it('keeps the words of links and wikilinks', () => {
    expect(plainSnippet('see [[gosidian/hot|the hot file]] and [[notes/a#Top]]')).toBe('see the hot file and notes/a')
    expect(plainSnippet('a [link](https://example.com/x) and ![img](a.png)')).toBe('a link and img')
  })

  it('leaves words with underscores and stars inside alone', () => {
    expect(plainSnippet('open_plans and snake_case_name, 2*3*4')).toBe('open_plans and snake_case_name, 2*3*4')
  })

  it('drops what a cut left of a mark', () => {
    expect(plainSnippet('…ignored** and the rest of `it')).toBe('…ignored and the rest of it')
    expect(plainSnippet('```go\nfunc main() {}\n```')).toBe('func main() {}')
  })
})
