import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { AxiosError, AxiosHeaders } from 'axios'
import enUI from '@catalogs/ui.en.json'
import itUI from '@catalogs/ui.it.json'

vi.mock('@/api/notes', () => ({ patchFrontmatter: vi.fn() }))
vi.mock('@/api/tags', () => ({ listTags: vi.fn() }))
import { patchFrontmatter } from '@/api/notes'
import { listTags } from '@/api/tags'
import TagField from '@/components/views/TagField.vue'

const patch = vi.mocked(patchFrontmatter)
const tags = vi.mocked(listTags)

function mountField(props: Partial<InstanceType<typeof TagField>['$props']> = {}, locale = 'en') {
  const i18n = createI18n({
    legacy: false,
    locale,
    fallbackLocale: 'en',
    messages: { en: enUI, it: itUI },
  })
  return mount(TagField, {
    props: { path: 'p/a.md', tags: ['p', 'type:note'], writable: true, ...props },
    global: { plugins: [i18n] },
    attachTo: document.body,
  })
}

const chips = (w: ReturnType<typeof mountField>) =>
  w.findAll('[data-tag]').map((c) => c.attributes('data-tag'))
const input = (w: ReturnType<typeof mountField>) =>
  w.find<HTMLInputElement>('input[role="combobox"]')

describe('TagField (IMP-223)', () => {
  beforeEach(() => {
    patch.mockReset().mockResolvedValue({} as never)
    tags.mockReset().mockResolvedValue([
      { tag: 'topic:audit', count: 9 },
      { tag: 'topic:ui', count: 4 },
      { tag: 'p', count: 30 },
      { tag: 'type:note', count: 12 },
    ])
    document.body.innerHTML = ''
  })

  it("shows a note's tags as chips, each with a button that takes it away, saved at once", async () => {
    const w = mountField()
    expect(chips(w)).toEqual(['p', 'type:note'])
    await w.find('[data-tag="type:note"] button').trigger('click')
    await flushPromises()
    expect(patch).toHaveBeenCalledWith('p/a.md', {
      set: { tags: ['p'] },
      expect: { tags: ['p', 'type:note'] },
    })
    expect(chips(w)).toEqual(['p'])
    expect(w.find('[role="status"]').text()).toBe('Tag type:note removed.')
    expect(w.emitted('saved')?.[0]).toEqual([['p']])
  })

  it('takes the tags key away with the last tag', async () => {
    const w = mountField({ tags: ['p'] })
    await w.find('[data-tag="p"] button').trigger('click')
    await flushPromises()
    expect(patch).toHaveBeenCalledWith('p/a.md', { unset: ['tags'], expect: { tags: ['p'] } })
  })

  it('adds what is typed with Enter or a comma, without # and without twins', async () => {
    const w = mountField()
    await input(w).setValue('#topic:audit')
    await input(w).trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(patch).toHaveBeenLastCalledWith('p/a.md', {
      set: { tags: ['p', 'type:note', 'topic:audit'] },
      expect: { tags: ['p', 'type:note'] },
    })
    expect(input(w).element.value).toBe('')
    await input(w).setValue('P')
    await input(w).trigger('keydown', { key: ',' })
    await flushPromises()
    expect(patch).toHaveBeenCalledTimes(1)
    expect(chips(w)).toEqual(['p', 'type:note', 'topic:audit'])
  })

  it('splits a pasted list at its commas and spaces', async () => {
    const w = mountField({ tags: [] })
    await input(w).setValue('a, b  c,,')
    await input(w).trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(patch).toHaveBeenCalledWith('p/a.md', {
      set: { tags: ['a', 'b', 'c'] },
      expect: { tags: [] },
    })
  })

  it("suggests the project's tags it does not have, the most used first, as a listbox the arrows walk", async () => {
    const w = mountField({ vocabulary: ['topic:roadmap', 'area:*'] })
    await input(w).trigger('focus')
    await flushPromises()
    expect(tags).toHaveBeenCalledWith('p')
    await input(w).setValue('top')
    const options = w.findAll('[role="option"]')
    expect(options.map((o) => o.text())).toEqual(['topic:audit', 'topic:ui', 'topic:roadmap'])
    expect(input(w).attributes('aria-expanded')).toBe('true')
    await input(w).trigger('keydown', { key: 'ArrowDown' })
    await input(w).trigger('keydown', { key: 'ArrowDown' })
    expect(input(w).attributes('aria-activedescendant')).toBe(options[1]!.attributes('id'))
    await input(w).trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(patch).toHaveBeenCalledWith('p/a.md', {
      set: { tags: ['p', 'type:note', 'topic:ui'] },
      expect: { tags: ['p', 'type:note'] },
    })
    await input(w).setValue('top')
    await input(w).trigger('keydown', { key: 'Escape' })
    expect(w.find('[role="listbox"]').exists()).toBe(false)
  })

  it('brings the suggestions and the one the arrows are on into sight', async () => {
    const seen: Element[] = []
    const spy = vi.spyOn(Element.prototype, 'scrollIntoView').mockImplementation(function (
      this: Element,
    ) {
      seen.push(this)
    })
    const w = mountField()
    await input(w).trigger('focus')
    await flushPromises()
    await input(w).setValue('top')
    await flushPromises()
    expect(seen).toContain(w.find('[role="listbox"]').element)
    await input(w).trigger('keydown', { key: 'ArrowDown' })
    await flushPromises()
    expect(seen).toContain(w.findAll('[role="option"]')[0]!.element)
    spy.mockRestore()
  })

  it('puts back the tags of now and says so when someone changed them meanwhile', async () => {
    patch.mockRejectedValue(
      new AxiosError('conflict', '409', undefined, undefined, {
        status: 409,
        data: { error: { details: { fields: { tags: ['p', 'other'] } } } },
        statusText: '',
        headers: {},
        config: { headers: new AxiosHeaders() },
      }),
    )
    const w = mountField()
    await input(w).setValue('new')
    await input(w).trigger('keydown', { key: 'Enter' })
    await flushPromises()
    expect(chips(w)).toEqual(['p', 'other'])
    expect(w.find('[data-tag-message]').text()).toContain('p, other')
  })

  it('shows the tags without buttons nor field to a reader who may not write', () => {
    const w = mountField({ writable: false })
    expect(chips(w)).toEqual(['p', 'type:note'])
    expect(w.find('button').exists()).toBe(false)
    expect(input(w).exists()).toBe(false)
  })

  it("speaks the reader's language", () => {
    const w = mountField({}, 'it')
    expect(w.find('[data-tag="p"] button').attributes('aria-label')).toBe('Togli il tag p')
    expect(input(w).attributes('placeholder')).toBe('Aggiungi un tag')
  })
})
