import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import type { RowFields } from '@/api/notes'

vi.mock('@/api/notes', () => ({ getRowFields: vi.fn(), patchFrontmatter: vi.fn() }))
import { getRowFields, patchFrontmatter } from '@/api/notes'
import PropertiesPanel from '@/components/views/PropertiesPanel.vue'
import FieldValue from '@/components/views/FieldValue.vue'

const get = vi.mocked(getRowFields)
const patch = vi.mocked(patchFrontmatter)

function fields(over: Partial<RowFields> = {}): RowFields {
  return {
    database: 'p/docs/tasks.md',
    source: 'p/docs/tasks',
    columns: [
      { name: 'id', type: 'text', required: true },
      { name: 'status', type: 'select', options: ['todo', 'doing', 'done'], required: true },
      { name: 'see', type: 'relation' },
    ],
    others: ['extra'],
    values: { id: 'T-1', status: 'todo', see: '[[p/docs/tasks/T-2]]', extra: 'x' },
    links: { see: [{ text: 'p/docs/tasks/T-2', path: 'p/docs/tasks/T-2.md' }] },
    writable: true,
    ...over,
  }
}

async function mountPanel(f: RowFields) {
  get.mockResolvedValue(f)
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
  const w = mount(PropertiesPanel, {
    props: { path: 'p/docs/tasks/T-1.md', etag: 'e1' },
    global: { plugins: [i18n], provide: { openWindow: vi.fn() } },
  })
  await flushPromises()
  return w
}

describe('PropertiesPanel', () => {
  beforeEach(() => {
    get.mockReset()
    patch.mockReset()
  })

  it('shows the fields of a row, the id read-only, the undeclared ones apart', async () => {
    const w = await mountPanel(fields())
    expect(w.find('[data-properties]').text()).toContain('row of')
    expect(w.find('dd[data-field="id"] button').exists()).toBe(false)
    expect(w.find('dd[data-field="id"]').text()).toBe('T-1')
    expect(w.find('dd[data-field="status"] button').exists()).toBe(true)
    // A relation shows its link, with a separate edit button.
    const link = w.find('dd[data-field="see"] a.wikilink')
    expect(link.attributes('data-preview-path')).toBe('p/docs/tasks/T-2.md')
    expect(w.find('dd[data-field="see"] button').exists()).toBe(true)
    expect(w.text()).toContain('Not in the schema')
    expect(w.find('dd[data-field="extra"]').text()).toBe('x')
  })

  it('saves a field with the value it showed as expect', async () => {
    patch.mockResolvedValue({} as never)
    const w = await mountPanel(fields())
    await w.find('dd[data-field="status"] button').trigger('click')
    await w.find('dd[data-field="status"] select').setValue('done')
    await flushPromises()
    expect(patch).toHaveBeenCalledWith('p/docs/tasks/T-1.md', {
      set: { status: 'done' },
      expect: { status: 'todo' },
    })
  })

  it('is read-only for a row the reader may not write', async () => {
    const w = await mountPanel(fields({ writable: false }))
    expect(w.findAll('dd button')).toHaveLength(0)
    expect(w.text()).toContain('read only')
  })

  it('is not shown for a note outside a database', async () => {
    const w = await mountPanel({ values: {}, writable: false })
    expect(w.find('[data-properties]').exists()).toBe(false)
  })
})

describe('FieldValue', () => {
  it('shows wikilinks as links, unresolved ones plain', () => {
    // FieldValue has several root nodes: read the text from its host.
    const host = document.createElement('div')
    const w = mount(FieldValue, {
      attachTo: host,
      props: {
        value: ['[[p/a|A]]', 'see [[nowhere]] too'],
        links: [{ text: 'A', path: 'p/a.md' }, { text: 'nowhere' }],
      },
      global: { provide: { openWindow: vi.fn() } },
    })
    expect(host.querySelector('a.wikilink')?.textContent).toBe('A')
    expect(host.querySelector('span.unresolved')?.textContent).toBe('nowhere')
    expect(host.textContent).toBe('A, see nowhere too')
    w.unmount()
  })
})

describe('PropertiesPanel replies', () => {
  beforeEach(() => get.mockReset())

  it('keeps the fields of the last note asked when an older answer lands later', async () => {
    let releaseOld: (f: RowFields) => void = () => {}
    get.mockImplementation((path: string) =>
      path === 'p/docs/tasks/T-1.md'
        ? new Promise((r) => (releaseOld = r))
        : Promise.resolve(fields({ values: { id: 'T-2', status: 'done' } })),
    )
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })
    const w = mount(PropertiesPanel, {
      props: { path: 'p/docs/tasks/T-1.md', etag: 'e1' },
      global: { plugins: [i18n], provide: { openWindow: vi.fn() } },
    })
    await w.setProps({ path: 'p/docs/tasks/T-2.md', etag: 'e2' })
    await flushPromises()
    releaseOld(fields({ values: { id: 'T-1', status: 'todo' } }))
    await flushPromises()
    expect(w.text()).toContain('T-2')
    expect(w.text()).not.toContain('T-1')
  })
})
