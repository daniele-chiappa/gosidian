import { describe, expect, it } from 'vitest'
import { noteFrontmatter, yamlScalar } from '@/views/noteFrontmatter'

describe('noteFrontmatter (BUG-116, S6-6)', () => {
  it('leaves a plain title bare', () => {
    expect(yamlScalar('Weekly notes')).toBe('Weekly notes')
    expect(yamlScalar("Bob's plan (v2)")).toBe("Bob's plan (v2)")
    expect(yamlScalar('type:image')).toBe('type:image')
    expect(yamlScalar('Città è già')).toBe('Città è già')
  })

  it('quotes what YAML would read otherwise', () => {
    expect(yamlScalar('Plan: Q3')).toBe('"Plan: Q3"')
    expect(yamlScalar('#1 priority')).toBe('"#1 priority"')
    expect(yamlScalar('2024')).toBe('"2024"')
    expect(yamlScalar('yes')).toBe('"yes"')
    expect(yamlScalar('- item')).toBe('"- item"')
    expect(yamlScalar('[draft] "x"')).toBe('"[draft] \\"x\\""')
    expect(yamlScalar('a, b')).toBe('"a, b"')
    expect(yamlScalar('ends:')).toBe('"ends:"')
    expect(yamlScalar('line\u2028break')).toBe('"line\\u2028break"')
  })

  it('writes the image note of the vault root without an empty tag', () => {
    expect(
      noteFrontmatter([
        ['title', 'Photo: sea'],
        ['type', 'image'],
        ['media', 'attachments/sea.png'],
        ['tags', ['', 'type:image']],
      ]),
    ).toBe('---\ntitle: "Photo: sea"\ntype: image\nmedia: attachments/sea.png\ntags: [type:image]\n---\n\n')
  })
})
