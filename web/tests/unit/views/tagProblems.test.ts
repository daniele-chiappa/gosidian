import { describe, expect, it } from 'vitest'
import { tagProblems } from '@/views/tagProblems'

const fm = (tags: string) => `---\ntitle: A\n${tags}\nstatus: open\n---\n# A\n`

// The tags of a frontmatter as the server's reader splits them (IMP-223,
// F-13): a comma out of place made a wrong tag unseen.
describe('tagProblems', () => {
  it('finds an empty tag and a tag with a space in an inline list', () => {
    expect(tagProblems(fm('tags: [agile-lab,, topic:audit type:task]'))).toEqual({
      empty: 1,
      spaced: ['topic:audit type:task'],
    })
  })

  it('reads the forms a frontmatter writes tags in', () => {
    expect(tagProblems(fm('tags: a b'))).toEqual({ empty: 0, spaced: ['a b'] })
    expect(tagProblems(fm('tags: a, , b'))).toEqual({ empty: 1, spaced: [] })
    expect(tagProblems(fm('tags:\n  - a\n  - "b c"\n  -\n'))).toEqual({ empty: 1, spaced: ['b c'] })
  })

  it('finds nothing in good tags, an empty list, a trailing comma, or a body', () => {
    for (const tags of [
      'tags: [a, "#b", \'c\']',
      'tags: []',
      'tags: [a, b,]',
      'tags:\n  - a\n  - b',
      'tags:',
    ]) {
      expect(tagProblems(fm(tags)), tags).toEqual({ empty: 0, spaced: [] })
    }
    expect(tagProblems('# A\n\ntags: [a,, b c]\n')).toEqual({ empty: 0, spaced: [] })
    expect(tagProblems(fm('subtags: [a,, b c]'))).toEqual({ empty: 0, spaced: [] })
  })
})
