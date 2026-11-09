import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import enUI from '@catalogs/ui.en.json'
import itUI from '@catalogs/ui.it.json'
import { i18n as appI18n } from '@/locales'
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

async function mountPanel(f: RowFields, attachTo?: HTMLElement, locale: 'en' | 'it' = 'en') {
  get.mockResolvedValue(f)
  appI18n.global.locale.value = locale
  const i18n = createI18n({ legacy: false, locale, fallbackLocale: 'en', messages: { en: enUI, it: itUI } })
  const w = mount(PropertiesPanel, {
    props: { path: 'p/docs/tasks/T-1.md', etag: 'e1' },
    global: { plugins: [i18n], provide: { openWindow: vi.fn() } },
    attachTo,
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

  it('names a field in words, the key in its tooltip, and labels its editor so', async () => {
    const w = await mountPanel(fields())
    const status = w.find('dt[title="status"]')
    expect(status.text()).toBe('Status')
    // Named by the value it shows, then what a click does (IMP-220, F-22).
    const edit = w.find('dd[data-field="status"] button')
    expect(edit.attributes('aria-label')).toBeUndefined()
    expect(edit.text().replace(/\s+/g, ' ')).toMatch(/ \(edit Status\)$/)
    expect(w.find('dt[title="extra"]').text()).toBe('Extra')
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

// A note's tags as chips, in every markdown note (IMP-223, F-13).
describe('PropertiesPanel and the tags', () => {
  beforeEach(() => {
    get.mockReset()
    patch.mockReset().mockResolvedValue({} as never)
  })
  const chips = (w: Awaited<ReturnType<typeof mountPanel>>) =>
    w.findAll('[data-tag]').map((c) => c.attributes('data-tag'))

  it("puts a row's tags among its fields, after those with a value", async () => {
    const f = fields({ tags: ['p', 'type:task'] })
    f.columns!.push({ name: 'due', type: 'date' })
    const w = await mountPanel(f)
    expect(w.findAll('dl')[0]!.findAll('dd').map((d) => d.attributes('data-field'))).toEqual([
      'id',
      'status',
      'see',
      'tags',
      'due',
    ])
    const row = w.find('dd[data-field="tags"]')
    expect(row.find('[data-tag-field]').exists()).toBe(true)
    expect(chips(w)).toEqual(['p', 'type:task'])
    expect(w.find('dd[data-field="tags"]').element.previousElementSibling?.textContent).toBe('Tags')
  })

  it('gives any other note a line of its tags, which a writer changes', async () => {
    const w = await mountPanel({
      values: {},
      writable: true,
      tags: ['alpha'],
      tag_vocabulary: ['topic:x'],
    })
    expect(w.find('[data-properties]').exists()).toBe(false)
    const line = w.find('[data-note-tags]')
    expect(line.attributes('role')).toBe('group')
    expect(line.text()).toContain('Tags')
    expect(chips(w)).toEqual(['alpha'])
    await w.find('[data-tag="alpha"] button').trigger('click')
    await flushPromises()
    expect(patch).toHaveBeenCalledWith('p/docs/tasks/T-1.md', {
      unset: ['tags'],
      expect: { tags: ['alpha'] },
    })
    // The field stays, to add one again.
    expect(w.find('[data-note-tags] input[role="combobox"]').exists()).toBe(true)
  })

  it('shows nothing to a reader with no tags to see, nor for a note that is not markdown', async () => {
    let w = await mountPanel({ values: {}, writable: false, tags: [] })
    expect(w.find('[data-note-tags]').exists()).toBe(false)
    w = await mountPanel({ values: {}, writable: false, tags: null })
    expect(w.find('[data-note-tags]').exists()).toBe(false)
  })
})

describe('PropertiesPanel in the reader\'s language (IMP-220, F-23)', () => {
  beforeEach(() => {
    get.mockReset()
    patch.mockReset()
  })

  const task = (values: Record<string, string>): RowFields =>
    fields({
      columns: [
        { name: 'title', type: 'text' },
        { name: 'status', type: 'select', options: ['submitted', 'working'] },
        { name: 'claimed_by', type: 'text' },
        { name: 'lease_until', type: 'date' },
        { name: 'cost', type: 'number', labels: { it: 'Spesa' } },
      ],
      others: ['type'],
      values,
      links: {},
    })

  it('names the fields and the values in Italian, the codes in the tooltips, dates as read', async () => {
    const w = await mountPanel(
      task({ title: 'Uno', status: 'submitted', claimed_by: 'run-1', lease_until: '2026-10-31T00:41:54+01:00', cost: '2', type: 'task' }),
      undefined,
      'it',
    )
    const names = w.findAll('dt').map((d) => d.text())
    expect(names).toEqual(['Titolo', 'Stato', 'Preso da', 'Tenuto fino a', 'Spesa', 'Tipo'])
    expect(w.find('dt[title="lease_until"]').exists()).toBe(true)
    expect(w.find('dd[data-field="status"] [title="submitted"]').text()).toBe('inviato')
    const lease = w.find('dd[data-field="lease_until"] time')
    expect(lease.attributes('title')).toBe('2026-10-31T00:41:54+01:00')
    expect(lease.text()).toMatch(/ott 2026/)
    // The editor of a field is named by its name in Italian too.
    expect(w.find('dd[data-field="status"] button').text().replace(/\s+/g, ' ')).toMatch(/ \(modifica Stato\)$/)
  })

  it('puts the empty fields last, in the order the note opened with', async () => {
    const w = await mountPanel(task({ title: 'Uno', lease_until: '2026-11-03', cost: '2' }))
    const order = () => w.findAll('dd[data-field]').map((d) => d.attributes('data-field'))
    expect(order()).toEqual(['title', 'lease_until', 'cost', 'status', 'claimed_by', 'type'])
    // A field filled keeps its place until the note opens again.
    // Each answer is a new object, as from the server.
    get.mockImplementation(async () => task({ title: 'Uno', status: 'working', lease_until: '2026-11-03', cost: '2' }))
    await w.setProps({ etag: 'e2' })
    await flushPromises()
    expect(order()).toEqual(['title', 'lease_until', 'cost', 'status', 'claimed_by', 'type'])
    await w.setProps({ path: 'p/docs/tasks/T-2.md', etag: 'e3' })
    await flushPromises()
    expect(order()).toEqual(['title', 'status', 'lease_until', 'cost', 'claimed_by', 'type'])
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
      global: {
        plugins: [createI18n({ legacy: false, locale: 'en', messages: { en: enUI } })],
        provide: { openWindow: vi.fn() },
      },
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

describe('PropertiesPanel, the focus on the field changed (IMP-217, F-04)', () => {
  beforeEach(() => {
    get.mockReset()
    patch.mockReset().mockResolvedValue({} as never)
  })

  const statusButton = (w: Awaited<ReturnType<typeof mountPanel>>) =>
    w.find('dd[data-field="status"] button').element as HTMLButtonElement

  it('keeps it on the field once the value is saved', async () => {
    const w = await mountPanel(fields(), document.body)
    statusButton(w).focus()
    await w.find('dd[data-field="status"] button').trigger('click')
    await w.find('dd[data-field="status"] select').setValue('done')
    await flushPromises()
    expect(patch).toHaveBeenCalled()
    expect(document.activeElement).toBe(statusButton(w))
    w.unmount()
  })

  it('gives it back to the field on Esc', async () => {
    const w = await mountPanel(fields(), document.body)
    await w.find('dd[data-field="status"] button').trigger('click')
    await w.find('dd[data-field="status"] select').trigger('keydown', { key: 'Escape' })
    await flushPromises()
    expect(document.activeElement).toBe(statusButton(w))
    w.unmount()
  })

  it('leaves it where Tab took it', async () => {
    const w = await mountPanel(fields(), document.body)
    await w.find('dd[data-field="status"] button').trigger('click')
    const next = w.find('dd[data-field="see"] button').element as HTMLButtonElement
    next.focus()
    await flushPromises()
    expect(document.activeElement).toBe(next)
    w.unmount()
  })

  it('keeps it on a checkbox, disabled while it saves', async () => {
    const f = fields()
    f.columns!.push({ name: 'urgent', type: 'checkbox' })
    f.values.urgent = 'false'
    const w = await mountPanel(f, document.body)
    const box = w.find('dd[data-field="urgent"] input')
    ;(box.element as HTMLInputElement).focus()
    await box.setValue(true)
    await flushPromises()
    expect(patch).toHaveBeenCalled()
    expect(document.activeElement).toBe(w.find('dd[data-field="urgent"] input').element)
    w.unmount()
  })
})
