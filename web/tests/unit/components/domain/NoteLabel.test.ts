import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import NoteLabel from '@/components/domain/NoteLabel.vue'

const PATH = 'p/tasks/20261031-004154-nota-delta.md'

// F-19 (IMP-219): a note's title where it has one, else its file name cut
// in the middle, its end always in sight; the file name on hover.
describe('NoteLabel', () => {
  it('shows the title, and the file name on hover', () => {
    const w = mount(NoteLabel, { props: { path: PATH, title: 'Nota delta' } })
    expect(w.text()).toBe('Nota delta')
    expect(w.attributes('title')).toBe('20261031-004154-nota-delta.md')
    expect(w.find('[data-cut-tail]').exists()).toBe(false)
  })

  // A title that is the file name, or the path (a note read without one),
  // is no title.
  it('cuts a long file name in the middle: the last 10 characters never shrink, and it is read once', () => {
    for (const title of [undefined, '20261031-004154-nota-delta', PATH]) {
      const w = mount(NoteLabel, { props: { path: PATH, title } })
      const head = w.find('[data-cut-head]')
      const tail = w.find('[data-cut-tail]')
      expect(head.text()).toBe('20261031-004154-')
      expect(head.classes()).toEqual(expect.arrayContaining(['min-w-0', 'overflow-hidden', 'text-ellipsis']))
      expect(tail.text()).toBe('nota-delta')
      expect(tail.classes()).toContain('shrink-0')
      for (const s of [head, tail]) expect(s.attributes('aria-hidden')).toBe('true')
      expect(w.find('.sr-only').text()).toBe('20261031-004154-nota-delta')
      expect(w.attributes('title')).toBe('20261031-004154-nota-delta.md')
    }
  })

  it('keeps a letter and its accent together at the cut', () => {
    // The é is two code points (e and a combining accent), the 10th letter
    // from the end.
    const w = mount(NoteLabel, { props: { path: 'p/verbale-perchéabcdefghi.md' } })
    expect(w.find('[data-cut-tail]').text()).toBe('éabcdefghi')
    expect(w.find('[data-cut-head]').text()).toBe('verbale-perch')
  })

  it('shows a short file name whole, with no cut', () => {
    const w = mount(NoteLabel, { props: { path: 'p/keep.md' } })
    expect(w.text()).toBe('keep')
    expect(w.attributes('title')).toBe('keep.md')
    expect(w.find('[data-cut-head]').exists()).toBe(false)
  })
})
